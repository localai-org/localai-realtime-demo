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

# 1) Source + build the shared library for the host arch. LocalVQE vendors ggml
# as a git submodule, so the checkout must pull submodules or cmake fails on the
# empty ggml/vendor/ggml directory.
if [ ! -d "$CACHE_DIR/LocalVQE/.git" ]; then
  rm -rf "$CACHE_DIR/LocalVQE"
  git clone --depth 1 --recurse-submodules --shallow-submodules "$SRC_REPO" "$CACHE_DIR/LocalVQE"
else
  git -C "$CACHE_DIR/LocalVQE" submodule update --init --recursive --depth 1
fi

# By default the CPU build links ggml + CPU backends as SEPARATE shared libs
# (GGML_BACKEND_DL: libggml.so, libggml-base.so, libggml-cpu-*.so) that
# liblocalvqe.so loads from its own directory at runtime. That doesn't survive
# being extracted from the binary on its own. Flip the CPU branch to a
# self-contained static link so liblocalvqe.so embeds ggml + the CPU backend and
# is the ONLY file we need to bundle. (PIC is already forced for the shared lib,
# so the static ggml links in cleanly - that's what that PIC line is there for.)
CML="$CACHE_DIR/LocalVQE/ggml/CMakeLists.txt"
sed -i \
  -e 's/set(BUILD_SHARED_LIBS ON CACHE BOOL "" FORCE)/set(BUILD_SHARED_LIBS OFF CACHE BOOL "" FORCE)/' \
  -e 's/set(GGML_BACKEND_DL ON CACHE BOOL "" FORCE)/set(GGML_BACKEND_DL OFF CACHE BOOL "" FORCE)/' \
  -e 's/set(GGML_CPU_ALL_VARIANTS ON CACHE BOOL "" FORCE)/set(GGML_CPU_ALL_VARIANTS OFF CACHE BOOL "" FORCE)/' \
  "$CML"

# Clean reconfigure so the flipped cache vars take effect.
rm -rf "$CACHE_DIR/LocalVQE/ggml/build"
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
