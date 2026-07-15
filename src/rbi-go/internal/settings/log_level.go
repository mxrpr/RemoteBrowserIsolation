package settings

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

// LogLevelName represents a C# Microsoft.Extensions.Logging.LogLevel name as
// serialised by JsonStringEnumConverter. The Go slog package does not define an
// equivalent named type, so we use a local string type with custom JSON
// (un)marshalling to validate and map the C# enum member names.
type LogLevelName string

const (
	// LogLevelTrace maps to slog.LevelDebug-4 (slog has no dedicated Trace level).
	LogLevelTrace LogLevelName = "Trace"
	// LogLevelDebug maps to slog.LevelDebug.
	LogLevelDebug LogLevelName = "Debug"
	// LogLevelInformation maps to slog.LevelInfo.
	LogLevelInformation LogLevelName = "Information"
	// LogLevelWarning maps to slog.LevelWarn.
	LogLevelWarning LogLevelName = "Warning"
	// LogLevelError maps to slog.LevelError.
	LogLevelError LogLevelName = "Error"
	// LogLevelCritical maps to slog.LevelError+4 (slog has no dedicated Critical level).
	LogLevelCritical LogLevelName = "Critical"
	// LogLevelNone disables all logging (slog.Level(math.MaxInt)).
	LogLevelNone LogLevelName = "None"
)

// allLogLevelNames is the set of valid LogLevelName values, used for validation.
var allLogLevelNames = map[LogLevelName]struct{}{
	LogLevelTrace:       {},
	LogLevelDebug:       {},
	LogLevelInformation: {},
	LogLevelWarning:     {},
	LogLevelError:       {},
	LogLevelCritical:    {},
	LogLevelNone:        {},
}

// MarshalJSON serialises LogLevelName as a JSON string, preserving the C# enum
// member name casing (e.g. "Information", not "information").
func (l LogLevelName) MarshalJSON() ([]byte, error) {
	return json.Marshal(string(l))
}

// UnmarshalJSON deserialises a JSON string into a LogLevelName, rejecting any
// value not present in allLogLevelNames.
func (l *LogLevelName) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return fmt.Errorf("logLevel: unmarshal string: %w", err)
	}
	candidate := LogLevelName(s)
	if _, ok := allLogLevelNames[candidate]; !ok {
		return fmt.Errorf("logLevel: unknown value %q (expected Trace, Debug, Information, Warning, Error, Critical, or None)", s)
	}
	*l = candidate
	return nil
}

// ToSlogLevel converts a LogLevelName to the corresponding slog.Level. Mappings for
// levels slog does not directly define (Trace → Debug-4, Critical → Error+4, None →
// very high sentinel) are documented in each LogLevelName constant's comment.
func (l LogLevelName) ToSlogLevel() slog.Level {
	switch l {
	case LogLevelTrace:
		return slog.LevelDebug - 4
	case LogLevelDebug:
		return slog.LevelDebug
	case LogLevelInformation:
		return slog.LevelInfo
	case LogLevelWarning:
		return slog.LevelWarn
	case LogLevelError:
		return slog.LevelError
	case LogLevelCritical:
		return slog.LevelError + 4
	case LogLevelNone:
		// Disable all logging by setting an absurdly high level.
		return slog.Level(1<<31 - 1)
	default:
		return slog.LevelInfo
	}
}

// LogLevelNameFromConfig converts the config/appsettings string form used by the Go
// config loader (same strings as C# Logging:LogLevel:Default) to a LogLevelName.
// Unrecognised values fall back to Information.
func LogLevelNameFromConfig(s string) LogLevelName {
	candidate := LogLevelName(s)
	if _, ok := allLogLevelNames[candidate]; ok {
		return candidate
	}
	return LogLevelInformation
}

// LogLevelStore persists the whole-server minimum log level in the single-row
// LogLevelSettings table and mirrors changes immediately into a slog.LevelVar so
// the new level applies to the very next log call without a restart. Mirrors the C#
// LogLevelSettingsStore + LogLevelState pair. Safe for concurrent use.
type LogLevelStore struct {
	sqlDB    *sql.DB
	levelVar *slog.LevelVar
	mu       sync.Mutex
	// cached holds the last-read level name, or empty string before the first DB read.
	cached LogLevelName
}

// NewLogLevelStore constructs a LogLevelStore backed by the given DB. levelVar is
// the slog.LevelVar used as the handler's Level option; SetLevel updates it live.
func NewLogLevelStore(sqlDB *sql.DB, levelVar *slog.LevelVar) *LogLevelStore {
	return &LogLevelStore{sqlDB: sqlDB, levelVar: levelVar}
}

// GetLevel returns the configured minimum log level. On the first call it reads the
// DB row (Id=1) and applies the level to levelVar; subsequent calls return the
// in-memory cache. Defaults to Information when no row exists yet, matching C#
// LogLevelSettingsStore.GetLevelAsync.
func (s *LogLevelStore) GetLevel() (LogLevelName, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.cached != "" {
		return s.cached, nil
	}

	level, err := s.loadFromDB()
	if err != nil {
		return LogLevelInformation, fmt.Errorf("log level: load from db: %w", err)
	}
	s.cached = level
	s.levelVar.Set(level.ToSlogLevel())
	return level, nil
}

// SetLevel persists level to the DB (INSERT OR REPLACE on Id=1), refreshes the
// in-memory cache, and updates levelVar so the change takes effect for the very
// next log call. Mirrors LogLevelSettingsStore.SetLevelAsync + LogLevelState.
func (s *LogLevelStore) SetLevel(level LogLevelName) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := s.sqlDB.Exec(
		`INSERT OR REPLACE INTO LogLevelSettings (Id, Level, UpdatedAt) VALUES (1, ?, ?)`,
		string(level), now,
	)
	if err != nil {
		return fmt.Errorf("log level: upsert: %w", err)
	}

	s.mu.Lock()
	s.cached = level
	s.mu.Unlock()

	// Apply the new level to the running slog handler immediately — no restart needed.
	s.levelVar.Set(level.ToSlogLevel())
	slog.Info("Log level updated", "level", string(level))
	return nil
}

// loadFromDB reads the single row (Id=1) from LogLevelSettings, returning
// LogLevelInformation when no row exists yet. Must be called with s.mu held.
func (s *LogLevelStore) loadFromDB() (LogLevelName, error) {
	var levelStr string
	err := s.sqlDB.QueryRow(
		`SELECT Level FROM LogLevelSettings WHERE Id = 1`,
	).Scan(&levelStr)
	if err == sql.ErrNoRows {
		return LogLevelInformation, nil
	}
	if err != nil {
		return LogLevelInformation, err
	}
	// Validate the stored string; fall back to Information if corrupted.
	candidate := LogLevelName(levelStr)
	if _, ok := allLogLevelNames[candidate]; !ok {
		slog.Warn("LogLevelSettings row has unrecognised level, defaulting to Information",
			"stored", levelStr)
		return LogLevelInformation, nil
	}
	return candidate, nil
}
