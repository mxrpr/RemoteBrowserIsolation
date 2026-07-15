package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"rbi-go/internal/auth"
	"rbi-go/internal/db"
	"rbi-go/internal/policy"
)

// sitePolicyRequest is the JSON body accepted by POST and PUT /api/admin/sites.
// Field names match the C# SitePolicyRequest record serialised with camelCase.
type sitePolicyRequest struct {
	HostPattern string      `json:"hostPattern"`
	ViewMode    db.ViewMode `json:"viewMode"`
}

// sitePolicyResponse is the JSON body returned by all site CRUD endpoints.
// Field names match the C# SitePolicyResponse record serialised with camelCase.
type sitePolicyResponse struct {
	Id          int64       `json:"id"`
	HostPattern string      `json:"hostPattern"`
	ViewMode    db.ViewMode `json:"viewMode"`
	CreatedAt   time.Time   `json:"createdAt"`
	UpdatedAt   time.Time   `json:"updatedAt"`
}

// extractHost normalises whatever an admin types — bare host, full URL, or host with
// a path — down to just the lowercase hostname. Mirrors C# AdminSiteEndpoints.ExtractHost:
// a schemeless input is prefixed with "https://" to make it parseable; falls back to the
// trimmed/lowercased raw input if url.Parse still cannot extract a host.
func extractHost(input string) string {
	trimmed := strings.TrimSpace(input)
	candidate := trimmed
	if !strings.Contains(trimmed, "://") {
		candidate = "https://" + trimmed
	}
	u, err := url.Parse(candidate)
	if err == nil && u.Hostname() != "" {
		// u.Hostname() strips the port (e.g. "example.com:8080" → "example.com"),
		// matching C# Uri.Host semantics. u.Host would include the port, causing
		// stored HostPatterns like "example.com:8080" that never match resolve-time
		// lookups (which always use Hostname()).
		return strings.ToLower(u.Hostname())
	}
	return strings.ToLower(trimmed)
}

// scanSiteRow reads (Id, HostPattern, ViewMode, CreatedAt, UpdatedAt) from any
// type that implements Scan, parses the TEXT-stored timestamps, and returns a
// sitePolicyResponse ready for JSON marshalling.
func scanSiteRow(s interface{ Scan(...interface{}) error }) (sitePolicyResponse, error) {
	var r sitePolicyResponse
	var createdAt, updatedAt string
	if err := s.Scan(&r.Id, &r.HostPattern, &r.ViewMode, &createdAt, &updatedAt); err != nil {
		return r, err
	}
	r.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdAt)
	r.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updatedAt)
	return r, nil
}

// querySiteByID fetches a single SitePolicy row by primary key and returns a
// sitePolicyResponse, or sql.ErrNoRows if no such row exists.
func querySiteByID(sqlDB *sql.DB, id int64) (sitePolicyResponse, error) {
	row := sqlDB.QueryRow(
		`SELECT Id, HostPattern, ViewMode, CreatedAt, UpdatedAt
		   FROM SitePolicies WHERE Id = ?`, id,
	)
	return scanSiteRow(row)
}

// handleListSites handles GET /api/admin/sites (JWT-protected). Returns all site
// policies ordered alphabetically by HostPattern, mirroring the C# handler.
func handleListSites(eng *policy.Engine, authSvc *auth.AdminAuthService) http.HandlerFunc {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sqlDB := eng.SQLDB()
		rows, err := sqlDB.Query(
			`SELECT Id, HostPattern, ViewMode, CreatedAt, UpdatedAt
			   FROM SitePolicies
			  ORDER BY HostPattern`,
		)
		if err != nil {
			slog.Error("list sites: query", "err", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		defer rows.Close()

		var resp []sitePolicyResponse
		for rows.Next() {
			item, err := scanSiteRow(rows)
			if err != nil {
				slog.Error("list sites: scan", "err", err)
				http.Error(w, "internal error", http.StatusInternalServerError)
				return
			}
			resp = append(resp, item)
		}
		if err := rows.Err(); err != nil {
			slog.Error("list sites: iterate", "err", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}

		// Return an empty JSON array rather than null when there are no rows.
		if resp == nil {
			resp = []sitePolicyResponse{}
		}
		writeJSON(w, http.StatusOK, resp)
	})
	return auth.RequireJWT(authSvc, inner).ServeHTTP
}

// handleCreateSite handles POST /api/admin/sites (JWT-protected). Normalises the
// host, checks for a duplicate (409 Conflict), inserts the row, and returns 201
// Created with a Location header and the new policy body.
func handleCreateSite(eng *policy.Engine, authSvc *auth.AdminAuthService) http.HandlerFunc {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req sitePolicyRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid JSON body"})
			return
		}
		if strings.TrimSpace(req.HostPattern) == "" {
			writeJSON(w, http.StatusBadRequest, errorResponse{Error: "hostPattern is required."})
			return
		}

		normalised := extractHost(req.HostPattern)
		sqlDB := eng.SQLDB()

		// Explicit existence check matches C# AnyAsync pattern and avoids parsing
		// SQLite constraint error messages.
		var count int
		if err := sqlDB.QueryRow(
			`SELECT COUNT(*) FROM SitePolicies WHERE HostPattern = ?`, normalised,
		).Scan(&count); err != nil {
			slog.Error("create site: exists check", "err", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		if count > 0 {
			writeJSON(w, http.StatusConflict, errorResponse{
				Error: fmt.Sprintf("A policy for '%s' already exists.", normalised),
			})
			return
		}

		now := time.Now().UTC().Format(time.RFC3339Nano)
		res, err := sqlDB.Exec(
			`INSERT INTO SitePolicies (HostPattern, ViewMode, CreatedAt, UpdatedAt)
			 VALUES (?, ?, ?, ?)`,
			normalised, int(req.ViewMode), now, now,
		)
		if err != nil {
			slog.Error("create site: insert", "err", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}

		id, _ := res.LastInsertId()
		eng.Invalidate()

		resp, err := querySiteByID(sqlDB, id)
		if err != nil {
			slog.Error("create site: read-back", "err", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Location", fmt.Sprintf("/api/admin/sites/%d", id))
		writeJSON(w, http.StatusCreated, resp)
	})
	return auth.RequireJWT(authSvc, inner).ServeHTTP
}

// handleUpdateSite handles PUT /api/admin/sites/{id} (JWT-protected). Looks up the
// row, updates HostPattern and ViewMode, and returns the updated policy. Returns 404
// if the id does not exist.
func handleUpdateSite(eng *policy.Engine, authSvc *auth.AdminAuthService) http.HandlerFunc {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		idStr := r.PathValue("id")
		id, err := strconv.ParseInt(idStr, 10, 64)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid id"})
			return
		}

		var req sitePolicyRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid JSON body"})
			return
		}
		if strings.TrimSpace(req.HostPattern) == "" {
			writeJSON(w, http.StatusBadRequest, errorResponse{Error: "hostPattern is required."})
			return
		}

		normalised := extractHost(req.HostPattern)
		sqlDB := eng.SQLDB()

		// Check existence before update — mirrors C# FindAsync returning null → NotFound.
		var existing int
		if err := sqlDB.QueryRow(
			`SELECT COUNT(*) FROM SitePolicies WHERE Id = ?`, id,
		).Scan(&existing); err != nil {
			slog.Error("update site: exists check", "err", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		if existing == 0 {
			w.WriteHeader(http.StatusNotFound)
			return
		}

		now := time.Now().UTC().Format(time.RFC3339Nano)
		if _, err := sqlDB.Exec(
			`UPDATE SitePolicies SET HostPattern = ?, ViewMode = ?, UpdatedAt = ? WHERE Id = ?`,
			normalised, int(req.ViewMode), now, id,
		); err != nil {
			slog.Error("update site: exec", "err", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}

		eng.Invalidate()

		resp, err := querySiteByID(sqlDB, id)
		if err != nil {
			slog.Error("update site: read-back", "err", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}

		writeJSON(w, http.StatusOK, resp)
	})
	return auth.RequireJWT(authSvc, inner).ServeHTTP
}

// handleDeleteSite handles DELETE /api/admin/sites/{id} (JWT-protected). Removes the
// row and returns 204 No Content, or 404 if the id does not exist.
func handleDeleteSite(eng *policy.Engine, authSvc *auth.AdminAuthService) http.HandlerFunc {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		idStr := r.PathValue("id")
		id, err := strconv.ParseInt(idStr, 10, 64)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid id"})
			return
		}

		sqlDB := eng.SQLDB()

		var existing int
		if err := sqlDB.QueryRow(
			`SELECT COUNT(*) FROM SitePolicies WHERE Id = ?`, id,
		).Scan(&existing); err != nil {
			slog.Error("delete site: exists check", "err", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		if existing == 0 {
			w.WriteHeader(http.StatusNotFound)
			return
		}

		if _, err := sqlDB.Exec(`DELETE FROM SitePolicies WHERE Id = ?`, id); err != nil {
			slog.Error("delete site: exec", "err", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}

		eng.Invalidate()
		w.WriteHeader(http.StatusNoContent)
	})
	return auth.RequireJWT(authSvc, inner).ServeHTTP
}
