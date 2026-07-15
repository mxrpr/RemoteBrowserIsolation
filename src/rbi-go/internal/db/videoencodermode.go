package db

import (
	"encoding/json"
	"fmt"
)

// String returns the string name of the VideoEncoderMode value, matching the C#
// VideoEncoderMode enum member names as serialised by JsonStringEnumConverter
// (e.g. "Auto", "Cpu", "Gpu").
func (m VideoEncoderMode) String() string {
	switch m {
	case VideoEncoderModeAuto:
		return "Auto"
	case VideoEncoderModeCpu:
		return "Cpu"
	case VideoEncoderModeGpu:
		return "Gpu"
	default:
		return fmt.Sprintf("VideoEncoderMode(%d)", int(m))
	}
}

// MarshalJSON serialises VideoEncoderMode as its string name so the JSON wire
// format matches the C# JsonStringEnumConverter output ("Auto", "Cpu", "Gpu").
func (m VideoEncoderMode) MarshalJSON() ([]byte, error) {
	return json.Marshal(m.String())
}

// UnmarshalJSON deserialises VideoEncoderMode from its string name, accepting
// the same values the C# JsonStringEnumConverter produces.
func (m *VideoEncoderMode) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return fmt.Errorf("videoEncoderMode: unmarshal string: %w", err)
	}
	switch s {
	case "Auto":
		*m = VideoEncoderModeAuto
	case "Cpu":
		*m = VideoEncoderModeCpu
	case "Gpu":
		*m = VideoEncoderModeGpu
	default:
		return fmt.Errorf("videoEncoderMode: unknown value %q (expected Auto, Cpu, or Gpu)", s)
	}
	return nil
}
