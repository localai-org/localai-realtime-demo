package scaffold

// imageRepo/imageTag pin LocalAI's :master channel — the same the hand-written
// docker-compose.yml uses (the reasoning_effort->backend forwarding lfm2.5 needs
// landed after the v4.3.6 'latest' tag). GPU variants append LocalAI's
// accelerator suffix to this tag; CPU/Metal use the bare tag.
const (
	imageRepo = "localai/localai"
	imageTag  = "master"
)

// Variant is the per-accelerator recipe for the localai service: the image to
// pull and the YAML granting GPU access. DeviceStanza is rendered verbatim
// under `services.localai:` (4-space indent), so it must be valid at that
// position; it is empty for CPU and Apple Metal.
type Variant struct {
	Accel        Accelerator
	Name         string // stable slug, e.g. "nvidia-cuda-12" (used in messages/tests)
	Label        string // human-readable, shown in the confirm menu
	Image        string // full image ref, e.g. localai/localai:master-gpu-nvidia-cuda-12
	DeviceStanza string // compose YAML granting GPU access; "" when none is needed
}

// nvidiaDeviceStanza reserves all NVIDIA GPUs via the Compose device-reservation
// spec (seeded from LocalAI's example compose). Requires the host's
// nvidia-container-toolkit / CDI.
const nvidiaDeviceStanza = `    deploy:
      resources:
        reservations:
          devices:
            - driver: nvidia.com/gpu
              count: all
              capabilities: [gpu, utility]`

// amdDeviceStanza exposes the AMD kernel-fusion and DRI render nodes ROCm needs,
// and adds the container user to the render+video groups for access.
const amdDeviceStanza = `    devices:
      - /dev/kfd
      - /dev/dri
    group_add:
      - render
      - video`

// driDeviceStanza exposes just the DRI render node — what Intel oneAPI and the
// Vulkan backend need.
const driDeviceStanza = `    devices:
      - /dev/dri`

// variants is the single source of truth mapping each Accelerator to its image
// and device wiring (LocalAI's published image matrix). Order is CPU-first so
// Variants() reads as a menu from the safe default upward.
var variants = []Variant{
	{Accel: CPU, Name: "cpu", Label: "CPU (portable, no GPU offload)", Image: imageRepo + ":" + imageTag},
	{Accel: NvidiaCUDA12, Name: "nvidia-cuda-12", Label: "NVIDIA GPU (CUDA 12)", Image: imageRepo + ":" + imageTag + "-gpu-nvidia-cuda-12", DeviceStanza: nvidiaDeviceStanza},
	{Accel: NvidiaCUDA13, Name: "nvidia-cuda-13", Label: "NVIDIA GPU (CUDA 13)", Image: imageRepo + ":" + imageTag + "-gpu-nvidia-cuda-13", DeviceStanza: nvidiaDeviceStanza},
	{Accel: AMDROCm, Name: "amd-rocm", Label: "AMD GPU (ROCm / hipBLAS)", Image: imageRepo + ":" + imageTag + "-gpu-hipblas", DeviceStanza: amdDeviceStanza},
	{Accel: IntelGPU, Name: "intel", Label: "Intel GPU (oneAPI)", Image: imageRepo + ":" + imageTag + "-gpu-intel", DeviceStanza: driDeviceStanza},
	{Accel: Vulkan, Name: "vulkan", Label: "Vulkan (vendor-neutral GPU)", Image: imageRepo + ":" + imageTag + "-gpu-vulkan", DeviceStanza: driDeviceStanza},
	{Accel: AppleMetal, Name: "metal", Label: "Apple Metal (arm64; Docker GPU passthrough limited)", Image: imageRepo + ":" + imageTag + "-metal-darwin-arm64"},
	{Accel: NvidiaJetson, Name: "jetson", Label: "NVIDIA Jetson / L4T (arm64)", Image: imageRepo + ":" + imageTag + "-nvidia-l4t-arm64", DeviceStanza: nvidiaDeviceStanza},
}

// Variants returns the supported accelerator recipes, CPU first. The slice is a
// copy so callers can't mutate the table.
func Variants() []Variant {
	out := make([]Variant, len(variants))
	copy(out, variants)
	return out
}

// ImageFor returns the Variant for the detected hardware. An unknown
// Accelerator falls back to the CPU variant, which always runs.
func ImageFor(hw Hardware) Variant {
	for _, v := range variants {
		if v.Accel == hw.Accel {
			return v
		}
	}
	return variants[0] // CPU
}
