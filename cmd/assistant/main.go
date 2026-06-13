package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"strconv"
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
	// `setup` is a pre-boot subcommand: it scaffolds a hardware-appropriate
	// docker-compose stack and exits, before any LocalAI exists. Dispatch it
	// ahead of the normal connect path so it owns its own flag set.
	if len(os.Args) > 1 && os.Args[1] == "setup" {
		if err := runSetup(os.Args[2:]); err != nil {
			log.Fatalf("setup: %v", err)
		}
		return
	}

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

	// Client.
	client := realtime.NewClient(realtime.Config{
		WSURL:        *wsURL,
		APIKey:       *apiKey,
		Model:        *model,
		Voice:        *voice,
		Instructions: *instructions,
		Language:     *language,
		SampleRate:   *sampleRate,
		Timeout:      30 * time.Second,
	}, registry, player)

	if err := client.Connect(ctx); err != nil {
		log.Fatalln("connect:", err)
	}
	log.Printf("connected to %s — start talking (Ctrl-C to quit)", *wsURL)

	// Forward captured mic audio to the server.
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case chunk := <-micOut:
				if err := client.SendAudio(ctx, chunk); err != nil {
					log.Println("send audio:", err)
				}
			}
		}
	}()

	if err := client.Run(ctx); err != nil && ctx.Err() == nil {
		log.Println("run:", err)
	}
}
