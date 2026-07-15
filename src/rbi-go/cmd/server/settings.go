package main

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"rbi-go/internal/auth"
	"rbi-go/internal/db"
	"rbi-go/internal/settings"
)

// videoEncoderSettingResponse is the JSON body returned by GET and PUT
// /api/admin/settings/video-encoder. Field names match the C#
// VideoEncoderSettingResponse record serialised with camelCase:
//
//	{"mode":"Auto","detectedGpu":{"available":false,"description":"..."}}
type videoEncoderSettingResponse struct {
	Mode        db.VideoEncoderMode `json:"mode"`
	DetectedGpu gpuProbeResponse    `json:"detectedGpu"`
}

// gpuProbeResponse is the nested GPU status in videoEncoderSettingResponse.
// Field names match the C# GpuProbeResponse record serialised with camelCase:
//
//	{"available":false,"description":"..."}
type gpuProbeResponse struct {
	Available   bool   `json:"available"`
	Description string `json:"description"`
}

// videoEncoderSettingRequest is the JSON body accepted by PUT
// /api/admin/settings/video-encoder. Field name matches the C#
// VideoEncoderSettingRequest record serialised with camelCase:
//
//	{"mode":"Cpu"}
type videoEncoderSettingRequest struct {
	Mode db.VideoEncoderMode `json:"mode"`
}

// logLevelSettingResponse is the JSON body returned by GET and PUT
// /api/admin/settings/log-level. Field name matches the C#
// LogLevelSettingResponse record serialised with camelCase:
//
//	{"level":"Information"}
type logLevelSettingResponse struct {
	Level settings.LogLevelName `json:"level"`
}

// logLevelSettingRequest is the JSON body accepted by PUT
// /api/admin/settings/log-level. Field name matches the C#
// LogLevelSettingRequest record serialised with camelCase:
//
//	{"level":"Debug"}
type logLevelSettingRequest struct {
	Level settings.LogLevelName `json:"level"`
}

// buildVideoEncoderResponse combines the current mode with a fresh GPU probe into
// the wire response shape, mirroring AdminVideoEncoderSettingsEndpoints.ToResponseAsync.
func buildVideoEncoderResponse(mode db.VideoEncoderMode) videoEncoderSettingResponse {
	probed := settings.ProbeGpu()
	return videoEncoderSettingResponse{
		Mode: mode,
		DetectedGpu: gpuProbeResponse{
			Available:   probed.Available,
			Description: probed.Description,
		},
	}
}

// handleGetVideoEncoderSettings handles GET /api/admin/settings/video-encoder
// (JWT-protected). Returns the current encoder mode alongside live GPU probe status.
func handleGetVideoEncoderSettings(store *settings.VideoEncoderStore, authSvc *auth.AdminAuthService) http.HandlerFunc {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mode, err := store.GetMode()
		if err != nil {
			slog.Error("get video encoder settings: get mode", "err", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, buildVideoEncoderResponse(mode))
	})
	return auth.RequireJWT(authSvc, inner).ServeHTTP
}

// handlePutVideoEncoderSettings handles PUT /api/admin/settings/video-encoder
// (JWT-protected). Persists the new encoder mode and returns the updated setting
// alongside live GPU probe status. Mirrors AdminVideoEncoderSettingsEndpoints.MapPut.
//
// Note on "fail loudly" semantics: the C# endpoint accepts Gpu mode even when no
// hardware is detected (the PUT always succeeds); the loud failure is deferred to
// when a video session actually attempts to encode. The Go implementation preserves
// that behavior — GPU unavailability is surfaced via detectedGpu.available=false in
// the response, not as a 4xx error on the PUT itself.
func handlePutVideoEncoderSettings(store *settings.VideoEncoderStore, authSvc *auth.AdminAuthService) http.HandlerFunc {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req videoEncoderSettingRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid JSON body"})
			return
		}

		if err := store.SetMode(req.Mode); err != nil {
			slog.Error("put video encoder settings: set mode", "err", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, buildVideoEncoderResponse(req.Mode))
	})
	return auth.RequireJWT(authSvc, inner).ServeHTTP
}

// handleGetLogLevelSettings handles GET /api/admin/settings/log-level (JWT-protected).
// Returns the current minimum log level as its C# LogLevel enum member name string.
func handleGetLogLevelSettings(store *settings.LogLevelStore, authSvc *auth.AdminAuthService) http.HandlerFunc {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		level, err := store.GetLevel()
		if err != nil {
			slog.Error("get log level settings: get level", "err", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, logLevelSettingResponse{Level: level})
	})
	return auth.RequireJWT(authSvc, inner).ServeHTTP
}

// handlePutLogLevelSettings handles PUT /api/admin/settings/log-level (JWT-protected).
// Persists the new log level, mirrors it into the live slog.LevelVar immediately (no
// restart required), and returns the updated level. Mirrors
// AdminLogLevelSettingsEndpoints.MapPut + LogLevelState live-update behavior.
func handlePutLogLevelSettings(store *settings.LogLevelStore, authSvc *auth.AdminAuthService) http.HandlerFunc {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req logLevelSettingRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid JSON body"})
			return
		}

		if err := store.SetLevel(req.Level); err != nil {
			slog.Error("put log level settings: set level", "err", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, logLevelSettingResponse{Level: req.Level})
	})
	return auth.RequireJWT(authSvc, inner).ServeHTTP
}
