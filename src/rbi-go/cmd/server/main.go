// Package main is the entrypoint for the rbi-go HTTP server. It loads config,
// registers routes, wires middleware, and starts listening.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"rbi-go/internal/auth"
	"rbi-go/internal/browser"
	"rbi-go/internal/config"
	"rbi-go/internal/db"
	"rbi-go/internal/middleware"
	"rbi-go/internal/policy"
	"rbi-go/internal/proxy"
	"rbi-go/internal/rootca"
	"rbi-go/internal/settings"
	rtcmgr "rbi-go/internal/webrtc"
)

// healthResponse is the JSON body returned by GET /health.
type healthResponse struct {
	Status string `json:"status"`
}

// loginRequest is the JSON body expected by POST /api/admin/auth/login.
// Field names match the C# LoginRequest record serialised with camelCase (ASP.NET Core default).
type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

// loginResponse is the JSON body returned on successful login or bootstrap.
// Field name matches the C# LoginResponse record ("token" in camelCase).
type loginResponse struct {
	Token string `json:"token"`
}

// authStatusResponse is the JSON body returned by GET /api/admin/auth/status.
// Field name matches the C# AuthStatusResponse record ("bootstrapped" in camelCase).
type authStatusResponse struct {
	Bootstrapped bool `json:"bootstrapped"`
}

// errorResponse is a generic JSON error envelope used for 4xx responses.
type errorResponse struct {
	Error string `json:"error"`
}

// handleHealth responds with {"status":"ok"}, matching the C# Results.Ok(new { status = "ok" })
// response from Program.cs. Uses json.Marshal + w.Write (not Encoder.Encode) to avoid the
// trailing newline that Encoder appends, keeping the body byte-identical to the C# output.
func handleHealth(w http.ResponseWriter, r *http.Request) {
	body, err := json.Marshal(healthResponse{Status: "ok"})
	if err != nil {
		slog.Error("health: marshal response", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write(body); err != nil {
		slog.Error("health: write response", "err", err)
	}
}

// resolveWwwRoot returns an absolute path for the static-file directory. If the
// configured path is already absolute it is returned as-is. If it is relative,
// two candidate locations are tried in order:
//  1. Relative to the directory that contains the running executable — correct
//     for a compiled binary placed next to (or at a known offset from) wwwroot.
//  2. Relative to the OS working directory — correct for `go run ./cmd/server/`
//     invoked from src/rbi-go/, where the temp binary lives in /tmp/go-build*/exe/
//     and the relative path would otherwise resolve to the wrong place.
//
// The function returns an error if neither candidate is accessible, matching the
// C# UseStaticFiles behavior of failing loudly at startup rather than silently
// 404ing every request.
func resolveWwwRoot(cfgPath string) (string, error) {
	if filepath.IsAbs(cfgPath) {
		if _, err := os.Stat(cfgPath); err != nil {
			return "", fmt.Errorf("wwwroot %q not accessible: %w", cfgPath, err)
		}
		return cfgPath, nil
	}

	// Candidate 1: relative to the executable's directory.
	exe, err := os.Executable()
	if err == nil {
		// EvalSymlinks normalises /proc/self/exe and similar OS-level symlinks.
		if resolved, err := filepath.EvalSymlinks(exe); err == nil {
			exeRel := filepath.Join(filepath.Dir(resolved), cfgPath)
			if _, err := os.Stat(exeRel); err == nil {
				return exeRel, nil
			}
		}
	}

	// Candidate 2: relative to the working directory (covers `go run` in dev).
	cwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("get working directory: %w", err)
	}
	cwdRel := filepath.Join(cwd, cfgPath)
	if _, err := os.Stat(cwdRel); err != nil {
		return "", fmt.Errorf("wwwroot %q not accessible (tried relative to executable and %q): %w",
			cfgPath, cwd, err)
	}
	return cwdRel, nil
}

// writeJSON writes v as a JSON response with the given HTTP status code. On marshal
// failure it falls back to a 500 plain-text error so the client always gets a response.
func writeJSON(w http.ResponseWriter, status int, v any) {
	body, err := json.Marshal(v)
	if err != nil {
		slog.Error("writeJSON: marshal", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if _, err := w.Write(body); err != nil {
		slog.Error("writeJSON: write", "err", err)
	}
}

// handleAdminLogin handles POST /api/admin/auth/login. It accepts a JSON body with
// "email" and "password" fields. On the first call it bootstraps the sole admin
// account; on subsequent calls it verifies credentials. Returns 200 + {"token":"..."}
// on success, 400 for missing fields, or 401 for wrong credentials.
func handleAdminLogin(authSvc *auth.AdminAuthService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req loginRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid JSON body"})
			return
		}

		// Validate required fields — mirrors the C# endpoint guard.
		if strings.TrimSpace(req.Email) == "" || strings.TrimSpace(req.Password) == "" {
			writeJSON(w, http.StatusBadRequest, errorResponse{Error: "Email and password are required."})
			return
		}

		token, err := authSvc.LoginOrBootstrap(req.Email, req.Password)
		if err != nil {
			slog.Error("admin login: internal error", "err", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		if token == "" {
			// Wrong email or wrong password — return 401 without detail.
			w.WriteHeader(http.StatusUnauthorized)
			return
		}

		writeJSON(w, http.StatusOK, loginResponse{Token: token})
	}
}

// handleAdminAuthStatus handles GET /api/admin/auth/status. Returns
// {"bootstrapped": false} before any admin account exists, and {"bootstrapped": true}
// after the first login call creates it. No authentication required — this is the
// probe the UI calls to decide which login form to show.
func handleAdminAuthStatus(authSvc *auth.AdminAuthService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ok, err := authSvc.IsBootstrapped()
		if err != nil {
			slog.Error("admin auth status: internal error", "err", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, authStatusResponse{Bootstrapped: ok})
	}
}

// buildRouter constructs and returns the ServeMux with all routes registered.
// Uses Go 1.22+ method+path pattern syntax (e.g. "GET /health").
// authSvc is used to register auth endpoints and for the JWT middleware protecting
// admin routes. eng is the in-memory policy resolver used by site CRUD and the
// public resolve endpoint. videoStore and logStore are the singleton settings stores
// for the /api/admin/settings/* endpoints. caStore and caMinter are the singletons
// for /api/admin/rootca/* endpoints and TLS leaf cert minting.
// webrtcMgr and browserMgr are the singletons for POST /api/session/offer (Part 11).
func buildRouter(
	staticDir string,
	authSvc *auth.AdminAuthService,
	eng *policy.Engine,
	videoStore *settings.VideoEncoderStore,
	logStore *settings.LogLevelStore,
	caStore *rootca.Store,
	caMinter *rootca.Minter,
	webrtcMgr *rtcmgr.Manager,
	browserMgr *browser.Manager,
) *http.ServeMux {
	mux := http.NewServeMux()

	// GET /health — liveness probe, no auth required.
	mux.HandleFunc("GET /health", handleHealth)

	// Admin auth endpoints — not protected by JWT (they are the token entry points).
	// POST /api/admin/auth/login — bootstrap-or-login, returns a bearer JWT.
	mux.HandleFunc("POST /api/admin/auth/login", handleAdminLogin(authSvc))
	// GET /api/admin/auth/status — probe whether the admin account exists.
	mux.HandleFunc("GET /api/admin/auth/status", handleAdminAuthStatus(authSvc))

	// Site policy CRUD — all routes require a valid bearer JWT.
	// GET /api/admin/sites — list all policies, ordered by HostPattern.
	mux.HandleFunc("GET /api/admin/sites", handleListSites(eng, authSvc))
	// POST /api/admin/sites — create a new policy; 409 if HostPattern already exists.
	mux.HandleFunc("POST /api/admin/sites", handleCreateSite(eng, authSvc))
	// PUT /api/admin/sites/{id} — update an existing policy by id; 404 if not found.
	mux.HandleFunc("PUT /api/admin/sites/{id}", handleUpdateSite(eng, authSvc))
	// DELETE /api/admin/sites/{id} — remove a policy by id; 404 if not found.
	mux.HandleFunc("DELETE /api/admin/sites/{id}", handleDeleteSite(eng, authSvc))

	// Public policy resolution — no JWT required.
	// GET /api/policy/resolve?url= — returns the ViewMode or 403 for unmatched hosts.
	mux.HandleFunc("GET /api/policy/resolve", handlePolicyResolve(eng))

	// Admin audit log — requires a valid bearer JWT.
	// GET /api/admin/logs?limit=&offset= — paginated request log, newest first.
	mux.HandleFunc("GET /api/admin/logs", handleAdminLogs(eng, authSvc))

	// Settings endpoints — both require a valid bearer JWT.
	// GET /api/admin/settings/video-encoder — read current encoder mode + GPU probe.
	mux.HandleFunc("GET /api/admin/settings/video-encoder", handleGetVideoEncoderSettings(videoStore, authSvc))
	// PUT /api/admin/settings/video-encoder — update encoder mode; returns same shape as GET.
	mux.HandleFunc("PUT /api/admin/settings/video-encoder", handlePutVideoEncoderSettings(videoStore, authSvc))
	// GET /api/admin/settings/log-level — read current minimum log level.
	mux.HandleFunc("GET /api/admin/settings/log-level", handleGetLogLevelSettings(logStore, authSvc))
	// PUT /api/admin/settings/log-level — update log level; applies immediately without restart.
	mux.HandleFunc("PUT /api/admin/settings/log-level", handlePutLogLevelSettings(logStore, authSvc))

	// Root CA management — all routes require a valid bearer JWT.
	// GET /api/admin/rootca — return current CA metadata (subject, validity, thumbprint).
	mux.HandleFunc("GET /api/admin/rootca", handleGetRootCa(caStore, authSvc))
	// POST /api/admin/rootca — upload a PFX; validates CA constraints, replaces any existing CA.
	mux.HandleFunc("POST /api/admin/rootca", handlePostRootCa(caStore, caMinter, authSvc))
	// DELETE /api/admin/rootca — remove the CA and clear all caches.
	mux.HandleFunc("DELETE /api/admin/rootca", handleDeleteRootCa(caStore, caMinter, authSvc))
	// GET /api/admin/rootca/certificate — download the CA's public cert in DER format.
	mux.HandleFunc("GET /api/admin/rootca/certificate", handleGetRootCaCertificate(caStore, authSvc))

	// WebRTC video session — POST /api/session/offer.
	// Accepts the browser's SDP offer, re-resolves policy, negotiates the answer,
	// and wires the screencast + input pipeline once the connection is established.
	mux.HandleFunc("POST /api/session/offer", handleSessionOffer(eng, webrtcMgr, browserMgr))

	// Static file serving for wwwroot/ — mirrors UseDefaultFiles() + UseStaticFiles()
	// in the C# Program.cs: index.html is served for GET /, and every file under
	// wwwroot/ is served at its path-relative URL.
	fs := http.FileServer(http.Dir(staticDir))
	mux.Handle("/", fs)

	return mux
}

// run is the real main logic, separated from main() so that deferred cleanup
// runs before os.Exit and to make the startup flow easy to follow.
func run() error {
	// Load config from file + env overrides.
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	// Configure structured logging via a slog.LevelVar so the admin can change the
	// log level at runtime (via PUT /api/admin/settings/log-level) without a restart.
	// The initial level comes from config; the LogLevelStore may override it once the
	// DB-persisted level is loaded (see the store initialisation below).
	var logLevelVar slog.LevelVar
	logLevelVar.Set(settings.LogLevelNameFromConfig(cfg.Logging.LogLevel.Default).ToSlogLevel())
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: &logLevelVar}))
	slog.SetDefault(logger)

	// Open the SQLite database and ensure all six tables exist. Fails loudly so a
	// misconfigured DB path is surfaced at startup rather than at first request.
	database, err := db.Connect(cfg.ConnectionStrings.Sqlite)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer func() {
		if closeErr := database.Close(); closeErr != nil {
			slog.Error("database close", "err", closeErr)
		}
	}()
	slog.Info("Database ready", "connStr", cfg.ConnectionStrings.Sqlite)

	// Resolve the wwwroot path to an absolute location anchored to the executable,
	// and verify it exists before accepting any requests.
	wwwRoot, err := resolveWwwRoot(cfg.WwwRoot)
	if err != nil {
		return fmt.Errorf("static files: %w", err)
	}
	slog.Info("Serving static files", "path", wwwRoot)

	// Construct the admin auth service. It is used both for the /api/admin/auth/*
	// endpoints and to provide the JWT validator for the RequireJWT middleware that
	// protects admin endpoints.
	authSvc := auth.NewAdminAuthService(database, &cfg.Jwt)

	// Construct the in-memory policy engine. The cache is loaded lazily on the first
	// resolve call; CRUD mutations call Invalidate() to trigger a reload.
	eng := policy.NewEngine(database)

	// Construct settings stores. Both are singletons backed by the single-row DB tables.
	videoStore := settings.NewVideoEncoderStore(database.Unwrap())
	logStore := settings.NewLogLevelStore(database.Unwrap(), &logLevelVar)

	// Construct the root CA store and leaf cert minter. Both are singletons: the store
	// holds the in-memory parsed CA (lazy-loaded, invalidated on upload/delete) and the
	// minter keeps a per-hostname leaf cache that is cleared whenever the CA changes.
	caStore := rootca.NewStore(database.Unwrap())
	caMinter := rootca.NewMinter(caStore)

	// Load the persisted log level from the DB so it takes effect immediately on
	// startup, mirroring the C# Program.cs call to GetLevelAsync() before the app
	// starts serving. Errors here are non-fatal — we fall back to the config level.
	if _, err := logStore.GetLevel(); err != nil {
		slog.Warn("Could not load persisted log level from DB; using config default", "err", err)
	}

	// Construct the WebRTC session manager. It is a singleton (one Manager,
	// many concurrent sessions); each CreateSession call allocates independent
	// UDP sockets within the configured port range.
	webrtcMgr := rtcmgr.NewManager(&cfg.WebRtc)

	// Construct the headless-browser session manager. Validates the Chromium
	// binary path at construction time so a misconfigured path surfaces at
	// startup rather than at first video-mode request.
	browserMgr, err := browser.NewManager(&cfg.Browser)
	if err != nil {
		return fmt.Errorf("browser manager: %w", err)
	}
	defer browserMgr.Close()

	// Build router + wire per-request logging middleware.
	mux := buildRouter(wwwRoot, authSvc, eng, videoStore, logStore, caStore, caMinter, webrtcMgr, browserMgr)
	handler := middleware.RequestLogger(mux)

	// Bind the TCP listener first so we can log the actual address before
	// accepting connections — mirrors the C# ApplicationStarted callback.
	addr := fmt.Sprintf(":%d", cfg.Port)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("listen %s: %w", addr, err)
	}

	// Log the actual bound address (including OS-assigned port when Port=0).
	slog.Info("Accepting browser connections on", "address", fmt.Sprintf("http://%s", ln.Addr()))
	fmt.Println("Server started. Press Ctrl+C to shut down.")

	srv := &http.Server{Handler: handler}

	// Construct the TLS-intercepting proxy server. It runs as a separate TCP
	// listener alongside the HTTP server and shares the policy engine and cert
	// minter. httpServerAddr is passed so the proxy can (a) connect back to the
	// HTTP server for self-origin bypass, and (b) embed the correct URL in the
	// video-mode interstitial link.
	proxyServer := proxy.NewServer(&cfg.Proxy, ln.Addr().String(), eng, caMinter)

	// Graceful shutdown on SIGINT/SIGTERM. A cancellable context is used to
	// signal both the HTTP server (via srv.Shutdown) and the proxy listener
	// (via ctx cancellation closing the TCP accept loop).
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel() // ensure context is always cleaned up on run() return
	done := make(chan struct{})
	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
		<-sigCh

		slog.Info("Server is shutting down...")
		cancel() // signals the proxy listener to stop

		shutCtx, shutCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer shutCancel()
		if err := srv.Shutdown(shutCtx); err != nil {
			slog.Error("shutdown", "err", err)
		}
		close(done)
	}()

	// Start the proxy listener in a background goroutine, using the same
	// context that will be cancelled on shutdown. Errors from the proxy are
	// logged but do not terminate the HTTP server.
	go func() {
		if err := proxyServer.Run(ctx); err != nil {
			slog.Error("proxy server error", "err", err)
		}
	}()

	if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
		cancel() // clean up proxy goroutine on unexpected HTTP server exit
		return fmt.Errorf("serve: %w", err)
	}

	<-done
	return nil
}

// main is the program entrypoint. Delegates to run() and exits non-zero on error.
func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "rbi-go: %v\n", err)
		os.Exit(1)
	}
}
