package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
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
	instructions := flag.String("instructions", env("ASSISTANT_INSTRUCTIONS",
		"You are a helpful voice assistant. Keep replies short and conversational. Use the get_weather tool when the user asks about the weather."),
		"system instructions")
	sampleRate := flag.Int("sample-rate", 24000, "PCM sample rate (Hz)")
	aecEnabled := flag.Bool("aec", envBool("AEC", true), "enable LocalVQE acoustic echo cancellation when the library and model are available")
	localvqeLib := flag.String("localvqe-lib", env("LOCALVQE_LIB", "liblocalvqe.so"), "path to liblocalvqe.so")
	localvqeModel := flag.String("localvqe-model", env("LOCALVQE_MODEL", ""), "path to the LocalVQE GGUF model")
	aecDelayMs := flag.Int("aec-delay-ms", 50, "AEC reference delay in ms (speaker->mic acoustic path)")
	flag.Parse()

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	micOut := make(chan []byte, 64)
	playIn := make(chan []byte, 256)

	// Optional acoustic echo cancellation. On any setup problem we log once and
	// fall back to passthrough rather than failing the session.
	var aecOpts *audio.AECOptions
	if *aecEnabled {
		switch {
		case *localvqeModel == "":
			log.Println("aec: disabled (no LOCALVQE_MODEL / -localvqe-model set)")
		default:
			engine, err := localvqe.New(*localvqeLib, *localvqeModel)
			if err != nil {
				log.Printf("aec: disabled (%v)", err)
			} else {
				defer engine.Close()
				aecOpts = &audio.AECOptions{Engine: engine, DelayMs: *aecDelayMs}
				log.Printf("aec: enabled (lib=%s model=%s delay=%dms)", *localvqeLib, *localvqeModel, *aecDelayMs)
			}
		}
	}

	// Audio device runs for the lifetime of the session.
	go func() {
		if err := audio.Duplex(ctx, *sampleRate, micOut, playIn, aecOpts); err != nil {
			log.Println("audio:", err)
			cancel()
		}
	}()

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
		SampleRate:   *sampleRate,
		Timeout:      30 * time.Second,
	}, registry, playIn)

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
