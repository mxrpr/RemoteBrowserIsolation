package main

import (
	"database/sql"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"rbi-go/internal/auth"
	"rbi-go/internal/policy"
)

// requestLogResponse is the JSON body for one row returned by GET /api/admin/logs.
// Field names match the C# RequestLogResponse record serialised with camelCase.
type requestLogResponse struct {
	Id        int64     `json:"id"`
	Timestamp time.Time `json:"timestamp"`
	Url       string    `json:"url"`
	Host      string    `json:"host"`
	Decision  string    `json:"decision"`
	Allowed   bool      `json:"allowed"`
	ClientIp  *string   `json:"clientIp"` // nullable — stored as NULL when not available
}

// handleAdminLogs handles GET /api/admin/logs?limit=&offset= (JWT-protected).
// Returns request-log rows newest-first with pagination. Mirrors C# AdminLogEndpoints:
// default limit 50, max 500, offset clamps to ≥ 0.
func handleAdminLogs(eng *policy.Engine, authSvc *auth.AdminAuthService) http.HandlerFunc {
	const defaultLimit = 50
	const maxLimit = 500

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		limit := parseQueryInt(r, "limit", defaultLimit)
		if limit < 1 {
			limit = 1
		}
		if limit > maxLimit {
			limit = maxLimit
		}

		offset := parseQueryInt(r, "offset", 0)
		if offset < 0 {
			offset = 0
		}

		sqlDB := eng.SQLDB()
		rows, err := sqlDB.Query(
			`SELECT Id, Timestamp, Url, Host, Decision, Allowed, ClientIp
			   FROM RequestLogs
			  ORDER BY Timestamp DESC
			  LIMIT ? OFFSET ?`,
			limit, offset,
		)
		if err != nil {
			slog.Error("admin logs: query", "err", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		defer rows.Close()

		var resp []requestLogResponse
		for rows.Next() {
			var entry requestLogResponse
			var tsStr string
			var allowedInt int
			var clientIp sql.NullString
			if err := rows.Scan(
				&entry.Id, &tsStr, &entry.Url, &entry.Host,
				&entry.Decision, &allowedInt, &clientIp,
			); err != nil {
				slog.Error("admin logs: scan", "err", err)
				http.Error(w, "internal error", http.StatusInternalServerError)
				return
			}
			entry.Timestamp, _ = time.Parse(time.RFC3339Nano, tsStr)
			entry.Allowed = allowedInt != 0
			if clientIp.Valid {
				v := clientIp.String
				entry.ClientIp = &v
			}
			resp = append(resp, entry)
		}
		if err := rows.Err(); err != nil {
			slog.Error("admin logs: iterate", "err", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}

		// Return an empty JSON array rather than null when there are no rows.
		if resp == nil {
			resp = []requestLogResponse{}
		}
		writeJSON(w, http.StatusOK, resp)
	})
	return auth.RequireJWT(authSvc, inner).ServeHTTP
}

// parseQueryInt reads a URL query parameter as an integer, returning defaultVal
// if the parameter is absent or cannot be parsed.
func parseQueryInt(r *http.Request, key string, defaultVal int) int {
	s := r.URL.Query().Get(key)
	if s == "" {
		return defaultVal
	}
	v, err := strconv.Atoi(s)
	if err != nil {
		return defaultVal
	}
	return v
}
