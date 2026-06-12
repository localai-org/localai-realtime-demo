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
	"github.com/mudler/minimal-realtime-assistant/realtime"
	"github.com/mudler/minimal-realtime-assistant/tools"
)

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
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
	flag.Parse()

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	micOut := make(chan []byte, 64)
	playIn := make(chan []byte, 256)

	// Audio device runs for the lifetime of the session.
	go func() {
		if err := audio.Duplex(ctx, *sampleRate, micOut, playIn); err != nil {
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
		Language:     *language,
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
