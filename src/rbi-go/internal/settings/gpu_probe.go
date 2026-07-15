// Package settings provides stores for whole-server admin-configurable settings
// (video encoder mode and log level), backed by the single-row DB tables and kept
// in a memory cache for low-latency reads.
package settings

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"sync"
)

// GpuProbeResult holds the outcome of probing for a usable GPU hardware encoder,
// matching the C# GpuProbeResult record shape returned in the video-encoder API response.
type GpuProbeResult struct {
	// Available is true when at least one hardware acceleration device was detected.
	Available bool
	// Description is a human-readable summary of what was found or tried, suitable
	// for display in the admin UI.
	Description string
}

// gpuProbeOnce ensures the GPU probe runs at most once per process lifetime,
// mirroring the C# Lazy<GpuProbeResult> pattern in GpuEncoderProbe.
var gpuProbeOnce sync.Once

// gpuProbeCache stores the single probe result computed by gpuProbeOnce.
var gpuProbeCache GpuProbeResult

// ProbeGpu returns the GPU hardware acceleration availability for this host. The
// result is computed on the first call and cached for the process lifetime — hardware
// availability does not change at runtime.
//
// Deviation from C#: the C# implementation calls ffmpeg.av_hwdevice_ctx_create via
// FFmpeg.AutoGen (CUDA first, then VAAPI), which requires cgo FFmpeg bindings that are
// not yet wired in the Go backend (planned for Part 11). This Go version uses equivalent
// shell probes that detect the same hardware without cgo:
//   - CUDA: runs nvidia-smi (standard tool, present whenever the NVIDIA driver is installed).
//   - VAAPI: checks for /dev/dri/renderD128, the canonical Linux DRM render node for Intel/AMD VAAPI.
func ProbeGpu() GpuProbeResult {
	gpuProbeOnce.Do(func() {
		gpuProbeCache = probe()
	})
	return gpuProbeCache
}

// probe performs the actual hardware detection. Called once via gpuProbeOnce.
func probe() GpuProbeResult {
	// Try CUDA first — if nvidia-smi exits 0 then an NVIDIA GPU and driver are present.
	if cudaResult, ok := probeCuda(); ok {
		return cudaResult
	}

	// Try VAAPI — /dev/dri/renderD128 is the standard DRM render node for VAAPI on Linux.
	if vaapiResult, ok := probeVaapi(); ok {
		return vaapiResult
	}

	return GpuProbeResult{
		Available:   false,
		Description: "No hardware encoder device detected (tried CUDA, VAAPI)",
	}
}

// probeCuda checks for an NVIDIA GPU by running nvidia-smi. Returns (result, true)
// if nvidia-smi succeeds, or (zero, false) if it is not installed or fails.
func probeCuda() (GpuProbeResult, bool) {
	path, err := exec.LookPath("nvidia-smi")
	if err != nil {
		return GpuProbeResult{}, false
	}

	cmd := exec.Command(path, "--query-gpu=name", "--format=csv,noheader")
	out, err := cmd.Output()
	if err != nil {
		return GpuProbeResult{}, false
	}

	// nvidia-smi succeeded — at least one NVIDIA GPU is present.
	trimmed := bytes.TrimRight(out, "\r\n")
	name := "NVIDIA GPU"
	if len(trimmed) > 0 {
		// Use the first line as the GPU name.
		if i := bytes.IndexAny(trimmed, "\r\n"); i >= 0 {
			name = string(trimmed[:i])
		} else {
			name = string(trimmed)
		}
	}
	return GpuProbeResult{
		Available:   true,
		Description: fmt.Sprintf("AV_HWDEVICE_TYPE_CUDA available (%s)", name),
	}, true
}

// probeVaapi checks for a VAAPI-capable GPU by looking for the standard Linux DRM
// render node /dev/dri/renderD128. Returns (result, true) if the node exists, or
// (zero, false) if it does not.
func probeVaapi() (GpuProbeResult, bool) {
	node := "/dev/dri/renderD128"
	if _, err := os.Stat(node); err != nil {
		return GpuProbeResult{}, false
	}
	return GpuProbeResult{
		Available:   true,
		Description: fmt.Sprintf("AV_HWDEVICE_TYPE_VAAPI available (%s)", node),
	}, true
}
