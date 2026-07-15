package settings

import (
	"database/sql"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"rbi-go/internal/db"
)

// VideoEncoderStore persists the whole-server video encoder mode (Auto/Cpu/Gpu) in
// the single-row VideoEncoderSettings table and caches the value in memory so reads
// are fast. Mirrors the C# VideoEncoderSettingsStore singleton. Safe for concurrent use.
type VideoEncoderStore struct {
	sqlDB *sql.DB
	mu    sync.Mutex
	// cached holds the last-read mode, or nil before the first DB read.
	cached *db.VideoEncoderMode
}

// NewVideoEncoderStore constructs a VideoEncoderStore backed by the given DB.
func NewVideoEncoderStore(sqlDB *sql.DB) *VideoEncoderStore {
	return &VideoEncoderStore{sqlDB: sqlDB}
}

// GetMode returns the configured video encoder mode. On the first call it reads the
// DB row (Id=1); subsequent calls return the in-memory cache. Defaults to Auto when
// no row exists yet, matching the C# VideoEncoderSettingsStore.GetModeAsync default.
func (s *VideoEncoderStore) GetMode() (db.VideoEncoderMode, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.cached != nil {
		return *s.cached, nil
	}

	mode, err := s.loadFromDB()
	if err != nil {
		return db.VideoEncoderModeAuto, fmt.Errorf("video encoder: load from db: %w", err)
	}
	s.cached = &mode
	return mode, nil
}

// SetMode persists mode to the DB (INSERT OR REPLACE on Id=1) and refreshes the
// in-memory cache so the change is visible to the next GetMode call without a restart.
// Mirrors the C# VideoEncoderSettingsStore.SetModeAsync upsert pattern.
func (s *VideoEncoderStore) SetMode(mode db.VideoEncoderMode) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := s.sqlDB.Exec(
		`INSERT OR REPLACE INTO VideoEncoderSettings (Id, Mode, UpdatedAt) VALUES (1, ?, ?)`,
		int(mode), now,
	)
	if err != nil {
		return fmt.Errorf("video encoder: upsert: %w", err)
	}

	s.mu.Lock()
	s.cached = &mode
	s.mu.Unlock()

	slog.Info("Video encoder mode updated", "mode", mode)
	return nil
}

// loadFromDB reads the single row (Id=1) from VideoEncoderSettings, returning
// VideoEncoderModeAuto when no row exists yet. Must be called with s.mu held.
func (s *VideoEncoderStore) loadFromDB() (db.VideoEncoderMode, error) {
	var mode int
	err := s.sqlDB.QueryRow(
		`SELECT Mode FROM VideoEncoderSettings WHERE Id = 1`,
	).Scan(&mode)
	if err == sql.ErrNoRows {
		return db.VideoEncoderModeAuto, nil
	}
	if err != nil {
		return db.VideoEncoderModeAuto, err
	}
	return db.VideoEncoderMode(mode), nil
}
