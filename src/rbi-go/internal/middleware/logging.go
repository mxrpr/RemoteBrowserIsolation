// Package middleware provides HTTP middleware for the rbi-go server.
package middleware

import (
	"log/slog"
	"net/http"
	"time"
)

// RequestLogger returns an HTTP middleware that logs each incoming request at
// INFO level with the method, URL, and UTC timestamp — matching the intent of
// the C# middleware in Program.cs ("Received request {Method} {Url} at {Timestamp}").
func RequestLogger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Reconstruct the full request URL including scheme and host so the log
		// line is equivalent to ASP.NET Core's GetDisplayUrl().
		scheme := "http"
		if r.TLS != nil {
			scheme = "https"
		}
		fullURL := scheme + "://" + r.Host + r.RequestURI

		slog.Info("Received request",
			"method", r.Method,
			"url", fullURL,
			"timestamp", time.Now().UTC().Format(time.RFC3339Nano),
		)

		next.ServeHTTP(w, r)
	})
}
