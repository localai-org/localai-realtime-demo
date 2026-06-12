#!/usr/bin/env bash
# Build-time asset producer for bundled AEC.
#
# Builds liblocalvqe.so for the host arch and downloads the LocalVQE GGUF model,
# placing BOTH into internal/localvqe/assets/ with the fixed names the go:embed
# layer expects (liblocalvqe.so / model.gguf). Idempotent: skips work already
# done. After this runs, `make build` bundles the assets into the binary.
set -euo pipefail

# LocalVQE model to bundle. Override LOCALVQE_MODEL_FILE to swap.
#   compact (default): localvqe-v1.2-1.3M-f32.gguf   (~5 MB)
#   larger/quality:    localvqe-v1.3-4.8M-f32.gguf   (~19 MB)
LOCALVQE_MODEL_FILE="${LOCALVQE_MODEL_FILE:-localvqe-v1.2-1.3M-f32.gguf}"
HF_REPO="LocalAI-io/LocalVQE"
SRC_REPO="https://github.com/localai-org/LocalVQE.git"

# Resolve repo root from this script's location so it works from any cwd.
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

ASSETS_DIR="$REPO_ROOT/internal/localvqe/assets"
CACHE_DIR="$REPO_ROOT/.localvqe"

mkdir -p "$ASSETS_DIR" "$CACHE_DIR"

# 1) Source + build the shared library for the host arch.
if [ ! -d "$CACHE_DIR/LocalVQE" ]; then
  git clone --depth 1 "$SRC_REPO" "$CACHE_DIR/LocalVQE"
fi
cmake -S "$CACHE_DIR/LocalVQE/ggml" -B "$CACHE_DIR/LocalVQE/ggml/build" -DLOCALVQE_BUILD_SHARED=ON
cmake --build "$CACHE_DIR/LocalVQE/ggml/build" -j"$(nproc)"

LIB="$(find "$CACHE_DIR/LocalVQE/ggml/build" -name 'liblocalvqe.*' | head -n1)"
if [ -z "$LIB" ]; then
  echo "error: built liblocalvqe shared library not found" >&2
  exit 1
fi
cp "$LIB" "$ASSETS_DIR/liblocalvqe.so"

# 2) Download the model to the fixed embed name (skip if already present).
if [ ! -f "$ASSETS_DIR/model.gguf" ]; then
  curl -fSL "https://huggingface.co/$HF_REPO/resolve/main/$LOCALVQE_MODEL_FILE" -o "$ASSETS_DIR/model.gguf"
fi

echo
echo "LocalVQE assets ready in $ASSETS_DIR:"
echo "  liblocalvqe.so  (host build)"
echo "  model.gguf      ($LOCALVQE_MODEL_FILE)"
echo "Run \`make build\` to bundle them into the binary."
