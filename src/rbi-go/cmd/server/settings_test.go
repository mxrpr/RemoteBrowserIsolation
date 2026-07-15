package main

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"rbi-go/internal/db"
	"rbi-go/internal/settings"
)

// === handleGetVideoEncoderSettings Tests ===

// TestHandleGetVideoEncoderSettings_Returns200WithDefaultMode verifies that GET /api/admin/settings/video-encoder
// returns HTTP 200 with the default Auto mode.
func TestHandleGetVideoEncoderSettings_Returns200WithDefaultMode(t *testing.T) {
	vs, _ := newTestStores(t)
	authSvc := newTestAuthSvc(t)
	token := loginAndGetToken(t, authSvc)

	handler := handleGetVideoEncoderSettings(vs, authSvc)
	req := httptest.NewRequest("GET", "/api/admin/settings/video-encoder", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}

	body, err := io.ReadAll(w.Body)
	if err != nil {
		t.Fatalf("failed to read response: %v", err)
	}

	var resp videoEncoderSettingResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if resp.Mode != db.VideoEncoderModeAuto {
		t.Errorf("expected mode Auto, got %v", resp.Mode)
	}
}

// TestHandleGetVideoEncoderSettings_ResponseIncludesDetectedGpu verifies that the response includes GPU probe status.
func TestHandleGetVideoEncoderSettings_ResponseIncludesDetectedGpu(t *testing.T) {
	vs, _ := newTestStores(t)
	authSvc := newTestAuthSvc(t)
	token := loginAndGetToken(t, authSvc)

	handler := handleGetVideoEncoderSettings(vs, authSvc)
	req := httptest.NewRequest("GET", "/api/admin/settings/video-encoder", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}

	body, err := io.ReadAll(w.Body)
	if err != nil {
		t.Fatalf("failed to read response: %v", err)
	}

	var resp videoEncoderSettingResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	// DetectedGpu should be present in the response
	if resp.DetectedGpu.Description == "" {
		t.Error("expected DetectedGpu.Description to be non-empty")
	}
}

// TestHandleGetVideoEncoderSettings_NoJWT_Returns401 verifies that missing JWT returns 401.
func TestHandleGetVideoEncoderSettings_NoJWT_Returns401(t *testing.T) {
	vs, _ := newTestStores(t)
	authSvc := newTestAuthSvc(t)

	handler := handleGetVideoEncoderSettings(vs, authSvc)
	req := httptest.NewRequest("GET", "/api/admin/settings/video-encoder", nil)
	// No Authorization header
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected status 401, got %d", w.Code)
	}
}

// TestHandleGetVideoEncoderSettings_ContentTypeJSON verifies that the response Content-Type is application/json.
func TestHandleGetVideoEncoderSettings_ContentTypeJSON(t *testing.T) {
	vs, _ := newTestStores(t)
	authSvc := newTestAuthSvc(t)
	token := loginAndGetToken(t, authSvc)

	handler := handleGetVideoEncoderSettings(vs, authSvc)
	req := httptest.NewRequest("GET", "/api/admin/settings/video-encoder", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if contentType := w.Header().Get("Content-Type"); contentType != "application/json" {
		t.Errorf("expected Content-Type application/json, got %s", contentType)
	}
}

// === handlePutVideoEncoderSettings Tests ===

// TestHandlePutVideoEncoderSettings_SetAuto_Returns200 verifies that setting Auto mode returns 200.
func TestHandlePutVideoEncoderSettings_SetAuto_Returns200(t *testing.T) {
	vs, _ := newTestStores(t)
	authSvc := newTestAuthSvc(t)
	token := loginAndGetToken(t, authSvc)

	handler := handlePutVideoEncoderSettings(vs, authSvc)
	reqBody := []byte(`{"mode":"Auto"}`)
	req := httptest.NewRequest("PUT", "/api/admin/settings/video-encoder", bytes.NewReader(reqBody))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d. Body: %s", w.Code, w.Body.String())
	}

	body, err := io.ReadAll(w.Body)
	if err != nil {
		t.Fatalf("failed to read response: %v", err)
	}

	var resp videoEncoderSettingResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if resp.Mode != db.VideoEncoderModeAuto {
		t.Errorf("expected mode Auto in response, got %v", resp.Mode)
	}
}

// TestHandlePutVideoEncoderSettings_SetCpu_Returns200 verifies that setting Cpu mode returns 200.
func TestHandlePutVideoEncoderSettings_SetCpu_Returns200(t *testing.T) {
	vs, _ := newTestStores(t)
	authSvc := newTestAuthSvc(t)
	token := loginAndGetToken(t, authSvc)

	handler := handlePutVideoEncoderSettings(vs, authSvc)
	reqBody := []byte(`{"mode":"Cpu"}`)
	req := httptest.NewRequest("PUT", "/api/admin/settings/video-encoder", bytes.NewReader(reqBody))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}

	body, err := io.ReadAll(w.Body)
	if err != nil {
		t.Fatalf("failed to read response: %v", err)
	}

	var resp videoEncoderSettingResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if resp.Mode != db.VideoEncoderModeCpu {
		t.Errorf("expected mode Cpu in response, got %v", resp.Mode)
	}
}

// TestHandlePutVideoEncoderSettings_SetGpu_Returns200 verifies that setting Gpu mode returns 200.
func TestHandlePutVideoEncoderSettings_SetGpu_Returns200(t *testing.T) {
	vs, _ := newTestStores(t)
	authSvc := newTestAuthSvc(t)
	token := loginAndGetToken(t, authSvc)

	handler := handlePutVideoEncoderSettings(vs, authSvc)
	reqBody := []byte(`{"mode":"Gpu"}`)
	req := httptest.NewRequest("PUT", "/api/admin/settings/video-encoder", bytes.NewReader(reqBody))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}

	body, err := io.ReadAll(w.Body)
	if err != nil {
		t.Fatalf("failed to read response: %v", err)
	}

	var resp videoEncoderSettingResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if resp.Mode != db.VideoEncoderModeGpu {
		t.Errorf("expected mode Gpu in response, got %v", resp.Mode)
	}
}

// TestHandlePutVideoEncoderSettings_RoundTrip_PersistsMode verifies that setting a mode persists it.
func TestHandlePutVideoEncoderSettings_RoundTrip_PersistsMode(t *testing.T) {
	vs, _ := newTestStores(t)
	authSvc := newTestAuthSvc(t)
	token := loginAndGetToken(t, authSvc)

	// PUT Cpu mode
	handler := handlePutVideoEncoderSettings(vs, authSvc)
	reqBody := []byte(`{"mode":"Cpu"}`)
	req := httptest.NewRequest("PUT", "/api/admin/settings/video-encoder", bytes.NewReader(reqBody))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("PUT failed with status %d", w.Code)
	}

	// GET to verify the mode was persisted
	getHandler := handleGetVideoEncoderSettings(vs, authSvc)
	getReq := httptest.NewRequest("GET", "/api/admin/settings/video-encoder", nil)
	getReq.Header.Set("Authorization", "Bearer "+token)
	getW := httptest.NewRecorder()

	getHandler.ServeHTTP(getW, getReq)

	if getW.Code != http.StatusOK {
		t.Errorf("GET failed with status %d", getW.Code)
	}

	body, err := io.ReadAll(getW.Body)
	if err != nil {
		t.Fatalf("failed to read response: %v", err)
	}

	var resp videoEncoderSettingResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if resp.Mode != db.VideoEncoderModeCpu {
		t.Errorf("expected persisted mode Cpu, got %v", resp.Mode)
	}
}

// TestHandlePutVideoEncoderSettings_NoJWT_Returns401 verifies that missing JWT returns 401.
func TestHandlePutVideoEncoderSettings_NoJWT_Returns401(t *testing.T) {
	vs, _ := newTestStores(t)
	authSvc := newTestAuthSvc(t)

	handler := handlePutVideoEncoderSettings(vs, authSvc)
	reqBody := []byte(`{"mode":"Cpu"}`)
	req := httptest.NewRequest("PUT", "/api/admin/settings/video-encoder", bytes.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")
	// No Authorization header
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected status 401, got %d", w.Code)
	}
}

// TestHandlePutVideoEncoderSettings_MalformedJSON_Returns400 verifies that malformed JSON returns 400.
func TestHandlePutVideoEncoderSettings_MalformedJSON_Returns400(t *testing.T) {
	vs, _ := newTestStores(t)
	authSvc := newTestAuthSvc(t)
	token := loginAndGetToken(t, authSvc)

	handler := handlePutVideoEncoderSettings(vs, authSvc)
	reqBody := []byte(`{invalid json}`)
	req := httptest.NewRequest("PUT", "/api/admin/settings/video-encoder", bytes.NewReader(reqBody))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", w.Code)
	}
}

// TestHandlePutVideoEncoderSettings_UnknownMode_Returns400 verifies that an unknown mode returns 400.
func TestHandlePutVideoEncoderSettings_UnknownMode_Returns400(t *testing.T) {
	vs, _ := newTestStores(t)
	authSvc := newTestAuthSvc(t)
	token := loginAndGetToken(t, authSvc)

	handler := handlePutVideoEncoderSettings(vs, authSvc)
	reqBody := []byte(`{"mode":"UnknownMode"}`)
	req := httptest.NewRequest("PUT", "/api/admin/settings/video-encoder", bytes.NewReader(reqBody))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", w.Code)
	}
}

// === handleGetLogLevelSettings Tests ===

// TestHandleGetLogLevelSettings_Returns200WithDefaultLevel verifies that GET /api/admin/settings/log-level
// returns HTTP 200 with the default Information level.
func TestHandleGetLogLevelSettings_Returns200WithDefaultLevel(t *testing.T) {
	_, ls := newTestStores(t)
	authSvc := newTestAuthSvc(t)
	token := loginAndGetToken(t, authSvc)

	handler := handleGetLogLevelSettings(ls, authSvc)
	req := httptest.NewRequest("GET", "/api/admin/settings/log-level", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}

	body, err := io.ReadAll(w.Body)
	if err != nil {
		t.Fatalf("failed to read response: %v", err)
	}

	var resp logLevelSettingResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if resp.Level != settings.LogLevelInformation {
		t.Errorf("expected level Information, got %s", resp.Level)
	}
}

// TestHandleGetLogLevelSettings_NoJWT_Returns401 verifies that missing JWT returns 401.
func TestHandleGetLogLevelSettings_NoJWT_Returns401(t *testing.T) {
	_, ls := newTestStores(t)
	authSvc := newTestAuthSvc(t)

	handler := handleGetLogLevelSettings(ls, authSvc)
	req := httptest.NewRequest("GET", "/api/admin/settings/log-level", nil)
	// No Authorization header
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected status 401, got %d", w.Code)
	}
}

// TestHandleGetLogLevelSettings_ContentTypeJSON verifies that the response Content-Type is application/json.
func TestHandleGetLogLevelSettings_ContentTypeJSON(t *testing.T) {
	_, ls := newTestStores(t)
	authSvc := newTestAuthSvc(t)
	token := loginAndGetToken(t, authSvc)

	handler := handleGetLogLevelSettings(ls, authSvc)
	req := httptest.NewRequest("GET", "/api/admin/settings/log-level", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if contentType := w.Header().Get("Content-Type"); contentType != "application/json" {
		t.Errorf("expected Content-Type application/json, got %s", contentType)
	}
}

// === handlePutLogLevelSettings Tests ===

// TestHandlePutLogLevelSettings_SetDebug_Returns200WithDebug verifies that setting Debug level returns 200 with Debug in response.
func TestHandlePutLogLevelSettings_SetDebug_Returns200WithDebug(t *testing.T) {
	_, ls := newTestStores(t)
	authSvc := newTestAuthSvc(t)
	token := loginAndGetToken(t, authSvc)

	handler := handlePutLogLevelSettings(ls, authSvc)
	reqBody := []byte(`{"level":"Debug"}`)
	req := httptest.NewRequest("PUT", "/api/admin/settings/log-level", bytes.NewReader(reqBody))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}

	body, err := io.ReadAll(w.Body)
	if err != nil {
		t.Fatalf("failed to read response: %v", err)
	}

	var resp logLevelSettingResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if resp.Level != settings.LogLevelDebug {
		t.Errorf("expected level Debug in response, got %s", resp.Level)
	}
}

// TestHandlePutLogLevelSettings_SetEachLevel_Returns200 verifies that all levels can be set and return 200.
func TestHandlePutLogLevelSettings_SetEachLevel_Returns200(t *testing.T) {
	levels := []settings.LogLevelName{
		settings.LogLevelTrace,
		settings.LogLevelDebug,
		settings.LogLevelInformation,
		settings.LogLevelWarning,
		settings.LogLevelError,
		settings.LogLevelCritical,
		settings.LogLevelNone,
	}

	for _, level := range levels {
		t.Run(string(level), func(t *testing.T) {
			_, ls := newTestStores(t)
			authSvc := newTestAuthSvc(t)
			token := loginAndGetToken(t, authSvc)

			handler := handlePutLogLevelSettings(ls, authSvc)
			reqBody, _ := json.Marshal(logLevelSettingRequest{Level: level})
			req := httptest.NewRequest("PUT", "/api/admin/settings/log-level", bytes.NewReader(reqBody))
			req.Header.Set("Authorization", "Bearer "+token)
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()

			handler.ServeHTTP(w, req)

			if w.Code != http.StatusOK {
				t.Errorf("expected status 200 for level %s, got %d", level, w.Code)
			}

			body, err := io.ReadAll(w.Body)
			if err != nil {
				t.Fatalf("failed to read response: %v", err)
			}

			var resp logLevelSettingResponse
			if err := json.Unmarshal(body, &resp); err != nil {
				t.Fatalf("failed to unmarshal response: %v", err)
			}

			if resp.Level != level {
				t.Errorf("expected level %s in response, got %s", level, resp.Level)
			}
		})
	}
}

// TestHandlePutLogLevelSettings_RoundTrip_PersistsLevel verifies that setting a level persists it.
func TestHandlePutLogLevelSettings_RoundTrip_PersistsLevel(t *testing.T) {
	_, ls := newTestStores(t)
	authSvc := newTestAuthSvc(t)
	token := loginAndGetToken(t, authSvc)

	// PUT Debug level
	handler := handlePutLogLevelSettings(ls, authSvc)
	reqBody := []byte(`{"level":"Debug"}`)
	req := httptest.NewRequest("PUT", "/api/admin/settings/log-level", bytes.NewReader(reqBody))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("PUT failed with status %d", w.Code)
	}

	// GET to verify the level was persisted
	getHandler := handleGetLogLevelSettings(ls, authSvc)
	getReq := httptest.NewRequest("GET", "/api/admin/settings/log-level", nil)
	getReq.Header.Set("Authorization", "Bearer "+token)
	getW := httptest.NewRecorder()

	getHandler.ServeHTTP(getW, getReq)

	if getW.Code != http.StatusOK {
		t.Errorf("GET failed with status %d", getW.Code)
	}

	body, err := io.ReadAll(getW.Body)
	if err != nil {
		t.Fatalf("failed to read response: %v", err)
	}

	var resp logLevelSettingResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if resp.Level != settings.LogLevelDebug {
		t.Errorf("expected persisted level Debug, got %s", resp.Level)
	}
}

// TestHandlePutLogLevelSettings_LiveUpdatesLevelVar verifies that setting a level updates the slog.LevelVar immediately.
func TestHandlePutLogLevelSettings_LiveUpdatesLevelVar(t *testing.T) {
	// We need to construct stores manually to get access to the LevelVar
	database, err := db.Connect(":memory:")
	if err != nil {
		t.Fatalf("failed to connect to DB: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })

	var levelVar slog.LevelVar
	ls := settings.NewLogLevelStore(database.Unwrap(), &levelVar)

	authSvc := newTestAuthSvc(t)
	token := loginAndGetToken(t, authSvc)

	// PUT Warning level
	handler := handlePutLogLevelSettings(ls, authSvc)
	reqBody := []byte(`{"level":"Warning"}`)
	req := httptest.NewRequest("PUT", "/api/admin/settings/log-level", bytes.NewReader(reqBody))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("PUT failed with status %d", w.Code)
	}

	// Verify the LevelVar was updated immediately
	expectedLevel := slog.LevelWarn
	if levelVar.Level() != expectedLevel {
		t.Errorf("expected LevelVar to be updated to Warn (%v), got %v", expectedLevel, levelVar.Level())
	}
}

// TestHandlePutLogLevelSettings_NoJWT_Returns401 verifies that missing JWT returns 401.
func TestHandlePutLogLevelSettings_NoJWT_Returns401(t *testing.T) {
	_, ls := newTestStores(t)
	authSvc := newTestAuthSvc(t)

	handler := handlePutLogLevelSettings(ls, authSvc)
	reqBody := []byte(`{"level":"Debug"}`)
	req := httptest.NewRequest("PUT", "/api/admin/settings/log-level", bytes.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")
	// No Authorization header
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected status 401, got %d", w.Code)
	}
}

// TestHandlePutLogLevelSettings_MalformedJSON_Returns400 verifies that malformed JSON returns 400.
func TestHandlePutLogLevelSettings_MalformedJSON_Returns400(t *testing.T) {
	_, ls := newTestStores(t)
	authSvc := newTestAuthSvc(t)
	token := loginAndGetToken(t, authSvc)

	handler := handlePutLogLevelSettings(ls, authSvc)
	reqBody := []byte(`{invalid json}`)
	req := httptest.NewRequest("PUT", "/api/admin/settings/log-level", bytes.NewReader(reqBody))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", w.Code)
	}
}

// TestHandlePutLogLevelSettings_UnknownLevel_Returns400 verifies that an unknown level returns 400.
func TestHandlePutLogLevelSettings_UnknownLevel_Returns400(t *testing.T) {
	_, ls := newTestStores(t)
	authSvc := newTestAuthSvc(t)
	token := loginAndGetToken(t, authSvc)

	handler := handlePutLogLevelSettings(ls, authSvc)
	reqBody := []byte(`{"level":"UnknownLevel"}`)
	req := httptest.NewRequest("PUT", "/api/admin/settings/log-level", bytes.NewReader(reqBody))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", w.Code)
	}
}
