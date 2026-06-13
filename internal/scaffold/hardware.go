// Package scaffold generates a hardware-appropriate docker-compose stack for
// the LocalAI realtime backend, before any LocalAI is running. It detects the
// host accelerator (best-effort, detect-then-confirm), picks the matching
// LocalAI image variant and GPU wiring, lets the user pick an LLM and a TTS
// voice (curated tables, or the full gallery), then renders docker-compose.yml
// and patches localai/models/gpt-realtime.yaml — no running LocalAI required.
package scaffold

import (
	"context"
	"os/exec"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// Accelerator is the target compute LocalAI should run on. The zero value is
// CPU so a detection that finds nothing still yields a runnable stack.
type Accelerator int

const (
	CPU          Accelerator = iota // no GPU offload; the portable default
	NvidiaCUDA12                    // NVIDIA GPU, CUDA 12
	NvidiaCUDA13                    // NVIDIA GPU, CUDA 13
	AMDROCm                         // AMD GPU via ROCm/hipBLAS
	IntelGPU                        // Intel Arc/iGPU via oneAPI
	Vulkan                          // any GPU via the Vulkan backend
	AppleMetal                      // Apple Silicon (Docker Desktop; GPU passthrough limited)
	NvidiaJetson                    // NVIDIA Jetson / L4T (arm64)
)

// Hardware is the result of best-effort host probing. Accel drives the image
// and GPU wiring; Detected reports whether a real probe matched (vs the CPU
// fallback); Detail is a short, human-readable line for the confirm prompt.
type Hardware struct {
	Accel    Accelerator
	Detected bool
	Detail   string
}

// detectTimeout bounds every external probe. DetectHardware must never block a
// CLI on a wedged driver tool, so each command is killed well before a human
// would notice the wait.
const detectTimeout = 2 * time.Second

// cudaVersionRe pulls the "CUDA Version: 12.4" field out of nvidia-smi's header.
var cudaVersionRe = regexp.MustCompile(`CUDA Version:\s*(\d+)`)

// DetectHardware probes the host for a supported accelerator. It is
// best-effort and non-fatal: every external tool runs under a short timeout and
// any failure (missing binary, non-zero exit, timeout) falls through to the
// next strategy, ending at CPU. It never errors — a wrong guess is corrected at
// the confirm menu, which always shows.
//
// Strategy, most specific first:
//  1. macOS arm64 (uname)  — Apple Metal, before anything else.
//  2. nvidia-smi           — authoritative for NVIDIA; carries the CUDA version
//     and, on arm64, means Jetson/L4T.
//  3. rocminfo             — authoritative for AMD ROCm.
//  4. lspci                — catches GPUs whose vendor tool isn't installed.
//  5. CPU                  — the always-valid fallback.
func DetectHardware() Hardware {
	if runtime.GOOS == "darwin" && runtime.GOARCH == "arm64" {
		return Hardware{Accel: AppleMetal, Detected: true, Detail: "Apple Silicon (macOS arm64) — Metal"}
	}
	if out, ok := runProbe("nvidia-smi"); ok {
		return parseNvidiaSMI(out, runtime.GOARCH)
	}
	if out, ok := runProbe("rocminfo"); ok && strings.Contains(strings.ToLower(out), "gfx") {
		return Hardware{Accel: AMDROCm, Detected: true, Detail: "AMD GPU detected via rocminfo (ROCm/hipBLAS)"}
	}
	if hw, ok := detectViaLspci(); ok {
		return hw
	}
	return Hardware{Accel: CPU, Detail: "no GPU detected; using CPU (" + runtime.GOOS + "/" + runtime.GOARCH + ")"}
}

// parseNvidiaSMI maps an nvidia-smi header to an NVIDIA accelerator. The CUDA
// major version selects the cuda-13 vs cuda-12 image (anything unparseable
// defaults to cuda-12, the current line); an arm64 host means Jetson/L4T.
func parseNvidiaSMI(out, arch string) Hardware {
	if arch == "arm64" {
		return Hardware{Accel: NvidiaJetson, Detected: true, Detail: "NVIDIA Jetson/L4T detected via nvidia-smi (arm64)"}
	}
	accel := NvidiaCUDA12
	detail := "NVIDIA GPU detected via nvidia-smi"
	if m := cudaVersionRe.FindStringSubmatch(out); m != nil {
		if major, err := strconv.Atoi(m[1]); err == nil {
			detail += " (CUDA " + m[1] + ")"
			if major >= 13 {
				accel = NvidiaCUDA13
			}
		}
	}
	return Hardware{Accel: accel, Detected: true, Detail: detail}
}

// detectViaLspci runs lspci and matches the GPU controller lines on vendor name.
func detectViaLspci() (Hardware, bool) {
	out, ok := runProbe("lspci")
	if !ok {
		return Hardware{}, false
	}
	return parseLspci(out)
}

// parseLspci scans lspci output for a VGA/3D/Display controller line and maps
// its vendor to an Accelerator. Split out from detectViaLspci so it's testable
// against captured fixtures.
func parseLspci(out string) (Hardware, bool) {
	for _, line := range strings.Split(out, "\n") {
		low := strings.ToLower(line)
		if !strings.Contains(low, "vga") && !strings.Contains(low, "3d controller") && !strings.Contains(low, "display controller") {
			continue
		}
		switch {
		case strings.Contains(low, "nvidia"):
			return Hardware{Accel: NvidiaCUDA12, Detected: true, Detail: "NVIDIA GPU detected via lspci"}, true
		// Match AMD vendor names, but not a bare "ati" — it's a substring of
		// "Corporation" and would false-positive on every other vendor's line.
		case strings.Contains(low, "amd") || strings.Contains(low, "advanced micro devices") || strings.Contains(low, "radeon") || strings.Contains(low, "[ati"):
			return Hardware{Accel: AMDROCm, Detected: true, Detail: "AMD GPU detected via lspci"}, true
		case strings.Contains(low, "intel"):
			return Hardware{Accel: IntelGPU, Detected: true, Detail: "Intel GPU detected via lspci"}, true
		}
	}
	return Hardware{}, false
}

// runProbe runs name with the given args under detectTimeout and returns its
// combined output. ok is false when the binary is missing, exits non-zero, or
// times out — callers treat all three identically (fall through).
func runProbe(name string, args ...string) (out string, ok bool) {
	if _, err := exec.LookPath(name); err != nil {
		return "", false
	}
	ctx, cancel := context.WithTimeout(context.Background(), detectTimeout)
	defer cancel()
	b, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
	if err != nil {
		return "", false
	}
	return string(b), true
}
