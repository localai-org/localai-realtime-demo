package realtime

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"log"
	"net/http"
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
}

// Client wraps the realtime WebSocket connection and dispatches server events.
type Client struct {
	cfg      Config
	registry *Registry
	playIn   chan<- []byte
	rt       *openairt.Client
	conn     *openairt.Conn
}

func NewClient(cfg Config, registry *Registry, playIn chan<- []byte) *Client {
	rtConf := openairt.DefaultConfig(cfg.APIKey)
	rtConf.BaseURL = cfg.WSURL
	rtConf.HTTPClient = &http.Client{Timeout: cfg.Timeout}
	return &Client{
		cfg:      cfg,
		registry: registry,
		playIn:   playIn,
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
		return fmt.Errorf("wait for session: %w", err)
	}
	if err := c.updateSession(ctx); err != nil {
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
	for {
		msg, err := c.conn.ReadMessage(ctx)
		if err != nil {
			var permanent *openairt.PermanentError
			if errors.As(err, &permanent) {
				return fmt.Errorf("connection failed: %w", err)
			}
			if ctx.Err() != nil {
				return ctx.Err()
			}
			log.Println("realtime: read error, retrying:", err)
			continue
		}

		switch msg.ServerEventType() {
		case openairt.ServerEventTypeInputAudioBufferSpeechStarted:
			log.Println("realtime: speech detected")
		case openairt.ServerEventTypeInputAudioBufferSpeechStopped:
			log.Println("realtime: speech stopped")
		case openairt.ServerEventTypeConversationItemInputAudioTranscriptionCompleted:
			if ev, ok := msg.(openairt.ConversationItemInputAudioTranscriptionCompletedEvent); ok {
				log.Printf("you said: %s", ev.Transcript)
			}
		case openairt.ServerEventTypeResponseCreated:
			log.Println("realtime: generating response")
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
			select {
			case c.playIn <- pcm:
			default:
				log.Println("realtime: dropped playback chunk")
			}
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
