package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/mudler/minimal-realtime-assistant/audio"
	"github.com/mudler/minimal-realtime-assistant/internal/localvqe"
	"github.com/mudler/minimal-realtime-assistant/realtime"
	"github.com/mudler/minimal-realtime-assistant/tools"
)

// Compile-time guard: the purego binding must satisfy the AEC engine interface.
// Signature drift fails the build here rather than at runtime.
var _ audio.LocalVQEEngine = (*localvqe.LocalVQE)(nil)

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

func envBool(key string, def bool) bool {
	v := os.Getenv(key)
	switch v {
	case "1", "true", "TRUE", "True", "yes", "on":
		return true
	case "0", "false", "FALSE", "False", "no", "off":
		return false
	default:
		return def
	}
}

func main() {
	wsURL := flag.String("ws-url", env("OPENAI_WS_BASE_URL", "ws://localhost:8080/v1/realtime"), "LocalAI realtime WebSocket URL")
	apiKey := flag.String("api-key", env("OPENAI_API_KEY", "sk-xxx"), "API key (LocalAI ignores it)")
	model := flag.String("model", env("ASSISTANT_MODEL", "gpt-4o-realtime-preview"), "realtime model name served by LocalAI")
	voice := flag.String("voice", env("ASSISTANT_VOICE", ""), "TTS voice (empty = server default)")
	language := flag.String("language", env("ASSISTANT_LANGUAGE", ""), "input audio language as ISO-639-1, e.g. en/it (empty = auto-detect)")
	instructions := flag.String("instructions", env("ASSISTANT_INSTRUCTIONS",
		"You are a helpful voice assistant. Keep replies short and conversational. Use the get_weather tool when the user asks about the weather."),
		"system instructions")
	sampleRate := flag.Int("sample-rate", 24000, "PCM sample rate (Hz)")
	aecEnabled := flag.Bool("aec", envBool("AEC", true), "enable LocalVQE acoustic echo cancellation when a model is bundled into the binary")
	aecDelayMs := flag.Int("aec-delay-ms", envInt("AEC_DELAY_MS", 50), "AEC reference delay in ms (speaker->mic acoustic path)")
	fallbackWSURL := flag.String("fallback-ws-url", env("FALLBACK_WS_BASE_URL", "ws://localhost:8080/v1/realtime"), "fallback LocalAI realtime WebSocket URL")
	fallbackModel := flag.String("fallback-model", env("FALLBACK_MODEL", ""), "fallback realtime model (empty = same as -model)")
	fallbackAPIKey := flag.String("fallback-api-key", env("FALLBACK_API_KEY", ""), "fallback API key (empty = same as -api-key)")
	flag.Parse()

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	micOut := make(chan []byte, 64)
	player := audio.NewPlayer()

	// Optional acoustic echo cancellation. The LocalVQE lib + model are bundled
	// into the binary (see `make build`); when no model is bundled, AEC is simply
	// disabled and the mic passes through untouched.
	var aecOpts *audio.AECOptions
	var aecEngine *localvqe.LocalVQE
	if *aecEnabled {
		libPath, modelPath, ok, eerr := localvqe.EnsureEmbedded()
		switch {
		case eerr != nil:
			log.Printf("aec: disabled (embedded asset extraction failed: %v)", eerr)
		case !ok:
			log.Println("aec: disabled (no bundled LocalVQE model; build with `make` to bundle one)")
		default:
			engine, err := localvqe.New(libPath, modelPath)
			if err != nil {
				log.Printf("aec: disabled (%v)", err)
			} else {
				aecEngine = engine
				aecOpts = &audio.AECOptions{Engine: engine, DelayMs: *aecDelayMs}
				log.Printf("aec: enabled (delay=%dms)", *aecDelayMs)
			}
		}
	}

	// Audio device runs for the lifetime of the session.
	audioDone := make(chan struct{})
	go func() {
		defer close(audioDone)
		if err := audio.Duplex(ctx, *sampleRate, micOut, player, aecOpts); err != nil {
			log.Println("audio:", err)
			cancel()
		}
	}()
	// Free the LocalVQE engine only after Duplex (and its AEC worker) have
	// stopped, so a worker mid-drain can never touch a freed context on exit.
	if aecEngine != nil {
		defer func() {
			cancel()
			<-audioDone
			aecEngine.Close()
		}()
	}

	// Tools.
	registry := realtime.NewRegistry()
	weather, err := tools.NewWeather()
	if err != nil {
		log.Fatalln("init weather tool:", err)
	}
	registry.Register(weather)

	// Endpoints: primary first, then fallback. Failover walks this list from the
	// top on every (re)connect, which also gives automatic fail-back to primary.
	endpoints := []realtime.Endpoint{
		{Name: "primary", WSURL: *wsURL, Model: *model, APIKey: *apiKey},
		{Name: "fallback", WSURL: *fallbackWSURL, Model: orDefault(*fallbackModel, *model), APIKey: orDefault(*fallbackAPIKey, *apiKey)},
	}
	if endpoints[0].WSURL == endpoints[1].WSURL &&
		endpoints[0].Model == endpoints[1].Model &&
		endpoints[0].APIKey == endpoints[1].APIKey {
		log.Println("fallback endpoint is identical to primary; failover is a no-op")
	}

	// Current connected session, swapped on each (re)connect. The mic-forwarding
	// goroutine routes captured audio to whichever session is live; chunks
	// captured while reconnecting are dropped (context is lost on switch).
	var (
		curMu sync.Mutex
		cur   realtime.Session
	)
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case chunk := <-micOut:
				curMu.Lock()
				s := cur
				curMu.Unlock()
				if s == nil {
					continue
				}
				if err := s.SendAudio(ctx, chunk); err != nil {
					log.Println("send audio:", err)
				}
			}
		}
	}()

	sup := &realtime.Supervisor{
		Endpoints: endpoints,
		Dial: func(ctx context.Context, ep realtime.Endpoint) (realtime.Session, error) {
			client := realtime.NewClient(realtime.Config{
				WSURL:        ep.WSURL,
				APIKey:       ep.APIKey,
				Model:        ep.Model,
				Voice:        *voice,
				Instructions: *instructions,
				Language:     *language,
				SampleRate:   *sampleRate,
				Timeout:      30 * time.Second,
			}, registry, player)
			if err := client.Connect(ctx); err != nil {
				return nil, err
			}
			log.Printf("connected to %s (%s)", ep.WSURL, ep.Name)
			return client, nil
		},
		OnConnect: func(s realtime.Session) {
			curMu.Lock()
			cur = s
			curMu.Unlock()
		},
		OnSwitch: func(from, to *realtime.Endpoint) {
			// Ascending sweep when moving toward the primary (lower index),
			// descending toward the fallback. from is non-nil here.
			if endpointIndex(endpoints, to) < endpointIndex(endpoints, from) {
				player.Write(audio.ToneSweep(*sampleRate, 440, 660, 200*time.Millisecond))
			} else {
				player.Write(audio.ToneSweep(*sampleRate, 660, 440, 200*time.Millisecond))
			}
		},
		OnDisconnect: func() {
			curMu.Lock()
			cur = nil
			curMu.Unlock()
		},
		Backoff: realtime.BackoffPolicy{Min: time.Second, Max: 30 * time.Second},
	}

	log.Printf("starting (primary=%s fallback=%s) — start talking (Ctrl-C to quit)", *wsURL, *fallbackWSURL)
	if err := sup.Run(ctx); err != nil && ctx.Err() == nil {
		log.Println("supervisor:", err)
	}
}

func orDefault(v, def string) string {
	if v != "" {
		return v
	}
	return def
}

// endpointIndex returns the position of ep within eps by pointer identity. The
// Supervisor passes pointers into this same slice, so identity is exact.
func endpointIndex(eps []realtime.Endpoint, ep *realtime.Endpoint) int {
	for i := range eps {
		if &eps[i] == ep {
			return i
		}
	}
	return -1
}
