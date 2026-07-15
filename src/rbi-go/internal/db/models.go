// Package db provides SQLite connection management and schema creation for the
// rbi-go backend. It models the same six logical tables as the C# EF Core schema
// (AdminUsers, SitePolicies, RequestLogs, RootCertificateAuthorities,
// VideoEncoderSettings, LogLevelSetting) with equivalent columns and constraints,
// but uses its own separate DB file (rbi-go.db) and schema — no coupling to EF
// Core's migration format.
package db

import "time"

// ViewMode mirrors the C# ViewMode enum values. Stored as INTEGER in SQLite.
type ViewMode int

const (
	// ViewModeHtmlAllowInput — raw HTML relayed, full input allowed client-side.
	ViewModeHtmlAllowInput ViewMode = 0
	// ViewModeHtmlNoInput — raw HTML relayed, input disabled client-side (client-enforced only).
	ViewModeHtmlNoInput ViewMode = 1
	// ViewModeVideoAllowInput — server-side VP8 video stream, keyboard + mouse forwarded.
	ViewModeVideoAllowInput ViewMode = 2
	// ViewModeVideoNoInput — server-side VP8 video stream, keyboard dropped server-side.
	ViewModeVideoNoInput ViewMode = 3
)

// VideoEncoderMode mirrors the C# VideoEncoderMode enum. Stored as INTEGER in SQLite.
type VideoEncoderMode int

const (
	// VideoEncoderModeAuto — probe for GPU, log result, fall back to CPU.
	VideoEncoderModeAuto VideoEncoderMode = 0
	// VideoEncoderModeCpu — always use software VP8 encoder.
	VideoEncoderModeCpu VideoEncoderMode = 1
	// VideoEncoderModeGpu — request hardware encode; fails loudly if unavailable.
	VideoEncoderModeGpu VideoEncoderMode = 2
)

// AdminUser represents a row in the AdminUsers table. At most one row exists
// (bootstrap semantics — first login creates it). Email has a UNIQUE index.
type AdminUser struct {
	// Id is the auto-incrementing primary key.
	Id int64
	// Email is stored as provided; case-insensitive comparisons are done in application code.
	Email string
	// PasswordHash is the bcrypt hash of the admin password (never the raw password).
	PasswordHash string
	// CreatedAt is the UTC time the account was bootstrapped.
	CreatedAt time.Time
}

// SitePolicy represents a row in the SitePolicies table. One rule per host;
// HostPattern has a UNIQUE index. Absence of a row means deny.
type SitePolicy struct {
	// Id is the auto-incrementing primary key.
	Id int64
	// HostPattern is the bare hostname (e.g. "example.com"); matching also covers subdomains.
	HostPattern string
	// ViewMode controls how requests to this host are handled.
	ViewMode ViewMode
	// CreatedAt is the UTC time the policy was created.
	CreatedAt time.Time
	// UpdatedAt is the UTC time the policy was last modified.
	UpdatedAt time.Time
}

// RequestLog represents a row in the RequestLogs table. Append-only audit log;
// one row per browse decision (allowed or denied).
type RequestLog struct {
	// Id is the auto-incrementing primary key.
	Id int64
	// Timestamp is the UTC time the request was evaluated.
	Timestamp time.Time
	// Url is the full request URL.
	Url string
	// Host is the extracted hostname from the URL.
	Host string
	// Decision is "deny" or the ViewMode name (e.g. "HtmlAllowInput"); stored as text
	// so the table is readable without joining to an enum.
	Decision string
	// Allowed indicates whether the request was permitted.
	Allowed bool
	// ClientIp is the best-effort caller IP; empty string when not determinable.
	ClientIp string
}

// RootCertificateAuthority represents a row in the RootCertificateAuthorities table.
// Only one row is meaningful at a time — uploading a new CA replaces any existing row.
type RootCertificateAuthority struct {
	// Id is the auto-incrementing primary key.
	Id int64
	// Subject is the CA certificate's distinguished name string.
	Subject string
	// NotBefore is the CA certificate's validity start (UTC).
	NotBefore time.Time
	// NotAfter is the CA certificate's validity end (UTC).
	NotAfter time.Time
	// Thumbprint is the hex-encoded SHA-1 fingerprint of the CA certificate.
	Thumbprint string
	// UploadedAt is the UTC time the CA was uploaded.
	UploadedAt time.Time
	// PfxBytes is the raw PFX (cert + private key) as uploaded; never returned via any GET endpoint.
	PfxBytes []byte
	// PfxPassword is the passphrase protecting PfxBytes; stored so the CA can be reloaded after restart.
	PfxPassword string
}

// VideoEncoderSetting represents the single row in the VideoEncoderSettings table.
// Id is always 1 (INSERT OR REPLACE with explicit Id=1 enforces the single-row invariant).
type VideoEncoderSetting struct {
	// Id is always 1; not auto-incremented — explicit to enforce single-row convention.
	Id int64
	// Mode is the configured encoder path (Auto/Cpu/Gpu).
	Mode VideoEncoderMode
	// UpdatedAt is the UTC time the setting was last written.
	UpdatedAt time.Time
}

// LogLevelSetting represents the single row in the LogLevelSettings table.
// Id is always 1, mirroring the VideoEncoderSetting single-row convention.
type LogLevelSetting struct {
	// Id is always 1; not auto-incremented — explicit to enforce single-row convention.
	Id int64
	// Level is the minimum log level as a string (e.g. "Information", "Debug", "Warning", "Error").
	// Stored as TEXT so it maps directly to slog/config values without a numeric enum translation.
	Level string
	// UpdatedAt is the UTC time the setting was last written.
	UpdatedAt time.Time
}
