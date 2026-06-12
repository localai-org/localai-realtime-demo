# LocalAI realtime pipeline

[`models/gpt-realtime.yaml`](./models/gpt-realtime.yaml) defines the server-side
realtime pipeline this demo talks to. A LocalAI *realtime pipeline* stitches four
models into one endpoint:

| Stage           | Model                        | Role                        |
|-----------------|------------------------------|-----------------------------|
| `vad`           | `silero-vad-ggml`            | detect when the user speaks |
| `transcription` | `parakeet-cpp-tdt-0.6b-v3`   | speech → text               |
| `llm`           | `gemma-4-e2b-it-qat-q4_0`    | generate the reply          |
| `tts`           | `voice-it-paola-medium`      | text → speech (piper)       |

The compose file also installs `lfm2.5-8b-a1b` (a larger chat LLM) and
`qwen3-tts-cpp` (a neural multilingual voice) as heavier swap-in options —
point `llm:`/`tts:` in `gpt-realtime.yaml` at them to use them.

LocalAI then serves it at:

```
ws://localhost:8080/v1/realtime?model=gpt-realtime
```

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

for id in silero-vad-ggml parakeet-cpp-tdt-0.6b-v3 gemma-4-e2b-it-qat-q4_0 voice-it-paola-medium; do
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
