# LocalAI realtime pipeline

[`models/gpt-realtime.yaml`](./models/gpt-realtime.yaml) defines the server-side
realtime pipeline this demo talks to. A LocalAI *realtime pipeline* stitches four
models into one endpoint:

| Stage           | Model                        | Role                        |
|-----------------|------------------------------|-----------------------------|
| `vad`           | `silero-vad-ggml`            | detect when the user speaks |
| `transcription` | `parakeet-cpp-tdt-0.6b-v3`   | speech → text               |
| `llm`           | `gemma-4-e2b-it-qat-q4_0`    | generate the reply          |
| `tts`           | `vits-piper-it_IT-paola-sherpa` | text → speech (sherpa-onnx, streaming) |

The `tts` stage uses the **sherpa-onnx** backend, which supports streaming
synthesis, so `gpt-realtime.yaml` enables a `streaming` block to pipeline the
reply into TTS clause-by-clause (see [Streaming TTS](#streaming-tts) below).
Swap `llm:`/`tts:` in `gpt-realtime.yaml` for other models — e.g. `qwen3-tts-cpp`
for a neural multilingual voice (non-streaming), or `lfm2.5-8b-a1b` for a larger
chat LLM.

LocalAI then serves it at:

```
ws://localhost:8080/v1/realtime?model=gpt-realtime
```

## Streaming TTS

By default a realtime pipeline runs each stage to completion before the next
begins: the full reply is generated, then synthesized, then played. The
`streaming` block in `gpt-realtime.yaml` opts stages into incremental delivery
to cut the time-to-first-audio of a turn:

```yaml
pipeline:
  # ...
  tts: vits-piper-it_IT-paola-sherpa
  streaming:
    llm: true              # stream the LLM tokens as they are produced
    tts: true              # emit an audio delta per synthesized chunk
    clause_chunking: true  # synthesize each clause as soon as it completes
                           # (requires llm: true)
```

- `streaming.tts` only helps with a **TTS backend that supports streaming
  synthesis** — otherwise LocalAI falls back to a single audio delta for the
  whole utterance.
- `streaming.clause_chunking` is where the real win comes from: instead of
  buffering the whole reply, the LLM output is split into speakable clauses and
  each is synthesized (and starts playing) while the LLM keeps generating. The
  benefit shows on multi-sentence replies; a short one-clause reply still
  synthesizes once at the end, since there is no earlier boundary to speak.

### TTS backends / models that support streaming

These backends implement streaming synthesis (`TTSStream`), so `streaming.tts`
takes effect with a model served by one of them. Anything else degrades
gracefully to non-streaming.

| Backend          | Streaming | Example gallery models |
|------------------|-----------|------------------------|
| **sherpa-onnx**  | ✅ | `vits-piper-it_IT-paola-sherpa`, `vits-piper-en_US-amy-sherpa`, `vits-piper-es_ES-davefx-sherpa`, `vits-piper-fr_FR-siwis-sherpa`, `vits-piper-de_DE-thorsten-sherpa`, `kokoro-multi-lang-v1.0-sherpa`, `vits-ljs-sherpa` |
| **voxcpm**       | ✅ | `voxcpm-1.5` |
| **vibevoice-cpp**| ✅ | `vibevoice-cpp` |
| **omnivoice-cpp**| ✅ | `omnivoice-cpp`, `omnivoice-cpp-hq` |
| piper, qwen3-tts-cpp, kokoro, coqui, … | ❌ (file only) | `voice-it-paola-medium`, `qwen3-tts-cpp` |

The default pipeline uses `vits-piper-it_IT-paola-sherpa` — the same Italian
"Paola" voice as the old piper `voice-it-paola-medium`, but served through
sherpa-onnx so it streams. To use a different streaming voice, install it from
the gallery and point `tts:` at it; for non-Italian languages pick the matching
`vits-piper-*-sherpa` voice or the multilingual `kokoro-multi-lang-v1.0-sherpa`.

## Quick start (Docker Compose)

From the repo root, the included [`docker-compose.yml`](../docker-compose.yml)
brings the whole backend up and installs the sub-models for you:

```bash
docker compose up
```

First start downloads the model weights from the gallery (a few minutes — watch
the logs; the container's `/readyz` flips healthy once it's ready). Weights are
cached in `localai/models/`, so later starts are fast.

Confirm the pipeline registered:

```bash
curl -sS http://localhost:8080/models | grep -o gpt-realtime
```

Then talk to it from the repo root:

```bash
CGO_ENABLED=1 go build -o assistant ./cmd/assistant
./assistant -model gpt-realtime
```

The client defaults to `ws://localhost:8080/v1/realtime`, so no extra flags are
needed against the compose stack.

## Using your own LocalAI instance

If you already run LocalAI elsewhere, you don't need the compose file — just
make the sub-models available and deploy the pipeline config.

### Install the sub-models

Every model named in the pipeline must exist in the instance. Install any that
are missing from the gallery (the API is async and returns a job UUID you can
poll at `/models/jobs/<uuid>`):

```bash
BASE=http://<host>:<port>/v1   # add -H "Authorization: Bearer $KEY" if auth is on

for id in silero-vad-ggml parakeet-cpp-tdt-0.6b-v3 gemma-4-e2b-it-qat-q4_0 vits-piper-it_IT-paola-sherpa; do
  curl -sS "$BASE/models/apply" -H 'Content-Type: application/json' -d "{\"id\":\"$id\"}"
  echo
done
```

Swap in any chat model you prefer by editing the `llm:` field in
`models/gpt-realtime.yaml` (and installing that model instead of
`gemma-4-e2b-it-qat-q4_0`).

### Deploy the pipeline config

`gpt-realtime.yaml` must live in the LocalAI instance's **models directory** (the
same path that holds the other model configs, typically `/models`). LocalAI
loads `*.yaml` from there on startup / hot-reload:

```bash
# bare metal / docker bind mount
cp models/gpt-realtime.yaml /path/to/localai/models/

# kubernetes (config mounted from a ConfigMap)
kubectl create configmap gpt-realtime --from-file=models/gpt-realtime.yaml \
  -o yaml --dry-run=client | kubectl apply -f -
#   ...then mount it into the models volume and restart/reload.
```

Then point the client at it:

```bash
./assistant -ws-url ws://<host>:<port>/v1/realtime -model gpt-realtime
```
