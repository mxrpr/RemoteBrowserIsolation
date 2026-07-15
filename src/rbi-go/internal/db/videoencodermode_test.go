package db

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// === String() Tests ===

// TestVideoEncoderMode_String_Auto verifies that VideoEncoderModeAuto.String() returns "Auto".
func TestVideoEncoderMode_String_Auto(t *testing.T) {
	m := VideoEncoderModeAuto
	expected := "Auto"
	if got := m.String(); got != expected {
		t.Errorf("expected %q, got %q", expected, got)
	}
}

// TestVideoEncoderMode_String_Cpu verifies that VideoEncoderModeCpu.String() returns "Cpu".
func TestVideoEncoderMode_String_Cpu(t *testing.T) {
	m := VideoEncoderModeCpu
	expected := "Cpu"
	if got := m.String(); got != expected {
		t.Errorf("expected %q, got %q", expected, got)
	}
}

// TestVideoEncoderMode_String_Gpu verifies that VideoEncoderModeGpu.String() returns "Gpu".
func TestVideoEncoderMode_String_Gpu(t *testing.T) {
	m := VideoEncoderModeGpu
	expected := "Gpu"
	if got := m.String(); got != expected {
		t.Errorf("expected %q, got %q", expected, got)
	}
}

// TestVideoEncoderMode_String_Unknown_ContainsInt verifies that an unknown VideoEncoderMode value
// returns a string containing its integer representation (e.g. "VideoEncoderMode(99)").
func TestVideoEncoderMode_String_Unknown_ContainsInt(t *testing.T) {
	m := VideoEncoderMode(99)
	s := m.String()
	if !strings.Contains(s, "99") {
		t.Errorf("expected string to contain '99', got %q", s)
	}
	if !strings.HasPrefix(s, "VideoEncoderMode(") {
		t.Errorf("expected string to start with 'VideoEncoderMode(', got %q", s)
	}
}

// === MarshalJSON() Tests ===

// TestVideoEncoderMode_MarshalJSON_Auto verifies that VideoEncoderModeAuto marshals to "Auto".
func TestVideoEncoderMode_MarshalJSON_Auto(t *testing.T) {
	m := VideoEncoderModeAuto
	data, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}
	expected := `"Auto"`
	if string(data) != expected {
		t.Errorf("expected %s, got %s", expected, string(data))
	}
}

// TestVideoEncoderMode_MarshalJSON_Cpu verifies that VideoEncoderModeCpu marshals to "Cpu".
func TestVideoEncoderMode_MarshalJSON_Cpu(t *testing.T) {
	m := VideoEncoderModeCpu
	data, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}
	expected := `"Cpu"`
	if string(data) != expected {
		t.Errorf("expected %s, got %s", expected, string(data))
	}
}

// TestVideoEncoderMode_MarshalJSON_Gpu verifies that VideoEncoderModeGpu marshals to "Gpu".
func TestVideoEncoderMode_MarshalJSON_Gpu(t *testing.T) {
	m := VideoEncoderModeGpu
	data, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}
	expected := `"Gpu"`
	if string(data) != expected {
		t.Errorf("expected %s, got %s", expected, string(data))
	}
}

// TestVideoEncoderMode_MarshalJSON_InStruct verifies that VideoEncoderMode marshals correctly as a field in a struct.
func TestVideoEncoderMode_MarshalJSON_InStruct(t *testing.T) {
	type testStruct struct {
		Mode VideoEncoderMode `json:"mode"`
		Name string           `json:"name"`
	}
	obj := testStruct{
		Mode: VideoEncoderModeCpu,
		Name: "test",
	}
	data, err := json.Marshal(obj)
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}
	// Verify the JSON contains the mode as a string, not an integer.
	if !strings.Contains(string(data), `"mode":"Cpu"`) {
		t.Errorf("expected mode to be Cpu in JSON, got %s", string(data))
	}
}

// === UnmarshalJSON() Tests ===

// TestVideoEncoderMode_UnmarshalJSON_Auto verifies that unmarshaling "Auto" produces VideoEncoderModeAuto.
func TestVideoEncoderMode_UnmarshalJSON_Auto(t *testing.T) {
	var m VideoEncoderMode
	if err := json.Unmarshal([]byte(`"Auto"`), &m); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if m != VideoEncoderModeAuto {
		t.Errorf("expected VideoEncoderModeAuto (%d), got %d", VideoEncoderModeAuto, m)
	}
}

// TestVideoEncoderMode_UnmarshalJSON_Cpu verifies that unmarshaling "Cpu" produces VideoEncoderModeCpu.
func TestVideoEncoderMode_UnmarshalJSON_Cpu(t *testing.T) {
	var m VideoEncoderMode
	if err := json.Unmarshal([]byte(`"Cpu"`), &m); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if m != VideoEncoderModeCpu {
		t.Errorf("expected VideoEncoderModeCpu (%d), got %d", VideoEncoderModeCpu, m)
	}
}

// TestVideoEncoderMode_UnmarshalJSON_Gpu verifies that unmarshaling "Gpu" produces VideoEncoderModeGpu.
func TestVideoEncoderMode_UnmarshalJSON_Gpu(t *testing.T) {
	var m VideoEncoderMode
	if err := json.Unmarshal([]byte(`"Gpu"`), &m); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if m != VideoEncoderModeGpu {
		t.Errorf("expected VideoEncoderModeGpu (%d), got %d", VideoEncoderModeGpu, m)
	}
}

// TestVideoEncoderMode_UnmarshalJSON_UnknownString_ReturnsError verifies that unmarshaling
// an unknown string value returns an error.
func TestVideoEncoderMode_UnmarshalJSON_UnknownString_ReturnsError(t *testing.T) {
	var m VideoEncoderMode
	err := json.Unmarshal([]byte(`"UnknownMode"`), &m)
	if err == nil {
		t.Error("expected error for unknown string value, got nil")
	}
}

// TestVideoEncoderMode_UnmarshalJSON_EmptyString_ReturnsError verifies that unmarshaling
// an empty string returns an error.
func TestVideoEncoderMode_UnmarshalJSON_EmptyString_ReturnsError(t *testing.T) {
	var m VideoEncoderMode
	err := json.Unmarshal([]byte(`""`), &m)
	if err == nil {
		t.Error("expected error for empty string, got nil")
	}
}

// TestVideoEncoderMode_UnmarshalJSON_Integer_ReturnsError verifies that unmarshaling
// an integer value (not a string) returns an error.
func TestVideoEncoderMode_UnmarshalJSON_Integer_ReturnsError(t *testing.T) {
	var m VideoEncoderMode
	err := json.Unmarshal([]byte(`0`), &m)
	if err == nil {
		t.Error("expected error for integer value, got nil")
	}
}

// === RoundTrip Tests ===

// TestVideoEncoderMode_RoundTrip_AllModes verifies that all VideoEncoderMode values can be marshaled
// and then unmarshaled to get back the original value.
func TestVideoEncoderMode_RoundTrip_AllModes(t *testing.T) {
	modes := []VideoEncoderMode{
		VideoEncoderModeAuto,
		VideoEncoderModeCpu,
		VideoEncoderModeGpu,
	}
	for _, original := range modes {
		t.Run(fmt.Sprintf("Mode_%d", original), func(t *testing.T) {
			// Marshal
			data, err := json.Marshal(original)
			if err != nil {
				t.Fatalf("marshal error: %v", err)
			}

			// Unmarshal
			var recovered VideoEncoderMode
			if err := json.Unmarshal(data, &recovered); err != nil {
				t.Fatalf("unmarshal error: %v", err)
			}

			// Verify
			if recovered != original {
				t.Errorf("round-trip failed: original %d, recovered %d", original, recovered)
			}
		})
	}
}
