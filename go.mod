module github.com/mudler/minimal-realtime-assistant

go 1.24.2

require (
	github.com/WqyJh/go-openai-realtime/v2 v2.0.0-rc.0.20260120095754-b1a91a348dbd
	github.com/gen2brain/malgo v0.11.25
	github.com/sashabaranov/go-openai v1.41.2
)

require github.com/coder/websocket v1.8.12 // indirect

replace github.com/WqyJh/go-openai-realtime/v2 => github.com/richiejp/go-openai-realtime/v2 v2.0.0-20260213113003-1b6db572709e
