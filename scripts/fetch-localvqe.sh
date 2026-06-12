#!/usr/bin/env bash
# Build liblocalvqe.so and download the LocalVQE GGUF model for AEC.
# Usage: scripts/fetch-localvqe.sh [dest-dir]
set -euo pipefail

DEST="${1:-$(pwd)/.localvqe}"
MODEL_VER="localvqe-v1.3-4.8M-f32.gguf"
HF_REPO="LocalAI-io/LocalVQE"
SRC_REPO="https://github.com/localai-org/LocalVQE.git"

mkdir -p "$DEST"
cd "$DEST"

# 1) Source + build the shared library.
if [ ! -d LocalVQE ]; then
  git clone --depth 1 "$SRC_REPO" LocalVQE
fi
cmake -S LocalVQE/ggml -B LocalVQE/ggml/build -DLOCALVQE_BUILD_SHARED=ON
cmake --build LocalVQE/ggml/build -j"$(nproc)"

LIB="$(find "$DEST/LocalVQE/ggml/build" -name 'liblocalvqe.*' | head -n1)"
cp "$LIB" "$DEST/"

# 2) Download the model.
if [ ! -f "$DEST/$MODEL_VER" ]; then
  curl -fSL "https://huggingface.co/$HF_REPO/resolve/main/$MODEL_VER" -o "$DEST/$MODEL_VER"
fi

echo
echo "LocalVQE ready. Run the assistant with:"
echo "  LOCALVQE_LIB=$DEST/$(basename "$LIB") \\"
echo "  LOCALVQE_MODEL=$DEST/$MODEL_VER \\"
echo "  go run ./cmd/assistant"
