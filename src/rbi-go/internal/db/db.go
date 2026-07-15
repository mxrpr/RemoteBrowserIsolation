package db

import (
	"database/sql"
	"fmt"
	"strings"

	_ "modernc.org/sqlite" // register the "sqlite" driver with database/sql
)

// DB wraps a *sql.DB and exposes the rbi-go data layer. All callers should use
// the exported methods on DB rather than accessing the inner sql.DB directly.
type DB struct {
	inner *sql.DB
}

// Unwrap returns the underlying *sql.DB. Used by callers that need direct query
// access (e.g. service packages in later parts that execute typed queries).
func (d *DB) Unwrap() *sql.DB {
	return d.inner
}

// Connect opens (or creates) a SQLite database at the path encoded in connStr,
// then calls CreateSchema to ensure all six tables exist. connStr is accepted in
// the ADO.NET "Data Source=<path>" format used by the C# appsettings.json, or as
// a bare file path. Returns an error if the file cannot be opened or the schema
// cannot be applied.
func Connect(connStr string) (*DB, error) {
	path := parsePath(connStr)
	if path == "" {
		return nil, fmt.Errorf("db: empty path in connection string %q", connStr)
	}

	// Open the SQLite database. modernc.org/sqlite is a pure-Go driver registered
	// under the "sqlite" driver name. The database file is created if it doesn't exist.
	sqlDB, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("db: open %q: %w", path, err)
	}

	// Limit the pool to a single connection. SQLite connection-scoped PRAGMAs
	// (foreign_keys, below) only apply to the connection they are issued on; with
	// multiple pool connections each new connection would start with foreign keys
	// disabled. A single connection also serialises writers and avoids SQLITE_BUSY.
	sqlDB.SetMaxOpenConns(1)

	// Verify the connection is usable (validates the file is a valid SQLite database
	// or creates it fresh if it didn't exist).
	if err := sqlDB.Ping(); err != nil {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("db: ping %q: %w", path, err)
	}

	// Enable WAL mode for better concurrent read performance and resilience.
	if _, err := sqlDB.Exec("PRAGMA journal_mode=WAL"); err != nil {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("db: enable WAL: %w", err)
	}

	// Enable foreign key enforcement (SQLite disables it by default).
	if _, err := sqlDB.Exec("PRAGMA foreign_keys=ON"); err != nil {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("db: enable foreign keys: %w", err)
	}

	d := &DB{inner: sqlDB}
	if err := CreateSchema(d); err != nil {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("db: create schema: %w", err)
	}

	return d, nil
}

// Close releases the underlying database connection. Should be deferred by callers
// that receive a *DB from Connect.
func (d *DB) Close() error {
	return d.inner.Close()
}

// parsePath extracts the SQLite file path from a connection string. Understands:
//   - "Data Source=<path>" (ADO.NET format used in C# appsettings.json)
//   - "<path>" (bare file path)
func parsePath(connStr string) string {
	connStr = strings.TrimSpace(connStr)
	lower := strings.ToLower(connStr)
	if idx := strings.Index(lower, "data source="); idx >= 0 {
		// Advance past "data source=" (12 characters).
		val := connStr[idx+12:]
		// Trim any trailing semicolon-separated options (e.g. "Data Source=foo.db;Mode=ReadWrite").
		if semi := strings.IndexByte(val, ';'); semi >= 0 {
			val = val[:semi]
		}
		return strings.TrimSpace(val)
	}
	return connStr
}

// CreateSchema runs CREATE TABLE IF NOT EXISTS for all six tables and their indexes.
// It is idempotent — safe to call on both a fresh database and an existing one.
// Table definitions mirror the C# EF Core entities but use SQLite-native types:
//   - INTEGER PRIMARY KEY for auto-increment int PKs (except VideoEncoderSettings and
//     LogLevelSettings which use explicit Id=1 via INSERT OR REPLACE).
//   - TEXT for string columns; BLOB for byte[].
//   - INTEGER for bool (0/1) and enum values.
//   - TEXT (ISO-8601 UTC) for DateTime columns.
func CreateSchema(d *DB) error {
	stmts := []string{
		// AdminUsers: at most one row (bootstrap semantics). UNIQUE index on Email.
		`CREATE TABLE IF NOT EXISTS AdminUsers (
			Id           INTEGER PRIMARY KEY AUTOINCREMENT,
			Email        TEXT    NOT NULL DEFAULT '',
			PasswordHash TEXT    NOT NULL DEFAULT '',
			CreatedAt    TEXT    NOT NULL DEFAULT ''
		)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS IX_AdminUsers_Email
			ON AdminUsers (Email)`,

		// SitePolicies: one rule per hostname. UNIQUE index on HostPattern.
		// ViewMode stored as INTEGER matching C# enum ordinal values.
		`CREATE TABLE IF NOT EXISTS SitePolicies (
			Id          INTEGER PRIMARY KEY AUTOINCREMENT,
			HostPattern TEXT    NOT NULL DEFAULT '',
			ViewMode    INTEGER NOT NULL DEFAULT 0,
			CreatedAt   TEXT    NOT NULL DEFAULT '',
			UpdatedAt   TEXT    NOT NULL DEFAULT ''
		)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS IX_SitePolicies_HostPattern
			ON SitePolicies (HostPattern)`,

		// RequestLogs: append-only audit log, one row per browse decision.
		// ClientIp allows NULL (best-effort; may be unavailable).
		`CREATE TABLE IF NOT EXISTS RequestLogs (
			Id        INTEGER PRIMARY KEY AUTOINCREMENT,
			Timestamp TEXT    NOT NULL DEFAULT '',
			Url       TEXT    NOT NULL DEFAULT '',
			Host      TEXT    NOT NULL DEFAULT '',
			Decision  TEXT    NOT NULL DEFAULT '',
			Allowed   INTEGER NOT NULL DEFAULT 0,
			ClientIp  TEXT
		)`,

		// RootCertificateAuthorities: only one row is meaningful at a time.
		// PfxBytes is stored as BLOB (raw bytes); PfxPassword stored in plaintext
		// (same trust boundary as the DB itself — see C# entity comment).
		`CREATE TABLE IF NOT EXISTS RootCertificateAuthorities (
			Id          INTEGER PRIMARY KEY AUTOINCREMENT,
			Subject     TEXT    NOT NULL DEFAULT '',
			NotBefore   TEXT    NOT NULL DEFAULT '',
			NotAfter    TEXT    NOT NULL DEFAULT '',
			Thumbprint  TEXT    NOT NULL DEFAULT '',
			UploadedAt  TEXT    NOT NULL DEFAULT '',
			PfxBytes    BLOB    NOT NULL DEFAULT X'',
			PfxPassword TEXT    NOT NULL DEFAULT ''
		)`,

		// VideoEncoderSettings: single-row settings table. Id is always 1.
		// CHECK (Id = 1) provides a hard DB-level guarantee on top of the
		// INSERT OR REPLACE Id=1 convention (matching C# ValueGeneratedNever).
		`CREATE TABLE IF NOT EXISTS VideoEncoderSettings (
			Id        INTEGER PRIMARY KEY CHECK (Id = 1),
			Mode      INTEGER NOT NULL DEFAULT 0,
			UpdatedAt TEXT    NOT NULL DEFAULT ''
		)`,

		// LogLevelSettings: same single-row convention as VideoEncoderSettings.
		// CHECK (Id = 1) enforces the single-row invariant at the DB level.
		// Level stored as TEXT so it maps directly to slog/config strings without
		// a numeric enum translation (differs from C# LogLevel enum which is int).
		`CREATE TABLE IF NOT EXISTS LogLevelSettings (
			Id        INTEGER PRIMARY KEY CHECK (Id = 1),
			Level     TEXT    NOT NULL DEFAULT 'Information',
			UpdatedAt TEXT    NOT NULL DEFAULT ''
		)`,
	}

	for _, stmt := range stmts {
		if _, err := d.inner.Exec(stmt); err != nil {
			return fmt.Errorf("exec %q: %w", stmt[:min(50, len(stmt))], err)
		}
	}
	return nil
}
