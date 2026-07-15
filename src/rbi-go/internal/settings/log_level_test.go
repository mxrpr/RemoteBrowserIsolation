package settings

import (
	"encoding/json"
	"log/slog"
	"testing"

	"rbi-go/internal/db"
)

// === LogLevelName MarshalJSON Tests ===

// TestLogLevelName_MarshalJSON_AllValues verifies that all LogLevelName values marshal to the correct JSON strings.
func TestLogLevelName_MarshalJSON_AllValues(t *testing.T) {
	tests := []struct {
		level    LogLevelName
		expected string
	}{
		{LogLevelTrace, `"Trace"`},
		{LogLevelDebug, `"Debug"`},
		{LogLevelInformation, `"Information"`},
		{LogLevelWarning, `"Warning"`},
		{LogLevelError, `"Error"`},
		{LogLevelCritical, `"Critical"`},
		{LogLevelNone, `"None"`},
	}

	for _, tt := range tests {
		t.Run(string(tt.level), func(t *testing.T) {
			data, err := json.Marshal(tt.level)
			if err != nil {
				t.Fatalf("marshal error: %v", err)
			}
			if string(data) != tt.expected {
				t.Errorf("expected %s, got %s", tt.expected, string(data))
			}
		})
	}
}

// === LogLevelName UnmarshalJSON Tests ===

// TestLogLevelName_UnmarshalJSON_AllValues verifies that all LogLevelName strings unmarshal correctly.
func TestLogLevelName_UnmarshalJSON_AllValues(t *testing.T) {
	tests := []struct {
		input    string
		expected LogLevelName
	}{
		{`"Trace"`, LogLevelTrace},
		{`"Debug"`, LogLevelDebug},
		{`"Information"`, LogLevelInformation},
		{`"Warning"`, LogLevelWarning},
		{`"Error"`, LogLevelError},
		{`"Critical"`, LogLevelCritical},
		{`"None"`, LogLevelNone},
	}

	for _, tt := range tests {
		t.Run(string(tt.expected), func(t *testing.T) {
			var level LogLevelName
			if err := json.Unmarshal([]byte(tt.input), &level); err != nil {
				t.Fatalf("unmarshal error: %v", err)
			}
			if level != tt.expected {
				t.Errorf("expected %s, got %s", tt.expected, level)
			}
		})
	}
}

// TestLogLevelName_UnmarshalJSON_UnknownString_ReturnsError verifies that an unknown string value returns an error.
func TestLogLevelName_UnmarshalJSON_UnknownString_ReturnsError(t *testing.T) {
	var level LogLevelName
	err := json.Unmarshal([]byte(`"UnknownLevel"`), &level)
	if err == nil {
		t.Error("expected error for unknown string value, got nil")
	}
}

// TestLogLevelName_UnmarshalJSON_EmptyString_ReturnsError verifies that an empty string returns an error.
func TestLogLevelName_UnmarshalJSON_EmptyString_ReturnsError(t *testing.T) {
	var level LogLevelName
	err := json.Unmarshal([]byte(`""`), &level)
	if err == nil {
		t.Error("expected error for empty string, got nil")
	}
}

// TestLogLevelName_UnmarshalJSON_Integer_ReturnsError verifies that an integer value (not a string) returns an error.
func TestLogLevelName_UnmarshalJSON_Integer_ReturnsError(t *testing.T) {
	var level LogLevelName
	err := json.Unmarshal([]byte(`0`), &level)
	if err == nil {
		t.Error("expected error for integer value, got nil")
	}
}

// === LogLevelName ToSlogLevel Tests ===

// TestLogLevelName_ToSlogLevel_Trace verifies that LogLevelTrace maps to the correct slog.Level.
func TestLogLevelName_ToSlogLevel_Trace(t *testing.T) {
	expected := slog.LevelDebug - 4
	if got := LogLevelTrace.ToSlogLevel(); got != expected {
		t.Errorf("expected %v, got %v", expected, got)
	}
}

// TestLogLevelName_ToSlogLevel_Debug verifies that LogLevelDebug maps to slog.LevelDebug.
func TestLogLevelName_ToSlogLevel_Debug(t *testing.T) {
	expected := slog.LevelDebug
	if got := LogLevelDebug.ToSlogLevel(); got != expected {
		t.Errorf("expected %v, got %v", expected, got)
	}
}

// TestLogLevelName_ToSlogLevel_Information verifies that LogLevelInformation maps to slog.LevelInfo.
func TestLogLevelName_ToSlogLevel_Information(t *testing.T) {
	expected := slog.LevelInfo
	if got := LogLevelInformation.ToSlogLevel(); got != expected {
		t.Errorf("expected %v, got %v", expected, got)
	}
}

// TestLogLevelName_ToSlogLevel_Warning verifies that LogLevelWarning maps to slog.LevelWarn.
func TestLogLevelName_ToSlogLevel_Warning(t *testing.T) {
	expected := slog.LevelWarn
	if got := LogLevelWarning.ToSlogLevel(); got != expected {
		t.Errorf("expected %v, got %v", expected, got)
	}
}

// TestLogLevelName_ToSlogLevel_Error verifies that LogLevelError maps to slog.LevelError.
func TestLogLevelName_ToSlogLevel_Error(t *testing.T) {
	expected := slog.LevelError
	if got := LogLevelError.ToSlogLevel(); got != expected {
		t.Errorf("expected %v, got %v", expected, got)
	}
}

// TestLogLevelName_ToSlogLevel_Critical verifies that LogLevelCritical maps to slog.LevelError + 4.
func TestLogLevelName_ToSlogLevel_Critical(t *testing.T) {
	expected := slog.LevelError + 4
	if got := LogLevelCritical.ToSlogLevel(); got != expected {
		t.Errorf("expected %v, got %v", expected, got)
	}
}

// TestLogLevelName_ToSlogLevel_None verifies that LogLevelNone maps to a very high sentinel level.
func TestLogLevelName_ToSlogLevel_None(t *testing.T) {
	expected := slog.Level(1<<31 - 1)
	if got := LogLevelNone.ToSlogLevel(); got != expected {
		t.Errorf("expected %v, got %v", expected, got)
	}
}

// === LogLevelNameFromConfig Tests ===

// TestLogLevelNameFromConfig_KnownValues_ReturnsSelf verifies that known config strings return themselves.
func TestLogLevelNameFromConfig_KnownValues_ReturnsSelf(t *testing.T) {
	tests := []string{
		"Trace",
		"Debug",
		"Information",
		"Warning",
		"Error",
		"Critical",
		"None",
	}

	for _, s := range tests {
		t.Run(s, func(t *testing.T) {
			result := LogLevelNameFromConfig(s)
			expected := LogLevelName(s)
			if result != expected {
				t.Errorf("expected %s, got %s", expected, result)
			}
		})
	}
}

// TestLogLevelNameFromConfig_UnknownString_ReturnsInformation verifies that unknown strings default to Information.
func TestLogLevelNameFromConfig_UnknownString_ReturnsInformation(t *testing.T) {
	result := LogLevelNameFromConfig("UnknownLevel")
	if result != LogLevelInformation {
		t.Errorf("expected LogLevelInformation, got %s", result)
	}
}

// TestLogLevelNameFromConfig_EmptyString_ReturnsInformation verifies that an empty string defaults to Information.
func TestLogLevelNameFromConfig_EmptyString_ReturnsInformation(t *testing.T) {
	result := LogLevelNameFromConfig("")
	if result != LogLevelInformation {
		t.Errorf("expected LogLevelInformation, got %s", result)
	}
}

// === LogLevelStore GetLevel Tests ===

// TestLogLevelStore_GetLevel_FreshDB_ReturnsInformation verifies that GetLevel on a fresh DB returns Information (the default).
func TestLogLevelStore_GetLevel_FreshDB_ReturnsInformation(t *testing.T) {
	database, err := db.Connect(":memory:")
	if err != nil {
		t.Fatalf("failed to connect to DB: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })

	var levelVar slog.LevelVar
	store := NewLogLevelStore(database.Unwrap(), &levelVar)
	level, err := store.GetLevel()
	if err != nil {
		t.Fatalf("GetLevel failed: %v", err)
	}

	if level != LogLevelInformation {
		t.Errorf("expected LogLevelInformation, got %s", level)
	}
}

// TestLogLevelStore_GetLevel_FreshDB_SetsLevelVarToInfo verifies that GetLevel sets the LevelVar to Info.
func TestLogLevelStore_GetLevel_FreshDB_SetsLevelVarToInfo(t *testing.T) {
	database, err := db.Connect(":memory:")
	if err != nil {
		t.Fatalf("failed to connect to DB: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })

	var levelVar slog.LevelVar
	store := NewLogLevelStore(database.Unwrap(), &levelVar)
	_, err = store.GetLevel()
	if err != nil {
		t.Fatalf("GetLevel failed: %v", err)
	}

	expectedLevel := slog.LevelInfo
	if levelVar.Level() != expectedLevel {
		t.Errorf("expected LevelVar to be set to Info (%v), got %v", expectedLevel, levelVar.Level())
	}
}

// TestLogLevelStore_SetGet_RoundTrip_AllLevels verifies that SetLevel and GetLevel round-trip correctly for all levels.
func TestLogLevelStore_SetGet_RoundTrip_AllLevels(t *testing.T) {
	levels := []LogLevelName{
		LogLevelTrace,
		LogLevelDebug,
		LogLevelInformation,
		LogLevelWarning,
		LogLevelError,
		LogLevelCritical,
		LogLevelNone,
	}

	for _, level := range levels {
		t.Run(string(level), func(t *testing.T) {
			database, err := db.Connect(":memory:")
			if err != nil {
				t.Fatalf("failed to connect to DB: %v", err)
			}
			t.Cleanup(func() { _ = database.Close() })

			var levelVar slog.LevelVar
			store := NewLogLevelStore(database.Unwrap(), &levelVar)
			if err := store.SetLevel(level); err != nil {
				t.Fatalf("SetLevel failed: %v", err)
			}

			got, err := store.GetLevel()
			if err != nil {
				t.Fatalf("GetLevel failed: %v", err)
			}

			if got != level {
				t.Errorf("expected %s, got %s", level, got)
			}
		})
	}
}

// TestLogLevelStore_SetLevel_MutatesLevelVar verifies that SetLevel updates the slog.LevelVar.
func TestLogLevelStore_SetLevel_MutatesLevelVar(t *testing.T) {
	database, err := db.Connect(":memory:")
	if err != nil {
		t.Fatalf("failed to connect to DB: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })

	var levelVar slog.LevelVar
	store := NewLogLevelStore(database.Unwrap(), &levelVar)

	// Set to Debug
	if err := store.SetLevel(LogLevelDebug); err != nil {
		t.Fatalf("SetLevel failed: %v", err)
	}

	expectedLevel := slog.LevelDebug
	if levelVar.Level() != expectedLevel {
		t.Errorf("expected LevelVar to be Debug (%v), got %v", expectedLevel, levelVar.Level())
	}
}

// TestLogLevelStore_SetLevel_LevelVarChanges_WithoutRestart verifies that changing the log level takes effect immediately.
func TestLogLevelStore_SetLevel_LevelVarChanges_WithoutRestart(t *testing.T) {
	database, err := db.Connect(":memory:")
	if err != nil {
		t.Fatalf("failed to connect to DB: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })

	var levelVar slog.LevelVar
	store := NewLogLevelStore(database.Unwrap(), &levelVar)

	// Set to Warning
	if err := store.SetLevel(LogLevelWarning); err != nil {
		t.Fatalf("SetLevel(Warning) failed: %v", err)
	}
	if levelVar.Level() != slog.LevelWarn {
		t.Errorf("expected LevelVar to be Warn, got %v", levelVar.Level())
	}

	// Change to Error
	if err := store.SetLevel(LogLevelError); err != nil {
		t.Fatalf("SetLevel(Error) failed: %v", err)
	}
	if levelVar.Level() != slog.LevelError {
		t.Errorf("expected LevelVar to be Error, got %v", levelVar.Level())
	}
}

// TestLogLevelStore_SetLevel_Overwrites_PreviousValue verifies that SetLevel replaces a previous value in the DB.
func TestLogLevelStore_SetLevel_Overwrites_PreviousValue(t *testing.T) {
	database, err := db.Connect(":memory:")
	if err != nil {
		t.Fatalf("failed to connect to DB: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })

	var levelVar1 slog.LevelVar
	store1 := NewLogLevelStore(database.Unwrap(), &levelVar1)

	// Set to Debug
	if err := store1.SetLevel(LogLevelDebug); err != nil {
		t.Fatalf("SetLevel(Debug) failed: %v", err)
	}

	// Overwrite with Error
	if err := store1.SetLevel(LogLevelError); err != nil {
		t.Fatalf("SetLevel(Error) failed: %v", err)
	}

	// Create a new store and verify the DB has the new value
	var levelVar2 slog.LevelVar
	store2 := NewLogLevelStore(database.Unwrap(), &levelVar2)
	level, err := store2.GetLevel()
	if err != nil {
		t.Fatalf("GetLevel failed: %v", err)
	}

	if level != LogLevelError {
		t.Errorf("expected Error in new store, got %s", level)
	}
}

// TestLogLevelStore_GetLevel_ReadsPersistedValueAfterRestart verifies that a new store instance reads the persisted value.
func TestLogLevelStore_GetLevel_ReadsPersistedValueAfterRestart(t *testing.T) {
	database, err := db.Connect(":memory:")
	if err != nil {
		t.Fatalf("failed to connect to DB: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })

	// First store: set to Debug
	var levelVar1 slog.LevelVar
	store1 := NewLogLevelStore(database.Unwrap(), &levelVar1)
	if err := store1.SetLevel(LogLevelDebug); err != nil {
		t.Fatalf("SetLevel failed: %v", err)
	}

	// Second store (simulating restart): should read the persisted value
	var levelVar2 slog.LevelVar
	store2 := NewLogLevelStore(database.Unwrap(), &levelVar2)
	level, err := store2.GetLevel()
	if err != nil {
		t.Fatalf("GetLevel failed: %v", err)
	}

	if level != LogLevelDebug {
		t.Errorf("expected persisted Debug, got %s", level)
	}
}

// TestLogLevelStore_GetLevel_CorruptDBValue_FallsBackToInformation verifies that corrupt DB values fall back to Information.
func TestLogLevelStore_GetLevel_CorruptDBValue_FallsBackToInformation(t *testing.T) {
	database, err := db.Connect(":memory:")
	if err != nil {
		t.Fatalf("failed to connect to DB: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })

	// Insert a corrupt value directly into the DB
	now := "2025-01-01T00:00:00Z"
	_, err = database.Unwrap().Exec(
		`INSERT OR REPLACE INTO LogLevelSettings (Id, Level, UpdatedAt) VALUES (1, ?, ?)`,
		"InvalidLevel", now,
	)
	if err != nil {
		t.Fatalf("failed to insert corrupt value: %v", err)
	}

	// Create a store and verify it falls back to Information
	var levelVar slog.LevelVar
	store := NewLogLevelStore(database.Unwrap(), &levelVar)
	level, err := store.GetLevel()
	if err != nil {
		t.Fatalf("GetLevel failed: %v", err)
	}

	if level != LogLevelInformation {
		t.Errorf("expected fallback to Information, got %s", level)
	}
}
