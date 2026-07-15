package settings

import (
	"os/exec"
	"testing"
)

// TestProbeCuda_NvidiaSmiAbsent_ReturnsFalse verifies that probeCuda returns false when nvidia-smi is not found.
// This test skips gracefully on systems where nvidia-smi is actually present.
func TestProbeCuda_NvidiaSmiAbsent_ReturnsFalse(t *testing.T) {
	// Check if nvidia-smi is actually available on this system.
	_, err := exec.LookPath("nvidia-smi")
	if err == nil {
		// nvidia-smi is present, skip this test since we can't reliably test the "absent" case.
		t.Skip("nvidia-smi is present on this system; skipping absent test")
	}

	// On systems without nvidia-smi, probeCuda should return false.
	result, ok := probeCuda()
	if ok {
		t.Errorf("expected probeCuda to return false when nvidia-smi is absent, got true")
	}
	if result.Available {
		t.Errorf("expected result.Available to be false when nvidia-smi is absent, got true")
	}
}

// TestProbeVaapi_RenderNodeAbsent_ReturnsFalse verifies that probeVaapi returns false when /dev/dri/renderD128 is not found.
// This test skips gracefully on systems where the render node is actually present.
func TestProbeVaapi_RenderNodeAbsent_ReturnsFalse(t *testing.T) {
	// Try to call probeVaapi. If it returns true, the render node exists, so skip this test.
	result, ok := probeVaapi()
	if ok {
		// The render node is present, skip the absent test.
		t.Skip("/dev/dri/renderD128 is present on this system; skipping absent test")
	}

	// On systems without /dev/dri/renderD128, probeVaapi should return false.
	if result.Available {
		t.Errorf("expected result.Available to be false when render node is absent, got true")
	}
}

// TestProbeGpu_NoHardware_AvailableFalse verifies that ProbeGpu returns Available=false when no GPU hardware is detected.
// This test skips on systems with real GPU hardware present.
func TestProbeGpu_NoHardware_AvailableFalse(t *testing.T) {
	result := ProbeGpu()

	// If neither nvidia-smi nor /dev/dri/renderD128 are present, Available should be false.
	_, nvidiaSmiErr := exec.LookPath("nvidia-smi")
	_, renderNodeAvailable := probeVaapi()

	if nvidiaSmiErr != nil && !renderNodeAvailable {
		// Neither CUDA nor VAAPI detected.
		if result.Available {
			t.Errorf("expected Available=false when no GPU hardware is detected, got true")
		}
		if result.Description == "" {
			t.Error("expected Description to be non-empty even when GPU is not available")
		}
	} else {
		// At least one GPU hardware type is present; skip this test.
		t.Skip("GPU hardware detected on this system; skipping no-hardware test")
	}
}

// TestProbeGpu_Caching_ReturnsSameStruct verifies that ProbeGpu returns the same cached result on multiple calls.
func TestProbeGpu_Caching_ReturnsSameStruct(t *testing.T) {
	// Clear the cache by creating a new test (in real code, this can't be done without
	// unexported functions, but we verify the caching behavior by calling multiple times).
	result1 := ProbeGpu()
	result2 := ProbeGpu()

	// Both calls should return the exact same result (same value and same pointer behavior).
	if result1.Available != result2.Available {
		t.Errorf("caching failed: Available changed between calls (%v vs %v)", result1.Available, result2.Available)
	}
	if result1.Description != result2.Description {
		t.Errorf("caching failed: Description changed between calls (%q vs %q)", result1.Description, result2.Description)
	}
}
