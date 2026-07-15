package auth

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
)

// RequireJWT returns an HTTP middleware that enforces JWT bearer authentication.
// It extracts the token from the "Authorization: Bearer <token>" header, validates
// it using the given AdminAuthService, and either passes the request through to the
// next handler or returns 401 Unauthorized with a JSON error body.
//
// Apply this middleware to any route that requires an authenticated admin session
// (all /api/admin/* endpoints except /api/admin/auth/login and /api/admin/auth/status).
func RequireJWT(svc *AdminAuthService, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHeader := r.Header.Get("Authorization")
		if authHeader == "" {
			writeUnauthorized(w, "missing Authorization header")
			return
		}

		// Expect "Bearer <token>"; reject anything else.
		const prefix = "Bearer "
		if !strings.HasPrefix(authHeader, prefix) {
			writeUnauthorized(w, "Authorization header must use Bearer scheme")
			return
		}
		raw := strings.TrimPrefix(authHeader, prefix)
		if raw == "" {
			writeUnauthorized(w, "empty bearer token")
			return
		}

		if _, err := svc.ValidateToken(raw); err != nil {
			slog.Debug("JWT validation failed", "err", err)
			writeUnauthorized(w, "invalid or expired token")
			return
		}

		next.ServeHTTP(w, r)
	})
}

// writeUnauthorized writes a 401 response with a JSON body containing an "error" field.
// json.Marshal is used to ensure msg is properly escaped regardless of its content.
func writeUnauthorized(w http.ResponseWriter, msg string) {
	body, err := json.Marshal(struct {
		Error string `json:"error"`
	}{Error: msg})
	if err != nil {
		// Fallback: plain-text 401 if marshalling somehow fails (should never happen
		// for a simple string field, but be defensive rather than silent).
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	// Best-effort write; ignore error since the connection may already be closing.
	_, _ = w.Write(body)
}
