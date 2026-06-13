# `assistant setup` — docker-compose scaffolder (hardware + models)

**Status:** design
**Date:** 2026-06-13
**Scope:** minimal-realtime-assistant (`assistant` CLI) only. This replaces the
earlier API-based "setup wizard" idea (which needed a running LocalAI — a
chicken-and-egg for an embedded/example target). prep-buddy gets a separate,
web-UI-only *live provisioner* (subsystem ①) — see that repo's spec.

## Why a scaffolder, not a runtime provisioner

The realtime-demo is **less "live"**: it targets embedded systems, boards, and
serves as an *example* rather than a full app. The hard part for those is bringing
LocalAI up correctly for the hardware (right image variant, GPU stanza) — and a
runtime model provisioner can't help with that because it requires LocalAI to
*already be running*. So the demo's setup is a **pre-boot docker-compose
scaffolder**: it runs before any LocalAI exists, picks the hardware-appropriate
image + GPU wiring, bakes the chosen models into the compose, and leaves you with
a `docker compose up` away from a working stack. Model selection folds in here (no
live API, no PATCH) — chosen models go into the gallery `command:` install list
and into `gpt-realtime.yaml`.

## Goals

- `assistant setup` — an **interactive wizard** that, with **no running LocalAI**:
  1. determines target hardware (best-effort **detect, then confirm** via a menu),
  2. lets the user pick the **LLM** and **TTS** model (curated preferred set; "show
     all" optional),
  3. writes a `docker-compose.yml` with the right LocalAI **image variant** + GPU
     **deploy stanza** + the chosen models in the `command:` install list, and
  4. writes the chosen `pipeline.llm` / `pipeline.tts` into
     `localai/models/gpt-realtime.yaml`.
- Output is ready to `docker compose up`; first boot gallery-installs the baked
  models (the existing mechanism) — which also pulls their backends.
- Works when scaffolding for a *different* machine than the one you run the wizard
  on (e.g. generate on a laptop for a Jetson).

## Non-goals

- No live/runtime model switching, no LocalAI HTTP API calls, no web UI (that is
  prep-buddy / subsystem ①).
- No transcription/VAD selection this round (defaults kept).
- Not a general k8s/helm generator — docker-compose only.

## Hardware → LocalAI image variant (mapping table)

From LocalAI's published image matrix:

| Target | Image tag | GPU wiring |
|---|---|---|
| CPU | `localai/localai:master` | none |
| NVIDIA CUDA 12 | `…:master-gpu-nvidia-cuda-12` | `deploy.resources.reservations.devices` (nvidia, count all, `[gpu,utility]`) |
| NVIDIA CUDA 13 | `…:master-gpu-nvidia-cuda-13` | same nvidia stanza |
| AMD ROCm | `…:master-gpu-hipblas` | device mounts `/dev/kfd`, `/dev/dri`; group_add render/video |
| Intel | `…:master-gpu-intel` | `/dev/dri` mount |
| Vulkan | `…:master-vulkan` | `/dev/dri` mount |
| Apple Metal (arm64) | `…:master-metal-darwin-arm64` | none (Docker Desktop; note: GPU passthrough limited) |
| NVIDIA Jetson (L4T) | `…:master-nvidia-l4t-arm64` (or `-cuda-13`) | nvidia runtime stanza |

The NVIDIA reservation stanza (seeded from LocalAI's example compose):

```yaml
deploy:
  resources:
    reservations:
      devices:
        - driver: nvidia.com/gpu
          count: all
          capabilities: [gpu, utility]
```

Per-vendor stanzas live as small templates in the scaffolder, kept in sync with
LocalAI's example composes (`docker-compose.yaml` and docs).

## Wizard UX

```
$ ./assistant setup
Detecting hardware… NVIDIA GPU (CUDA 12) detected.

Target hardware?
 > NVIDIA CUDA 12   (detected)        -> image master-gpu-nvidia-cuda-12 + GPU stanza
   NVIDIA CUDA 13
   CPU
   AMD ROCm / Intel / Vulkan / Apple Metal / NVIDIA Jetson
   (override freely)

LLM:
 > gemma-4-e2b-it-qat-q4_0    fast on CPU/GPU, reasoning_effort:none
   lfm2.5-8b-a1b              larger
   [ show all gallery LLMs ]

TTS:
 > vits-piper-it_IT-paola-sherpa   (streaming)
   kokoros                          (streaming)
   qwen3-tts-cpp                    (streaming, neural; speakers/voice-design)
   kokoro-multi-lang-v1.0-sherpa    (streaming, multilingual)
   [ show all gallery TTS ]

Wrote docker-compose.yml      (image master-gpu-nvidia-cuda-12, GPU stanza,
                               command: [silero-vad-ggml, parakeet-…, gemma-…, kokoros])
Updated localai/models/gpt-realtime.yaml   (pipeline.llm, pipeline.tts)

Next:  docker compose up      # first boot installs the baked models
```

- **Detect, then confirm:** best-effort detection (`nvidia-smi`, `rocminfo`,
  `lspci`, `uname -m`/`-s`) preselects a default; the menu always shows so the user
  can override (e.g. scaffolding for another board). Detection never blocks.
- **Model lists with no running LocalAI:** a **hardcoded preferred set** (per
  stage, offline-friendly — ideal for embedded). "Show all" optionally fetches the
  gallery `index.yaml` **directly from its URL** and filters by `known_usecases`;
  if offline, "show all" is unavailable and the wizard says so. Streaming is
  annotated from the hardcoded `streamingBackends` constant
  (`sherpa-onnx, qwen3-tts-cpp, voxcpm, vibevoice-cpp, omnivoice-cpp`).

## Outputs

1. **`docker-compose.yml`** (repo root) — image tag for the chosen HW, GPU
   deploy/device stanza, and the `command:` install list:
   `silero-vad-ggml`, `parakeet-cpp-tdt-0.6b-v3`, `<chosen LLM>`, `<chosen TTS>`.
   The tracked compose is the CPU default; scaffolding overwrites it (a git diff,
   accepted) unless `--out <file>` is given. A `.bak` is written before overwrite.
2. **`localai/models/gpt-realtime.yaml`** — `pipeline.llm` / `pipeline.tts` set to
   the choices; the `streaming` block kept (degrades gracefully for non-streaming
   backends).

## Components (Go)

### `internal/scaffold` package
- `DetectHardware() Hardware` — best-effort; returns a guess + whether detected.
- `Variants() []Variant` and `ImageFor(hw) (image string, gpuStanza yaml)` — the
  mapping table + per-vendor stanza templates.
- `PreferredLLMs()`, `PreferredTTS()` — hardcoded curated tables with labels;
  `streaming` annotated via the constant.
- `FetchGalleryModels(ctx, usecase) ([]ModelOption, error)` — optional "show all",
  fetches gallery `index.yaml` directly; errors offline.
- `RenderCompose(choice) ([]byte, error)` and `PatchPipelineYAML(path, llm, tts)` —
  produce the two outputs. Compose rendered from a template (text/template), not by
  mutating LocalAI's example, so output is deterministic and testable.

### `cmd/assistant`
- A `setup` subcommand (dispatched before the normal connect path). Interactive by
  default; a thin TUI behind an interface so selection logic is testable without a
  terminal. `--out` to redirect the compose; `--yes` to accept detected defaults
  non-interactively (handy for CI/board images).

### README
- Document `./assistant setup` as the way to produce a hardware-appropriate stack;
  note first boot installs the baked models (needs network), and that the
  generated compose overwrites the CPU default (`.bak` kept).

## Error handling

- Detection failure → fall back to CPU preselected; never blocks.
- Unknown/unsupported HW choice → only offer the table's entries.
- "Show all" while offline → clear message; preferred set still works.
- Existing `docker-compose.yml` → back up to `docker-compose.yml.bak` before write;
  refuse if `--out` target exists without `--force`.

## Testing

- `internal/scaffold`: golden-file tests — for each HW variant, `RenderCompose`
  produces the expected image tag + GPU stanza + `command:` list; `PatchPipelineYAML`
  sets the right fields and preserves the streaming block; `DetectHardware` parses
  representative `nvidia-smi`/`uname` outputs (table-driven, fed as fixtures).
- `cmd/assistant`: `setup` dispatch + non-interactive `--yes` path produces files.
- Manual e2e: `./assistant setup` on a CPU host → `docker compose up` → connect and
  talk; repeat selecting `kokoros`.

## Open questions / future work

- prep-buddy *may* later want a similar compose helper (the user floated "a CLI
  helper to set up a docker-compose file properly"); if so it would reuse this
  scaffolder's HW→image mapping. Deferred — prep-buddy's current spec is the live
  web provisioner only.
- Auto-detection breadth (multi-GPU, specific CUDA minor versions) can grow later;
  the menu override covers gaps from day one.
- Could later emit a `docker-compose.distributed.yml` variant (LocalAI ships one);
  out of scope now.
