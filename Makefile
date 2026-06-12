# Default model bundled into the binary. Swap with:
#   make build LOCALVQE_MODEL_FILE=localvqe-v1.3-4.8M-f32.gguf
LOCALVQE_MODEL_FILE ?= localvqe-v1.2-1.3M-f32.gguf

.DEFAULT_GOAL := all

.PHONY: all build build-noembed localvqe test vet

all: build

# Produce the embedded AEC assets (host lib build + model download).
localvqe:
	LOCALVQE_MODEL_FILE=$(LOCALVQE_MODEL_FILE) bash scripts/fetch-localvqe.sh

# Self-contained binary with LocalVQE lib + model bundled in.
build: localvqe
	go build -tags localvqe_embed -o bin/assistant ./cmd/assistant

# Binary with nothing bundled: AEC is disabled (mic passthrough).
build-noembed:
	go build -o bin/assistant ./cmd/assistant

test:
	go test ./...

vet:
	go vet ./...
