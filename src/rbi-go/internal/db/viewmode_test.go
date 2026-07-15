package db

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// === String() Tests ===

// TestViewMode_String_HtmlAllowInput verifies that ViewModeHtmlAllowInput.String() returns "HtmlAllowInput".
func TestViewMode_String_HtmlAllowInput(t *testing.T) {
	v := ViewModeHtmlAllowInput
	expected := "HtmlAllowInput"
	if got := v.String(); got != expected {
		t.Errorf("expected %q, got %q", expected, got)
	}
}

// TestViewMode_String_HtmlNoInput verifies that ViewModeHtmlNoInput.String() returns "HtmlNoInput".
func TestViewMode_String_HtmlNoInput(t *testing.T) {
	v := ViewModeHtmlNoInput
	expected := "HtmlNoInput"
	if got := v.String(); got != expected {
		t.Errorf("expected %q, got %q", expected, got)
	}
}

// TestViewMode_String_VideoAllowInput verifies that ViewModeVideoAllowInput.String() returns "VideoAllowInput".
func TestViewMode_String_VideoAllowInput(t *testing.T) {
	v := ViewModeVideoAllowInput
	expected := "VideoAllowInput"
	if got := v.String(); got != expected {
		t.Errorf("expected %q, got %q", expected, got)
	}
}

// TestViewMode_String_VideoNoInput verifies that ViewModeVideoNoInput.String() returns "VideoNoInput".
func TestViewMode_String_VideoNoInput(t *testing.T) {
	v := ViewModeVideoNoInput
	expected := "VideoNoInput"
	if got := v.String(); got != expected {
		t.Errorf("expected %q, got %q", expected, got)
	}
}

// TestViewMode_String_Unknown_ContainsInt verifies that an unknown ViewMode value
// returns a string containing its integer representation (e.g. "ViewMode(99)").
func TestViewMode_String_Unknown_ContainsInt(t *testing.T) {
	v := ViewMode(99)
	s := v.String()
	if !strings.Contains(s, "99") {
		t.Errorf("expected string to contain '99', got %q", s)
	}
	if !strings.HasPrefix(s, "ViewMode(") {
		t.Errorf("expected string to start with 'ViewMode(', got %q", s)
	}
}

// === MarshalJSON() Tests ===

// TestViewMode_MarshalJSON_HtmlAllowInput verifies that ViewModeHtmlAllowInput marshals to "HtmlAllowInput".
func TestViewMode_MarshalJSON_HtmlAllowInput(t *testing.T) {
	v := ViewModeHtmlAllowInput
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}
	expected := `"HtmlAllowInput"`
	if string(data) != expected {
		t.Errorf("expected %s, got %s", expected, string(data))
	}
}

// TestViewMode_MarshalJSON_HtmlNoInput verifies that ViewModeHtmlNoInput marshals to "HtmlNoInput".
func TestViewMode_MarshalJSON_HtmlNoInput(t *testing.T) {
	v := ViewModeHtmlNoInput
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}
	expected := `"HtmlNoInput"`
	if string(data) != expected {
		t.Errorf("expected %s, got %s", expected, string(data))
	}
}

// TestViewMode_MarshalJSON_VideoAllowInput verifies that ViewModeVideoAllowInput marshals to "VideoAllowInput".
func TestViewMode_MarshalJSON_VideoAllowInput(t *testing.T) {
	v := ViewModeVideoAllowInput
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}
	expected := `"VideoAllowInput"`
	if string(data) != expected {
		t.Errorf("expected %s, got %s", expected, string(data))
	}
}

// TestViewMode_MarshalJSON_VideoNoInput verifies that ViewModeVideoNoInput marshals to "VideoNoInput".
func TestViewMode_MarshalJSON_VideoNoInput(t *testing.T) {
	v := ViewModeVideoNoInput
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}
	expected := `"VideoNoInput"`
	if string(data) != expected {
		t.Errorf("expected %s, got %s", expected, string(data))
	}
}

// TestViewMode_MarshalJSON_InStruct verifies that ViewMode marshals correctly as a field in a struct.
func TestViewMode_MarshalJSON_InStruct(t *testing.T) {
	type testStruct struct {
		Mode ViewMode `json:"mode"`
		Name string   `json:"name"`
	}
	obj := testStruct{
		Mode: ViewModeHtmlAllowInput,
		Name: "test",
	}
	data, err := json.Marshal(obj)
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}
	// Verify the JSON contains the mode as a string, not an integer.
	if !strings.Contains(string(data), `"mode":"HtmlAllowInput"`) {
		t.Errorf("expected mode to be HtmlAllowInput in JSON, got %s", string(data))
	}
}

// === UnmarshalJSON() Tests ===

// TestViewMode_UnmarshalJSON_HtmlAllowInput verifies that unmarshaling "HtmlAllowInput" produces ViewModeHtmlAllowInput.
func TestViewMode_UnmarshalJSON_HtmlAllowInput(t *testing.T) {
	var v ViewMode
	if err := json.Unmarshal([]byte(`"HtmlAllowInput"`), &v); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if v != ViewModeHtmlAllowInput {
		t.Errorf("expected ViewModeHtmlAllowInput (%d), got %d", ViewModeHtmlAllowInput, v)
	}
}

// TestViewMode_UnmarshalJSON_HtmlNoInput verifies that unmarshaling "HtmlNoInput" produces ViewModeHtmlNoInput.
func TestViewMode_UnmarshalJSON_HtmlNoInput(t *testing.T) {
	var v ViewMode
	if err := json.Unmarshal([]byte(`"HtmlNoInput"`), &v); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if v != ViewModeHtmlNoInput {
		t.Errorf("expected ViewModeHtmlNoInput (%d), got %d", ViewModeHtmlNoInput, v)
	}
}

// TestViewMode_UnmarshalJSON_VideoAllowInput verifies that unmarshaling "VideoAllowInput" produces ViewModeVideoAllowInput.
func TestViewMode_UnmarshalJSON_VideoAllowInput(t *testing.T) {
	var v ViewMode
	if err := json.Unmarshal([]byte(`"VideoAllowInput"`), &v); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if v != ViewModeVideoAllowInput {
		t.Errorf("expected ViewModeVideoAllowInput (%d), got %d", ViewModeVideoAllowInput, v)
	}
}

// TestViewMode_UnmarshalJSON_VideoNoInput verifies that unmarshaling "VideoNoInput" produces ViewModeVideoNoInput.
func TestViewMode_UnmarshalJSON_VideoNoInput(t *testing.T) {
	var v ViewMode
	if err := json.Unmarshal([]byte(`"VideoNoInput"`), &v); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if v != ViewModeVideoNoInput {
		t.Errorf("expected ViewModeVideoNoInput (%d), got %d", ViewModeVideoNoInput, v)
	}
}

// TestViewMode_UnmarshalJSON_UnknownString_ReturnsError verifies that unmarshaling
// an unknown string value returns an error.
func TestViewMode_UnmarshalJSON_UnknownString_ReturnsError(t *testing.T) {
	var v ViewMode
	err := json.Unmarshal([]byte(`"UnknownMode"`), &v)
	if err == nil {
		t.Error("expected error for unknown string value, got nil")
	}
}

// TestViewMode_UnmarshalJSON_Integer_ReturnsError verifies that unmarshaling
// an integer value (not a string) returns an error.
func TestViewMode_UnmarshalJSON_Integer_ReturnsError(t *testing.T) {
	var v ViewMode
	err := json.Unmarshal([]byte(`0`), &v)
	if err == nil {
		t.Error("expected error for integer value, got nil")
	}
}

// TestViewMode_UnmarshalJSON_EmptyString_ReturnsError verifies that unmarshaling
// an empty string returns an error.
func TestViewMode_UnmarshalJSON_EmptyString_ReturnsError(t *testing.T) {
	var v ViewMode
	err := json.Unmarshal([]byte(`""`), &v)
	if err == nil {
		t.Error("expected error for empty string, got nil")
	}
}

// === RoundTrip Tests ===

// TestViewMode_RoundTrip_AllModes verifies that all ViewMode values can be marshaled
// and then unmarshaled to get back the original value.
func TestViewMode_RoundTrip_AllModes(t *testing.T) {
	modes := []ViewMode{
		ViewModeHtmlAllowInput,
		ViewModeHtmlNoInput,
		ViewModeVideoAllowInput,
		ViewModeVideoNoInput,
	}
	for _, original := range modes {
		t.Run(fmt.Sprintf("Mode_%d", original), func(t *testing.T) {
			// Marshal
			data, err := json.Marshal(original)
			if err != nil {
				t.Fatalf("marshal error: %v", err)
			}

			// Unmarshal
			var recovered ViewMode
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
