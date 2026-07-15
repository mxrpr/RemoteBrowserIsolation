package db

import (
	"encoding/json"
	"fmt"
)

// String returns the string name of the ViewMode value, matching the C# enum member
// names as serialized by JsonStringEnumConverter in ASP.NET Core (e.g. "HtmlAllowInput").
func (v ViewMode) String() string {
	switch v {
	case ViewModeHtmlAllowInput:
		return "HtmlAllowInput"
	case ViewModeHtmlNoInput:
		return "HtmlNoInput"
	case ViewModeVideoAllowInput:
		return "VideoAllowInput"
	case ViewModeVideoNoInput:
		return "VideoNoInput"
	default:
		return fmt.Sprintf("ViewMode(%d)", int(v))
	}
}

// MarshalJSON serializes ViewMode as its string name so the JSON wire format matches
// the C# JsonStringEnumConverter output (e.g. "HtmlAllowInput" rather than 0).
func (v ViewMode) MarshalJSON() ([]byte, error) {
	return json.Marshal(v.String())
}

// UnmarshalJSON deserializes ViewMode from its string name, accepting the same values
// that the C# JsonStringEnumConverter produces on the write side.
func (v *ViewMode) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return fmt.Errorf("viewMode: unmarshal string: %w", err)
	}
	switch s {
	case "HtmlAllowInput":
		*v = ViewModeHtmlAllowInput
	case "HtmlNoInput":
		*v = ViewModeHtmlNoInput
	case "VideoAllowInput":
		*v = ViewModeVideoAllowInput
	case "VideoNoInput":
		*v = ViewModeVideoNoInput
	default:
		return fmt.Errorf("viewMode: unknown value %q", s)
	}
	return nil
}
