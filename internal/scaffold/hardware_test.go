package scaffold

import "testing"

const nvidiaSMI12 = `| NVIDIA-SMI 550.54.14    Driver Version: 550.54.14    CUDA Version: 12.4     |`
const nvidiaSMI13 = `| NVIDIA-SMI 580.00.00    Driver Version: 580.00.00    CUDA Version: 13.0     |`
const nvidiaSMINoCUDA = `| NVIDIA-SMI 999.0   Driver Version: 999.0   |`

func TestParseNvidiaSMI(t *testing.T) {
	cases := []struct {
		name string
		out  string
		arch string
		want Accelerator
	}{
		{"cuda12 amd64", nvidiaSMI12, "amd64", NvidiaCUDA12},
		{"cuda13 amd64", nvidiaSMI13, "amd64", NvidiaCUDA13},
		{"unparsed cuda defaults to 12", nvidiaSMINoCUDA, "amd64", NvidiaCUDA12},
		{"arm64 means jetson regardless of cuda", nvidiaSMI13, "arm64", NvidiaJetson},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := parseNvidiaSMI(tc.out, tc.arch).Accel; got != tc.want {
				t.Errorf("Accel = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestParseLspci(t *testing.T) {
	cases := []struct {
		name   string
		out    string
		want   Accelerator
		wantOk bool
	}{
		{"nvidia", "00:02.0 VGA compatible controller: NVIDIA Corporation GA104 [GeForce RTX 3070]", NvidiaCUDA12, true},
		{"amd", "03:00.0 VGA compatible controller: Advanced Micro Devices, Inc. [AMD/ATI] Navi 31", AMDROCm, true},
		{"intel", "00:02.0 Display controller: Intel Corporation DG2 [Arc A770]", IntelGPU, true},
		{"amd 3d controller", "01:00.0 3D controller: Advanced Micro Devices, Inc. [AMD/ATI] Instinct MI200", AMDROCm, true},
		{"no gpu", "00:1f.3 Audio device: Intel Corporation Comet Lake PCH cAVS", 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			hw, ok := parseLspci(tc.out)
			if ok != tc.wantOk {
				t.Fatalf("ok = %v, want %v", ok, tc.wantOk)
			}
			if ok && hw.Accel != tc.want {
				t.Errorf("Accel = %d, want %d", hw.Accel, tc.want)
			}
		})
	}
}

// DetectHardware must never error or panic, whatever the host looks like, and
// always populate Detail.
func TestDetectHardwareNeverBlocksOrPanics(t *testing.T) {
	if DetectHardware().Detail == "" {
		t.Error("Detail should always be populated")
	}
}
