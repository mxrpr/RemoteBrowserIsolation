// Package rootca manages the single admin-uploaded root CA certificate used by the
// TLS-intercepting proxy to mint leaf certificates (see minter.go). It provides
// persistent storage in the RootCertificateAuthorities SQLite table and an
// in-memory cache so every proxy connection can read the active CA without a DB
// round-trip. Mirrors the C# IRootCaStore / RootCaStore singleton semantics.
package rootca

import (
	"crypto"
	"crypto/sha1"
	"crypto/x509"
	"database/sql"
	"encoding/hex"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"software.sslmate.com/src/go-pkcs12"
)

// CachedCA holds the parsed certificate and private key for the active root CA.
// Both fields are populated when returned by Store.GetActiveCA; nil means no CA
// is configured.
type CachedCA struct {
	// Cert is the parsed X.509 certificate for the root CA.
	Cert *x509.Certificate
	// Key is the private key embedded in the uploaded PFX. Type-assert to
	// *rsa.PrivateKey (or *ecdsa.PrivateKey) as needed by the minter.
	Key crypto.PrivateKey
}

// RootCaRow holds the metadata columns for a RootCertificateAuthorities row.
// PfxBytes and PfxPassword are intentionally absent — they must never be
// serialised to JSON or exposed outside the store.
type RootCaRow struct {
	Id         int64
	Subject    string
	NotBefore  time.Time
	NotAfter   time.Time
	Thumbprint string
	UploadedAt time.Time
}

// Store persists the single active root CA in the RootCertificateAuthorities table
// and keeps a parsed cert+key in memory for fast access by the LeafMinter. Safe for
// concurrent use. Mirrors C# RootCaStore: lazily loaded on first use, explicitly
// invalidated on upload/delete.
type Store struct {
	sqlDB  *sql.DB
	mu     sync.Mutex
	cached *CachedCA
	loaded bool // true once the first DB load has completed (even if the result is nil)
}

// NewStore constructs a Store backed by the given *sql.DB.
func NewStore(sqlDB *sql.DB) *Store {
	return &Store{sqlDB: sqlDB}
}

// GetActiveCA returns the in-memory cached CA, loading from the DB on the first call
// or after Invalidate. Returns nil if no CA row exists in the DB.
func (s *Store) GetActiveCA() (*CachedCA, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.loaded {
		return s.cached, nil
	}

	ca, err := s.loadFromDB()
	if err != nil {
		return nil, fmt.Errorf("rootca store: load from db: %w", err)
	}
	s.cached = ca
	s.loaded = true
	return ca, nil
}

// GetMetadata returns the metadata for the most-recent CA row without parsing the PFX
// into memory. Returns nil, nil if no CA has been uploaded yet.
func (s *Store) GetMetadata() (*RootCaRow, error) {
	var row RootCaRow
	var notBefore, notAfter, uploadedAt string
	err := s.sqlDB.QueryRow(
		`SELECT Id, Subject, NotBefore, NotAfter, Thumbprint, UploadedAt
		   FROM RootCertificateAuthorities
		  ORDER BY Id DESC LIMIT 1`,
	).Scan(&row.Id, &row.Subject, &notBefore, &notAfter, &row.Thumbprint, &uploadedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("rootca store: query metadata: %w", err)
	}
	var parseErr error
	row.NotBefore, parseErr = parseTimestamp(notBefore)
	if parseErr != nil {
		return nil, fmt.Errorf("rootca store: parse NotBefore %q: %w", notBefore, parseErr)
	}
	row.NotAfter, parseErr = parseTimestamp(notAfter)
	if parseErr != nil {
		return nil, fmt.Errorf("rootca store: parse NotAfter %q: %w", notAfter, parseErr)
	}
	row.UploadedAt, parseErr = parseTimestamp(uploadedAt)
	if parseErr != nil {
		return nil, fmt.Errorf("rootca store: parse UploadedAt %q: %w", uploadedAt, parseErr)
	}
	return &row, nil
}

// GetCertDER returns the raw DER bytes of the CA's public certificate, suitable for
// direct download. Returns nil, nil when no CA is configured.
func (s *Store) GetCertDER() ([]byte, error) {
	ca, err := s.GetActiveCA()
	if err != nil {
		return nil, err
	}
	if ca == nil {
		return nil, nil
	}
	// cert.Raw holds the ASN.1 DER bytes exactly as parsed — no re-encoding needed.
	return ca.Cert.Raw, nil
}

// Save replaces any existing CA row with a new one built from pfxBytes, password, and
// the already-parsed cert (avoids re-parsing the bytes a second time). Invalidates the
// in-memory cache so the next GetActiveCA picks up the new CA. Returns the saved row's
// metadata for the HTTP response.
func (s *Store) Save(pfxBytes []byte, password string, cert *x509.Certificate) (*RootCaRow, error) {
	now := time.Now().UTC()
	nowStr := now.Format(time.RFC3339Nano)
	thumbprint := computeThumbprint(cert)
	notBeforeStr := cert.NotBefore.UTC().Format(time.RFC3339Nano)
	notAfterStr := cert.NotAfter.UTC().Format(time.RFC3339Nano)
	subject := cert.Subject.String()

	tx, err := s.sqlDB.Begin()
	if err != nil {
		return nil, fmt.Errorf("rootca store: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	// Single active row: delete before inserting to mirror C#'s ExecuteDeleteAsync.
	if _, err := tx.Exec(`DELETE FROM RootCertificateAuthorities`); err != nil {
		return nil, fmt.Errorf("rootca store: delete old CA: %w", err)
	}

	res, err := tx.Exec(
		`INSERT INTO RootCertificateAuthorities
			(Subject, NotBefore, NotAfter, Thumbprint, UploadedAt, PfxBytes, PfxPassword)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		subject, notBeforeStr, notAfterStr, thumbprint, nowStr, pfxBytes, password,
	)
	if err != nil {
		return nil, fmt.Errorf("rootca store: insert CA: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("rootca store: commit: %w", err)
	}

	id, _ := res.LastInsertId()

	// Invalidate cache so the next GetActiveCA reloads the newly stored PFX.
	s.mu.Lock()
	s.cached = nil
	s.loaded = false
	s.mu.Unlock()

	slog.Info("Root CA saved", "subject", subject, "thumbprint", thumbprint)
	return &RootCaRow{
		Id:         id,
		Subject:    subject,
		NotBefore:  cert.NotBefore.UTC(),
		NotAfter:   cert.NotAfter.UTC(),
		Thumbprint: thumbprint,
		UploadedAt: now,
	}, nil
}

// Delete removes all CA rows from the DB and clears the in-memory cache.
// Mirrors C# AdminRootCaEndpoints DELETE path (ExecuteDeleteAsync + Invalidate).
func (s *Store) Delete() error {
	if _, err := s.sqlDB.Exec(`DELETE FROM RootCertificateAuthorities`); err != nil {
		return fmt.Errorf("rootca store: delete: %w", err)
	}
	s.mu.Lock()
	s.cached = nil
	s.loaded = false
	s.mu.Unlock()
	slog.Info("Root CA deleted")
	return nil
}

// Invalidate clears the in-memory cache so the next GetActiveCA reloads from the DB.
// Useful when the DB was modified outside the store (e.g. in tests or migrations).
func (s *Store) Invalidate() {
	s.mu.Lock()
	s.cached = nil
	s.loaded = false
	s.mu.Unlock()
}

// ParsePKCS12 decodes a PKCS#12 / PFX blob with the given password, returning the
// leaf certificate and private key. Returns an error if the bytes are not a valid
// PFX or the password is wrong. Note: the underlying pkcs12.Decode requires that the
// PFX contain exactly one cert bag and one key bag; a public-cert-only PFX will fail.
func ParsePKCS12(pfxBytes []byte, password string) (*x509.Certificate, crypto.PrivateKey, error) {
	key, cert, err := pkcs12.Decode(pfxBytes, password)
	if err != nil {
		return nil, nil, fmt.Errorf("pkcs12 decode: %w", err)
	}
	return cert, key, nil
}

// loadFromDB reads the most-recent PFX from the DB and parses it. Returns nil when no
// row exists. Must be called with s.mu held.
func (s *Store) loadFromDB() (*CachedCA, error) {
	var pfxBytes []byte
	var password string
	err := s.sqlDB.QueryRow(
		`SELECT PfxBytes, PfxPassword FROM RootCertificateAuthorities ORDER BY Id DESC LIMIT 1`,
	).Scan(&pfxBytes, &password)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("query: %w", err)
	}
	cert, key, err := ParsePKCS12(pfxBytes, password)
	if err != nil {
		return nil, fmt.Errorf("parse stored pfx: %w", err)
	}
	return &CachedCA{Cert: cert, Key: key}, nil
}

// computeThumbprint returns the SHA-1 thumbprint of the certificate's DER bytes as an
// uppercase hex string, matching the C# X509Certificate2.Thumbprint property format.
func computeThumbprint(cert *x509.Certificate) string {
	h := sha1.Sum(cert.Raw)
	return strings.ToUpper(hex.EncodeToString(h[:]))
}

// parseTimestamp parses a timestamp string written by the Go layer (RFC3339Nano or
// RFC3339) and returns the result in UTC.
func parseTimestamp(s string) (time.Time, error) {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		t, err = time.Parse(time.RFC3339, s)
	}
	return t.UTC(), err
}
