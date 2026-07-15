package auth

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// stubHandler is a simple test handler that responds with 200 and "ok".
func stubHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"status":"ok"}`))
}

// === RequireJWT tests ===

// TestRequireJWT_ValidToken_PassesThrough verifies that a request with a valid
// JWT bearer token is passed through to the next handler.
func TestRequireJWT_ValidToken_PassesThrough(t *testing.T) {
	svc := newTestSvc(t)

	// Issue a valid token
	token, err := svc.LoginOrBootstrap("admin@example.com", "password123")
	if err != nil {
		t.Fatalf("LoginOrBootstrap failed: %v", err)
	}

	// Build a middleware-wrapped handler
	handler := RequireJWT(svc, http.HandlerFunc(stubHandler))

	// Create a request with valid token
	req := httptest.NewRequest("GET", "/protected", nil)
	req.Header.Set("Authorization", "Bearer "+token)

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}

	body, err := io.ReadAll(w.Body)
	if err != nil {
		t.Fatalf("Failed to read body: %v", err)
	}
	if string(body) != `{"status":"ok"}` {
		t.Errorf("Expected stubHandler response, got %q", string(body))
	}
}

// TestRequireJWT_MissingAuthHeader_Returns401 verifies that a request without
// an Authorization header returns 401.
func TestRequireJWT_MissingAuthHeader_Returns401(t *testing.T) {
	svc := newTestSvc(t)
	handler := RequireJWT(svc, http.HandlerFunc(stubHandler))

	req := httptest.NewRequest("GET", "/protected", nil)
	// No Authorization header

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("Expected status 401, got %d", w.Code)
	}
}

// TestRequireJWT_MissingAuthHeader_BodyContainsError verifies that the 401 response
// contains a JSON body with an "error" field.
func TestRequireJWT_MissingAuthHeader_BodyContainsError(t *testing.T) {
	svc := newTestSvc(t)
	handler := RequireJWT(svc, http.HandlerFunc(stubHandler))

	req := httptest.NewRequest("GET", "/protected", nil)
	// No Authorization header

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	body, err := io.ReadAll(w.Body)
	if err != nil {
		t.Fatalf("Failed to read body: %v", err)
	}

	var result map[string]string
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatalf("Expected JSON body, got %q: %v", string(body), err)
	}

	if _, ok := result["error"]; !ok {
		t.Errorf("Expected 'error' field in response, got keys: %v", result)
	}
}

// TestRequireJWT_NoBearer_Returns401 verifies that an Authorization header without
// "Bearer " prefix returns 401.
func TestRequireJWT_NoBearer_Returns401(t *testing.T) {
	svc := newTestSvc(t)
	handler := RequireJWT(svc, http.HandlerFunc(stubHandler))

	req := httptest.NewRequest("GET", "/protected", nil)
	req.Header.Set("Authorization", "Basic dXNlcjpwYXNz")

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("Expected status 401, got %d", w.Code)
	}
}

// TestRequireJWT_EmptyBearerToken_Returns401 verifies that "Bearer " with no token
// returns 401.
func TestRequireJWT_EmptyBearerToken_Returns401(t *testing.T) {
	svc := newTestSvc(t)
	handler := RequireJWT(svc, http.HandlerFunc(stubHandler))

	req := httptest.NewRequest("GET", "/protected", nil)
	req.Header.Set("Authorization", "Bearer ")

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("Expected status 401, got %d", w.Code)
	}
}

// TestRequireJWT_InvalidToken_Returns401 verifies that an invalid token string
// returns 401.
func TestRequireJWT_InvalidToken_Returns401(t *testing.T) {
	svc := newTestSvc(t)
	handler := RequireJWT(svc, http.HandlerFunc(stubHandler))

	req := httptest.NewRequest("GET", "/protected", nil)
	req.Header.Set("Authorization", "Bearer invalid.token.string")

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("Expected status 401, got %d", w.Code)
	}
}

// TestRequireJWT_ExpiredToken_Returns401 verifies that an expired token returns 401.
func TestRequireJWT_ExpiredToken_Returns401(t *testing.T) {
	svc := newTestSvc(t)
	handler := RequireJWT(svc, http.HandlerFunc(stubHandler))

	// Create an expired token using the same pattern as in service_test.go
	expiredToken := createExpiredToken(svc)

	req := httptest.NewRequest("GET", "/protected", nil)
	req.Header.Set("Authorization", "Bearer "+expiredToken)

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("Expected status 401, got %d", w.Code)
	}
}

// TestRequireJWT_ContentTypeJSON_OnRejection verifies that a 401 response sets
// Content-Type: application/json.
func TestRequireJWT_ContentTypeJSON_OnRejection(t *testing.T) {
	svc := newTestSvc(t)
	handler := RequireJWT(svc, http.HandlerFunc(stubHandler))

	req := httptest.NewRequest("GET", "/protected", nil)
	req.Header.Set("Authorization", "Bearer invalid")

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if contentType := w.Header().Get("Content-Type"); contentType != "application/json" {
		t.Errorf("Expected Content-Type: application/json, got %q", contentType)
	}
}

// createExpiredToken is a helper that creates an expired JWT token for testing.
func createExpiredToken(svc *AdminAuthService) string {
	now := time.Now().UTC()
	expiredClaims := jwt.MapClaims{
		"sub":   "admin@example.com",
		"email": "admin@example.com",
		"iss":   svc.jwtCfg.Issuer,
		"aud":   []string{svc.jwtCfg.Audience},
		"iat":   now.Add(-2 * time.Hour).Unix(),
		"exp":   now.Add(-1 * time.Hour).Unix(),
	}

	expiredTokenObj := jwt.NewWithClaims(jwt.SigningMethodHS256, expiredClaims)
	signedExpiredToken, _ := expiredTokenObj.SignedString([]byte(svc.jwtCfg.Key))
	return signedExpiredToken
}
