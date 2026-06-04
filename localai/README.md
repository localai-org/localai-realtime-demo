# LocalAI realtime pipeline

[`gpt-realtime.yaml`](./gpt-realtime.yaml) defines the server-side realtime
pipeline this demo talks to. A LocalAI *realtime pipeline* stitches four models
into one endpoint:

| Stage         | Model                                                  | Role                              |
|---------------|--------------------------------------------------------|-----------------------------------|
| `vad`         | `silero-vad-ggml`                                      | detect when the user speaks       |
| `transcription` | `parakeet-cpp-tdt-0.6b-v3`                           | speech → text                     |
| `llm`         | `Qwen3.6-35B-A3B-Claude-4.7-Opus-Reasoning-Distilled-APEX-GGUF` | generate the reply       |
| `tts`         | `qwen3-tts-cpp`                                        | text → speech                     |

LocalAI then serves it at:

```
ws://<host>:<port>/v1/realtime?model=gpt-realtime
```

## Prerequisites: install the sub-models

Every model named in the pipeline must exist in the instance. Install any that
are missing from the gallery (the API is async and returns a job UUID you can
poll at `/models/jobs/<uuid>`):

```bash
# host/key come from your environment (e.g. OPENAI_BASE_URL / OPENAI_API_KEY)
BASE=http://<host>:<port>/v1

for id in silero-vad-ggml parakeet-cpp-tdt-0.6b-v3 qwen3-tts-cpp; do
  curl -sS "$BASE/models/apply" \
    -H "Authorization: Bearer $OPENAI_API_KEY" \
    -H 'Content-Type: application/json' \
    -d "{\"id\":\"$id\"}"
  echo
done
```

The LLM (`Qwen3.6-35B-A3B-…-APEX-GGUF`) is assumed already present; swap in any
chat model you prefer by editing the `llm:` field.

## Deploy the pipeline config

`gpt-realtime.yaml` must live in the LocalAI instance's **models directory**
(the same path that holds the other model configs, typically `/models` or
`./models`). LocalAI loads `*.yaml` from there on startup / hot-reload. Drop the
file in by whatever mechanism fits your deployment, e.g.:

```bash
# bare metal / docker bind mount
cp gpt-realtime.yaml /path/to/localai/models/

# kubernetes (config mounted from a ConfigMap)
kubectl create configmap gpt-realtime --from-file=gpt-realtime.yaml \
  -o yaml --dry-run=client | kubectl apply -f -
#   ...then mount it into the models volume and restart/reload.
```

Confirm it registered:

```bash
curl -sS "$BASE/models" -H "Authorization: Bearer $OPENAI_API_KEY" \
  | grep -o gpt-realtime
```

## Test it

From this repo's root, point the demo client at the instance and talk:

```bash
CGO_ENABLED=1 go build -o assistant ./cmd/assistant
./assistant -ws-url ws://<host>:<port>/v1/realtime -model gpt-realtime
```
