package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"rbi-go/internal/db"
)

// TestHandleSessionOffer_MalformedJSON_Returns400 verifies that malformed JSON
// in the request body returns HTTP 400.
func TestHandleSessionOffer_MalformedJSON_Returns400(t *testing.T) {
	eng := newTestEngine(t)
	handler := handleSessionOffer(eng, nil, nil)

	malformedJSON := []byte(`{invalid json}`)
	req := httptest.NewRequest("POST", "/api/session/offer", bytes.NewReader(malformedJSON))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", w.Code)
	}
}

// TestHandleSessionOffer_MissingURL_Returns400 verifies that a request without
// the "url" field returns HTTP 400.
func TestHandleSessionOffer_MissingURL_Returns400(t *testing.T) {
	eng := newTestEngine(t)
	handler := handleSessionOffer(eng, nil, nil)

	req := httptest.NewRequest("POST", "/api/session/offer",
		bytes.NewReader([]byte(`{"sdp":"fake-sdp"}`)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", w.Code)
	}
}

// TestHandleSessionOffer_MissingSDPOnly_Returns400 verifies that a request without
// the "sdp" field returns HTTP 400.
func TestHandleSessionOffer_MissingSDPOnly_Returns400(t *testing.T) {
	eng := newTestEngine(t)
	handler := handleSessionOffer(eng, nil, nil)

	req := httptest.NewRequest("POST", "/api/session/offer",
		bytes.NewReader([]byte(`{"url":"http://example.com"}`)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", w.Code)
	}
}

// TestHandleSessionOffer_RelativeURL_Returns400 verifies that a relative URL
// in the "url" field returns HTTP 400.
func TestHandleSessionOffer_RelativeURL_Returns400(t *testing.T) {
	eng := newTestEngine(t)
	handler := handleSessionOffer(eng, nil, nil)

	req := httptest.NewRequest("POST", "/api/session/offer",
		bytes.NewReader([]byte(`{"url":"/path/to/page","sdp":"fake-sdp"}`)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", w.Code)
	}
}

// TestHandleSessionOffer_NonAbsoluteURL_Returns400 verifies that a non-absolute URL
// (e.g., "example.com" without scheme) returns HTTP 400.
func TestHandleSessionOffer_NonAbsoluteURL_Returns400(t *testing.T) {
	eng := newTestEngine(t)
	handler := handleSessionOffer(eng, nil, nil)

	req := httptest.NewRequest("POST", "/api/session/offer",
		bytes.NewReader([]byte(`{"url":"example.com","sdp":"fake-sdp"}`)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", w.Code)
	}
}

// TestHandleSessionOffer_UnmatchedHost_Returns403 verifies that a URL with an
// unmatched host (no policy) returns HTTP 403.
func TestHandleSessionOffer_UnmatchedHost_Returns403(t *testing.T) {
	eng := newTestEngine(t)
	handler := handleSessionOffer(eng, nil, nil)

	req := httptest.NewRequest("POST", "/api/session/offer",
		bytes.NewReader([]byte(`{"url":"http://unmatched.example.com","sdp":"fake-sdp"}`)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("expected status 403, got %d", w.Code)
	}
}

// TestHandleSessionOffer_HtmlAllowInputHost_Returns409 verifies that a URL with
// a host policy set to HtmlAllowInput returns HTTP 409 (mode mismatch).
func TestHandleSessionOffer_HtmlAllowInputHost_Returns409(t *testing.T) {
	eng := newTestEngine(t)
	seedPolicySite(t, eng, "example.com", db.ViewModeHtmlAllowInput)
	eng.Invalidate()

	handler := handleSessionOffer(eng, nil, nil)

	req := httptest.NewRequest("POST", "/api/session/offer",
		bytes.NewReader([]byte(`{"url":"http://example.com","sdp":"fake-sdp"}`)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusConflict {
		t.Errorf("expected status 409, got %d", w.Code)
	}
}

// TestHandleSessionOffer_HtmlNoInputHost_Returns409 verifies that a URL with
// a host policy set to HtmlNoInput returns HTTP 409 (mode mismatch).
func TestHandleSessionOffer_HtmlNoInputHost_Returns409(t *testing.T) {
	eng := newTestEngine(t)
	seedPolicySite(t, eng, "example.com", db.ViewModeHtmlNoInput)
	eng.Invalidate()

	handler := handleSessionOffer(eng, nil, nil)

	req := httptest.NewRequest("POST", "/api/session/offer",
		bytes.NewReader([]byte(`{"url":"http://example.com","sdp":"fake-sdp"}`)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusConflict {
		t.Errorf("expected status 409, got %d", w.Code)
	}
}

// TestHandleSessionOffer_ResponseBodyIsJSON_OnErrors verifies that error responses
// have a valid JSON body with an "error" field.
func TestHandleSessionOffer_ResponseBodyIsJSON_OnErrors(t *testing.T) {
	eng := newTestEngine(t)
	handler := handleSessionOffer(eng, nil, nil)

	// Test with missing URL (400 case).
	req := httptest.NewRequest("POST", "/api/session/offer",
		bytes.NewReader([]byte(`{"sdp":"fake-sdp"}`)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d", w.Code)
	}

	// Verify that the response is valid JSON with an error field.
	body, err := io.ReadAll(w.Body)
	if err != nil {
		t.Fatalf("failed to read response body: %v", err)
	}

	var resp errorResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Errorf("expected valid JSON response, got: %s", string(body))
	}

	if resp.Error == "" {
		t.Error("expected non-empty error message in response")
	}
}

// TestClampViewport_NilRequest_ReturnsDefault verifies that clampViewport(nil, def, min, max)
// returns the default value.
func TestClampViewport_NilRequest_ReturnsDefault(t *testing.T) {
	result := clampViewport(nil, 640, 320, 1280)
	if result != 640 {
		t.Errorf("expected clampViewport(nil, 640, 320, 1280)=640, got %d", result)
	}
}

// TestClampViewport_BelowMin_ReturnsMin verifies that values below the minimum are clamped.
func TestClampViewport_BelowMin_ReturnsMin(t *testing.T) {
	val := 100
	result := clampViewport(&val, 640, 320, 1280)
	if result != 320 {
		t.Errorf("expected clampViewport(&100, 640, 320, 1280)=320, got %d", result)
	}
}

// TestClampViewport_AboveMax_ReturnsMax verifies that values above the maximum are clamped.
func TestClampViewport_AboveMax_ReturnsMax(t *testing.T) {
	val := 2000
	result := clampViewport(&val, 640, 320, 1280)
	if result != 1280 {
		t.Errorf("expected clampViewport(&2000, 640, 320, 1280)=1280, got %d", result)
	}
}

// TestClampViewport_InRange_ReturnsSelf verifies that values within [min, max]
// are returned unchanged.
func TestClampViewport_InRange_ReturnsSelf(t *testing.T) {
	val := 800
	result := clampViewport(&val, 640, 320, 1280)
	if result != 800 {
		t.Errorf("expected clampViewport(&800, 640, 320, 1280)=800, got %d", result)
	}
}

// TestClampViewport_AtMin_ReturnsMin verifies that a value exactly at the minimum
// is returned unchanged.
func TestClampViewport_AtMin_ReturnsMin(t *testing.T) {
	val := 320
	result := clampViewport(&val, 640, 320, 1280)
	if result != 320 {
		t.Errorf("expected clampViewport(&320, 640, 320, 1280)=320, got %d", result)
	}
}

// TestClampViewport_AtMax_ReturnsMax verifies that a value exactly at the maximum
// is returned unchanged.
func TestClampViewport_AtMax_ReturnsMax(t *testing.T) {
	val := 1280
	result := clampViewport(&val, 640, 320, 1280)
	if result != 1280 {
		t.Errorf("expected clampViewport(&1280, 640, 320, 1280)=1280, got %d", result)
	}
}
