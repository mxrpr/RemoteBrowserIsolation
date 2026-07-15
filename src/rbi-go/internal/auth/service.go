// Package auth implements admin authentication for the rbi-go server.
// It provides bootstrap-or-login semantics (first login creates the sole admin
// account), bcrypt password hashing, HS256 JWT issuance, and token validation.
package auth

import (
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"

	"rbi-go/internal/config"
	"rbi-go/internal/db"
)

// AdminAuthService implements the bootstrap-or-login flow:
// if no admin row exists the first login call creates it (first caller wins);
// subsequent calls verify the stored credentials. At most one admin row ever exists.
type AdminAuthService struct {
	// db is the SQLite data layer; only AdminUsers table is accessed here.
	db *db.DB
	// jwtCfg holds the signing key, issuer, audience, and TTL for issued tokens.
	jwtCfg *config.JwtConfig

	// bootstrapMu serialises the check-then-create bootstrap sequence so that two
	// simultaneous "first" login calls cannot both observe "no admin exists" and race
	// to insert. A process-wide mutex is sufficient for this single-operator deployment.
	bootstrapMu sync.Mutex
}

// NewAdminAuthService constructs an AdminAuthService backed by the given database
// and JWT configuration.
func NewAdminAuthService(database *db.DB, jwtCfg *config.JwtConfig) *AdminAuthService {
	return &AdminAuthService{
		db:     database,
		jwtCfg: jwtCfg,
	}
}

// IsBootstrapped returns true if at least one AdminUser row exists in the database,
// indicating the admin account has been created. The admin UI uses this to decide
// whether to show a "create admin" bootstrap form or a normal login form.
func (s *AdminAuthService) IsBootstrapped() (bool, error) {
	var count int
	err := s.db.Unwrap().QueryRow(`SELECT COUNT(*) FROM AdminUsers`).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("auth: IsBootstrapped query: %w", err)
	}
	return count > 0, nil
}

// LoginOrBootstrap is the combined bootstrap-or-login entry point:
//   - If no admin exists: creates one with email+password and returns a signed JWT.
//   - If an admin exists and email matches (case-insensitive): verifies the password
//     and returns a signed JWT on success.
//   - If an admin exists and the email doesn't match: returns ("", nil) — the caller
//     maps this to 401 without distinguishing which check failed (matches C# semantics).
//   - Returns ("", nil) on wrong password.
//   - Returns ("", err) on infrastructure errors (DB, hashing, token issuance).
func (s *AdminAuthService) LoginOrBootstrap(email, password string) (string, error) {
	s.bootstrapMu.Lock()
	defer s.bootstrapMu.Unlock()

	// Query the one-and-only admin row (if any). Id is not needed beyond this point
	// (issueToken uses only the email), so it is scanned into a blank identifier.
	var ignoredID int64
	var storedEmail, storedHash string
	err := s.db.Unwrap().QueryRow(
		`SELECT Id, Email, PasswordHash FROM AdminUsers LIMIT 1`,
	).Scan(&ignoredID, &storedEmail, &storedHash)

	if errors.Is(err, sql.ErrNoRows) {
		// No admin exists yet — bootstrap: create the account and issue a token.
		hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
		if err != nil {
			return "", fmt.Errorf("auth: hash password: %w", err)
		}

		createdAt := time.Now().UTC().Format(time.RFC3339Nano)
		if _, err := s.db.Unwrap().Exec(
			`INSERT INTO AdminUsers (Email, PasswordHash, CreatedAt) VALUES (?, ?, ?)`,
			email, string(hash), createdAt,
		); err != nil {
			return "", fmt.Errorf("auth: insert admin: %w", err)
		}

		slog.Info("Bootstrapped admin account", "email", email)

		token, err := s.issueToken(email)
		if err != nil {
			return "", err
		}
		return token, nil
	}

	if err != nil {
		return "", fmt.Errorf("auth: query admin: %w", err)
	}

	// Admin exists — check that the submitted email matches (case-insensitive).
	if !strings.EqualFold(storedEmail, email) {
		// Wrong email: signal failure without distinguishing from wrong password.
		return "", nil
	}

	// Verify the password against the stored bcrypt hash.
	if err := bcrypt.CompareHashAndPassword([]byte(storedHash), []byte(password)); err != nil {
		// bcrypt.ErrMismatchedHashAndPassword is the expected wrong-password error;
		// any other error is also treated as a failed login (not an infra error).
		return "", nil //nolint:nilerr
	}

	token, err := s.issueToken(storedEmail)
	if err != nil {
		return "", err
	}
	return token, nil
}

// issueToken builds and signs an HS256 JWT for the given admin email, using the key,
// issuer, audience, and TTL from the JWT configuration. The token carries sub
// and email claims matching the C# AdminAuthService.IssueToken output.
func (s *AdminAuthService) issueToken(email string) (string, error) {
	key := []byte(s.jwtCfg.Key)
	if len(key) == 0 {
		return "", fmt.Errorf("auth: Jwt:Key is not configured")
	}

	ttl := s.jwtCfg.TtlMinutes
	if ttl <= 0 {
		ttl = 60
	}

	now := time.Now().UTC()
	claims := jwt.MapClaims{
		"sub":   email,
		"email": email,
		"iss":   s.jwtCfg.Issuer,
		"aud":   []string{s.jwtCfg.Audience},
		"iat":   now.Unix(),
		"exp":   now.Add(time.Duration(ttl) * time.Minute).Unix(),
	}

	t := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := t.SignedString(key)
	if err != nil {
		return "", fmt.Errorf("auth: sign token: %w", err)
	}
	return signed, nil
}

// ValidateToken parses and validates a raw JWT string signed with the configured
// HS256 key. Returns the parsed token (with claims) on success, or an error if the
// token is missing, malformed, expired, or signed with a different key.
// Used by the JWT bearer middleware to authenticate requests to protected endpoints.
func (s *AdminAuthService) ValidateToken(raw string) (*jwt.Token, error) {
	key := []byte(s.jwtCfg.Key)

	token, err := jwt.Parse(raw, func(t *jwt.Token) (interface{}, error) {
		// Reject any algorithm other than HS256.
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("auth: unexpected signing method: %v", t.Header["alg"])
		}
		return key, nil
	},
		jwt.WithIssuer(s.jwtCfg.Issuer),
		jwt.WithAudience(s.jwtCfg.Audience),
		jwt.WithExpirationRequired(),
	)
	if err != nil {
		return nil, err
	}
	if !token.Valid {
		return nil, fmt.Errorf("auth: token not valid")
	}
	return token, nil
}
