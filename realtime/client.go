package realtime

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"log"
	"net/http"
	"sync/atomic"
	"time"

	openairt "github.com/WqyJh/go-openai-realtime/v2"
)

type Config struct {
	WSURL        string
	APIKey       string
	Model        string
	Voice        string
	Instructions string
	Language     string // ISO-639-1 input-audio language (empty = server auto-detect)
	SampleRate   int
	Timeout      time.Duration
	// IdleReset ends the session after this long with no server activity (no
	// speech, transcription, or response), so the Supervisor reconnects with a
	// fresh server-side conversation. 0 disables it (keep one session forever).
	IdleReset time.Duration
	// PingInterval is how often a WebSocket ping is sent to detect a dead link.
	// A silently-dropped network (interface down, no TCP FIN/RST) never surfaces
	// a read error on its own; a ping with no pong within PingTimeout does, so
	// the session ends and the Supervisor fails over. 0 disables the keepalive.
	PingInterval time.Duration
	// PingTimeout bounds how long each keepalive ping waits for its pong before
	// the link is considered dead. Only used when PingInterval > 0.
	PingTimeout time.Duration
}

// Player consumes assistant playback audio. Write appends decoded PCM; Flush
// drops everything still buffered (used for barge-in) and returns the dropped
// byte count.
type Player interface {
	Write(pcm []byte)
	Flush() int
}

// Client wraps the realtime WebSocket connection and dispatches server events.
type Client struct {
	cfg      Config
	registry *Registry
	player   Player
	rt       *openairt.Client
	conn     *openairt.Conn

	// responseActive tracks whether the server is generating a response, so
	// barge-in only sends response.cancel when there is something to cancel.
	// Touched only from the single-threaded Run loop.
	responseActive bool
}

func NewClient(cfg Config, registry *Registry, player Player) *Client {
	rtConf := openairt.DefaultConfig(cfg.APIKey)
	rtConf.BaseURL = cfg.WSURL
	rtConf.HTTPClient = &http.Client{Timeout: cfg.Timeout}
	return &Client{
		cfg:      cfg,
		registry: registry,
		player:   player,
		rt:       openairt.NewClientWithConfig(rtConf),
	}
}

// Connect dials the server, waits for the session to be created, then sends the
// session configuration (instructions, audio formats, VAD, tools).
func (c *Client) Connect(ctx context.Context) error {
	opts := []openairt.ConnectOption{}
	if c.cfg.Model != "" {
		opts = append(opts, openairt.WithModel(c.cfg.Model))
	}
	conn, err := c.rt.Connect(ctx, opts...)
	if err != nil {
		return fmt.Errorf("realtime connect: %w", err)
	}
	c.conn = conn

	if err := c.waitForSession(ctx); err != nil {
		c.conn.Close()
		c.conn = nil
		return fmt.Errorf("wait for session: %w", err)
	}
	if err := c.updateSession(ctx); err != nil {
		c.conn.Close()
		c.conn = nil
		return fmt.Errorf("update session: %w", err)
	}
	return nil
}

func (c *Client) waitForSession(ctx context.Context) error {
	for {
		msg, err := c.conn.ReadMessage(ctx)
		if err != nil {
			var permanent *openairt.PermanentError
			if errors.As(err, &permanent) {
				return err
			}
			if ctx.Err() != nil {
				return ctx.Err()
			}
			time.Sleep(250 * time.Millisecond)
			continue
		}
		switch msg.ServerEventType() {
		case openairt.ServerEventTypeError:
			if ev, ok := msg.(openairt.ErrorEvent); ok {
				log.Println("realtime: server error:", ev.Error.Message)
			}
		case openairt.ServerEventTypeSessionCreated, openairt.ServerEventTypeSessionUpdated:
			return nil
		}
	}
}

func (c *Client) updateSession(ctx context.Context) error {
	voice := openairt.Voice("")
	if c.cfg.Voice != "" {
		voice = openairt.Voice(c.cfg.Voice)
	}
	// Forcing the input language helps the STT model when the spoken language is
	// known; left empty the server (e.g. parakeet) auto-detects.
	var transcription *openairt.AudioTranscription
	if c.cfg.Language != "" {
		transcription = &openairt.AudioTranscription{Language: c.cfg.Language}
	}
	return c.conn.SendMessage(ctx, openairt.SessionUpdateEvent{
		EventBase: openairt.EventBase{EventID: "session-init"},
		Session: openairt.SessionUnion{
			Realtime: &openairt.RealtimeSession{
				Instructions: c.cfg.Instructions,
				Audio: &openairt.RealtimeSessionAudio{
					Input: &openairt.SessionAudioInput{
						Format: &openairt.AudioFormatUnion{
							PCM: &openairt.AudioFormatPCM{Rate: c.cfg.SampleRate},
						},
						TurnDetection: &openairt.TurnDetectionUnion{
							ServerVad: &openairt.ServerVad{},
						},
						Transcription: transcription,
					},
					Output: &openairt.SessionAudioOutput{
						Voice: voice,
						Format: &openairt.AudioFormatUnion{
							PCM: &openairt.AudioFormatPCM{Rate: c.cfg.SampleRate},
						},
					},
				},
				Tools: c.registry.ToolUnions(),
			},
		},
	})
}

// SendAudio appends a PCM16 chunk to the input audio buffer.
func (c *Client) SendAudio(ctx context.Context, pcm []byte) error {
	if len(pcm) == 0 {
		return nil
	}
	return c.conn.SendMessage(ctx, openairt.InputAudioBufferAppendEvent{
		EventBase: openairt.EventBase{EventID: "audio"},
		Audio:     base64.StdEncoding.EncodeToString(pcm),
	})
}

// Run reads server events until the context is cancelled or the connection
// fails permanently.
func (c *Client) Run(ctx context.Context) error {
	// Watchdogs run the read loop against a derived context they can cancel to
	// end the session cleanly so the Supervisor reconnects:
	//   - idle reset: after IdleReset of no server activity (fresh conversation).
	//   - keepalive: when a ping gets no pong, i.e. the link died silently.
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	var lastActivity atomic.Int64
	if c.cfg.IdleReset > 0 {
		lastActivity.Store(time.Now().UnixNano())
		go c.idleWatch(runCtx, &lastActivity, cancel)
	}

	if c.cfg.PingInterval > 0 {
		go func() {
			if err := keepalive(runCtx, c.conn, c.cfg.PingInterval, c.cfg.PingTimeout); err != nil {
				log.Printf("realtime: %v — ending session to reconnect", err)
				cancel()
			}
		}()
	}

	for {
		msg, err := c.conn.ReadMessage(runCtx)
		if err != nil {
			var permanent *openairt.PermanentError
			if errors.As(err, &permanent) {
				return fmt.Errorf("connection failed: %w", err)
			}
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if runCtx.Err() != nil {
				// A watchdog (idle reset or keepalive) fired: a clean end so the
				// Supervisor reconnects.
				return nil
			}
			log.Println("realtime: read error, retrying:", err)
			continue
		}

		// Any server event counts as activity (user speech, transcription, or an
		// in-progress response), so we only reset when the line is truly quiet.
		if c.cfg.IdleReset > 0 {
			lastActivity.Store(time.Now().UnixNano())
		}

		switch msg.ServerEventType() {
		case openairt.ServerEventTypeInputAudioBufferSpeechStarted:
			log.Println("realtime: speech detected")
			c.bargeIn(ctx)
		case openairt.ServerEventTypeInputAudioBufferSpeechStopped:
			log.Println("realtime: speech stopped")
		case openairt.ServerEventTypeConversationItemInputAudioTranscriptionCompleted:
			if ev, ok := msg.(openairt.ConversationItemInputAudioTranscriptionCompletedEvent); ok {
				log.Printf("you said: %s", ev.Transcript)
			}
		case openairt.ServerEventTypeResponseCreated:
			log.Println("realtime: generating response")
			c.responseActive = true
		case openairt.ServerEventTypeResponseDone:
			c.responseActive = false
		case openairt.ServerEventTypeResponseOutputAudioDelta:
			ev, ok := msg.(openairt.ResponseOutputAudioDeltaEvent)
			if !ok {
				continue
			}
			pcm, err := base64.StdEncoding.DecodeString(ev.Delta)
			if err != nil {
				log.Println("realtime: decode audio delta:", err)
				continue
			}
			c.player.Write(pcm)
		case openairt.ServerEventTypeResponseFunctionCallArgumentsDone:
			if ev, ok := msg.(openairt.ResponseFunctionCallArgumentsDoneEvent); ok {
				c.handleFunctionCall(ctx, ev)
			}
		case openairt.ServerEventTypeError:
			if ev, ok := msg.(openairt.ErrorEvent); ok {
				log.Println("realtime: server error:", ev.Error.Message)
			}
		}
	}
}

// idleWatch cancels the run context once no server activity has been seen for
// IdleReset, ending the session so the Supervisor reconnects with a fresh
// conversation. It exits when ctx is cancelled (session ended for any reason).
func (c *Client) idleWatch(ctx context.Context, last *atomic.Int64, reset context.CancelFunc) {
	interval := c.cfg.IdleReset / 3
	if interval < time.Second {
		interval = time.Second
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if time.Since(time.Unix(0, last.Load())) >= c.cfg.IdleReset {
				log.Printf("realtime: no activity for %s — resetting conversation", c.cfg.IdleReset)
				reset()
				return
			}
		}
	}
}

// Close shuts down the underlying WebSocket connection. It is safe to call once
// after Run returns; the Supervisor calls it when a session ends so that an
// abrupt disconnect does not leak the connection's goroutine and file
// descriptor.
func (c *Client) Close() error {
	if c.conn == nil {
		return nil
	}
	return c.conn.Close()
}

// bargeIn handles the user talking over the assistant. It always flushes the
// local playback buffer — the server streams a whole response in a burst, so
// by the time speech is detected it has usually finished sending and seconds of
// TTS remain buffered locally; cancelling server-side alone would not silence
// it. response.cancel is only sent when a response is genuinely still active
// (the server errors on a cancel with nothing to cancel).
func (c *Client) bargeIn(ctx context.Context) {
	if dropped := c.player.Flush(); dropped > 0 {
		log.Printf("realtime: barge-in, flushed %d bytes of playback", dropped)
	}
	if !c.responseActive {
		return
	}
	c.responseActive = false
	if err := c.conn.SendMessage(ctx, openairt.ResponseCancelEvent{}); err != nil {
		log.Println("realtime: cancel response:", err)
	}
}

func (c *Client) handleFunctionCall(ctx context.Context, ev openairt.ResponseFunctionCallArgumentsDoneEvent) {
	log.Printf("tool call: %s(%s)", ev.Name, ev.Arguments)
	tool, ok := c.registry.Get(ev.Name)
	if !ok {
		log.Printf("realtime: unknown tool %q", ev.Name)
		return
	}
	output, err := tool.Execute(ctx, ev.Arguments)
	if err != nil {
		log.Printf("realtime: tool %s failed: %v", ev.Name, err)
		output = fmt.Sprintf("error: %v", err)
	}
	if err := c.conn.SendMessage(ctx, openairt.ConversationItemCreateEvent{
		Item: openairt.MessageItemUnion{
			FunctionCallOutput: &openairt.MessageItemFunctionCallOutput{
				CallID: ev.CallID,
				Output: output,
			},
		},
	}); err != nil {
		log.Println("realtime: send function output:", err)
		return
	}
	// Ask the model to speak the tool result.
	if err := c.conn.SendMessage(ctx, openairt.ResponseCreateEvent{}); err != nil {
		log.Println("realtime: trigger response:", err)
	}
}
