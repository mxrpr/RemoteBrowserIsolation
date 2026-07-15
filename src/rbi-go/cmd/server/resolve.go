package main

import (
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"

	"rbi-go/internal/policy"
)

// handlePolicyResolve handles GET /api/policy/resolve?url= (public, no JWT required).
// It resolves the query-param URL against the site-policy table and returns:
//   - 400 if the url param is missing or is not an absolute URL.
//   - 403 + {"error": "This site is not permitted by policy."} if no policy matches.
//   - 200 + {"mode": "<ViewModeName>"} when a policy permits the host.
//
// Both outcomes write a RequestLog row for the audit trail, mirroring C#
// PolicyEndpoints.MapPolicyEndpoints / RequestLogService.LogAsync.
func handlePolicyResolve(eng *policy.Engine) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		rawURL := strings.TrimSpace(r.URL.Query().Get("url"))
		if rawURL == "" {
			writeJSON(w, http.StatusBadRequest, errorResponse{
				Error: "A valid absolute url query parameter is required.",
			})
			return
		}

		targetURL, err := url.ParseRequestURI(rawURL)
		if err != nil || !targetURL.IsAbs() {
			writeJSON(w, http.StatusBadRequest, errorResponse{
				Error: "A valid absolute url query parameter is required.",
			})
			return
		}

		host := targetURL.Hostname()

		// Best-effort client IP: strip the port from RemoteAddr.
		clientIP := r.RemoteAddr
		if h, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
			clientIP = h
		}

		mode, err := eng.Resolve(host)
		if err != nil {
			slog.Error("policy resolve: engine error", "err", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}

		if mode == nil {
			// No policy — deny. Write audit row then return 403.
			if logErr := policy.WriteRequestLog(eng.SQLDB(), rawURL, host, "deny", false, clientIP); logErr != nil {
				slog.Error("policy resolve: write deny log", "err", logErr)
			}
			writeJSON(w, http.StatusForbidden, errorResponse{
				Error: "This site is not permitted by policy.",
			})
			return
		}

		// Policy match — write audit row then return the mode name as a string.
		modeName := mode.String()
		if logErr := policy.WriteRequestLog(eng.SQLDB(), rawURL, host, modeName, true, clientIP); logErr != nil {
			slog.Error("policy resolve: write allow log", "err", logErr)
		}
		writeJSON(w, http.StatusOK, struct {
			Mode string `json:"mode"`
		}{Mode: modeName})
	}
}
