package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"rbi-go/internal/auth"
	"rbi-go/internal/config"
	"rbi-go/internal/db"
	"rbi-go/internal/policy"
	"rbi-go/internal/rootca"
	"rbi-go/internal/settings"
)

// newTestDB opens an in-memory SQLite database for tests and registers cleanup.
func newTestDB(t *testing.T) *db.DB {
	t.Helper()
	database, err := db.Connect(":memory:")
	if err != nil {
		t.Fatalf("newTestDB: open in-memory DB: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	return database
}

// newTestEngine creates a policy.Engine backed by an in-memory SQLite database for
// use in router tests that don't exercise policy logic directly.
func newTestEngine(t *testing.T) *policy.Engine {
	t.Helper()
	return policy.NewEngine(newTestDB(t))
}

// newTestAuthSvc creates an AdminAuthService backed by an in-memory SQLite database
// for use in tests that need a router but don't exercise auth logic directly.
func newTestAuthSvc(t *testing.T) *auth.AdminAuthService {
	t.Helper()
	database, err := db.Connect(":memory:")
	if err != nil {
		t.Fatalf("newTestAuthSvc: open in-memory DB: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	jwtCfg := &config.JwtConfig{
		Key:        "test-signing-key-at-least-32-bytes-long!",
		Issuer:     "test-issuer",
		Audience:   "test-audience",
		TtlMinutes: 60,
	}
	return auth.NewAdminAuthService(database, jwtCfg)
}

// newTestCaStores creates a rootca.Store and rootca.Minter backed by an in-memory
// SQLite database for use in router tests that don't exercise CA logic directly.
func newTestCaStores(t *testing.T) (*rootca.Store, *rootca.Minter) {
	t.Helper()
	database, err := db.Connect(":memory:")
	if err != nil {
		t.Fatalf("newTestCaStores: open in-memory DB: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	store := rootca.NewStore(database.Unwrap())
	return store, rootca.NewMinter(store)
}

// newTestStores creates VideoEncoderStore and LogLevelStore backed by an in-memory
// SQLite database for use in router tests that don't exercise settings logic directly.
func newTestStores(t *testing.T) (*settings.VideoEncoderStore, *settings.LogLevelStore) {
	t.Helper()
	database, err := db.Connect(":memory:")
	if err != nil {
		t.Fatalf("newTestStores: open in-memory DB: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	var lv slog.LevelVar
	return settings.NewVideoEncoderStore(database.Unwrap()),
		settings.NewLogLevelStore(database.Unwrap(), &lv)
}

// loginAndGetToken performs a login request to the admin service and returns the JWT token.
// It is a helper for tests that need to make authenticated requests.
func loginAndGetToken(t *testing.T, authSvc *auth.AdminAuthService) string {
	t.Helper()
	handler := handleAdminLogin(authSvc)

	reqBody := []byte(`{"email":"testadmin@example.com","password":"testpassword123"}`)
	req := httptest.NewRequest("POST", "/api/admin/auth/login", bytes.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("login failed with status %d, body: %s", w.Code, w.Body.String())
	}

	body, err := io.ReadAll(w.Body)
	if err != nil {
		t.Fatalf("failed to read response body: %v", err)
	}

	var result loginResponse
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatalf("failed to unmarshal login response: %v", err)
	}

	if result.Token == "" {
		t.Fatal("login returned empty token")
	}

	return result.Token
}

// TestHandleHealth_Returns200 verifies that the /health endpoint returns HTTP 200.
func TestHandleHealth_Returns200(t *testing.T) {
	req := httptest.NewRequest("GET", "/health", nil)
	w := httptest.NewRecorder()

	handleHealth(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}
}

// TestHandleHealth_BodyExactlyStatusOK verifies that the response body is exactly {"status":"ok"} with no trailing newline.
func TestHandleHealth_BodyExactlyStatusOK(t *testing.T) {
	req := httptest.NewRequest("GET", "/health", nil)
	w := httptest.NewRecorder()

	handleHealth(w, req)

	expected := []byte(`{"status":"ok"}`)
	actual := w.Body.Bytes()

	if !bytes.Equal(actual, expected) {
		t.Errorf("Expected body %q, got %q", string(expected), string(actual))
	}
}

// TestHandleHealth_ContentTypeJSON verifies that the response Content-Type is application/json.
func TestHandleHealth_ContentTypeJSON(t *testing.T) {
	req := httptest.NewRequest("GET", "/health", nil)
	w := httptest.NewRecorder()

	handleHealth(w, req)

	if contentType := w.Header().Get("Content-Type"); contentType != "application/json" {
		t.Errorf("Expected Content-Type application/json, got %s", contentType)
	}
}

// TestResolveWwwRoot_AbsolutePathExists verifies that an absolute path that exists is returned as-is.
func TestResolveWwwRoot_AbsolutePathExists(t *testing.T) {
	dir := t.TempDir()
	result, err := resolveWwwRoot(dir)

	if err != nil {
		t.Errorf("Expected no error for existing absolute path, got %v", err)
	}
	if result != dir {
		t.Errorf("Expected absolute path to be returned unchanged, got %s", result)
	}
}

// TestResolveWwwRoot_AbsolutePathMissing verifies that an absolute path that doesn't exist returns an error.
func TestResolveWwwRoot_AbsolutePathMissing(t *testing.T) {
	absPath := "/nonexistent/absolute/path/to/wwwroot"
	_, err := resolveWwwRoot(absPath)

	if err == nil {
		t.Errorf("Expected error for non-existent absolute path, got nil")
	}
}

// TestResolveWwwRoot_RelativePath_CwdFallback verifies relative path resolution with cwd fallback.
func TestResolveWwwRoot_RelativePath_CwdFallback(t *testing.T) {
	// Create a temp dir with a wwwroot subdir
	baseDir := t.TempDir()
	wwwrootDir := filepath.Join(baseDir, "wwwroot")
	if err := os.Mkdir(wwwrootDir, 0o755); err != nil {
		t.Fatalf("Failed to create wwwroot dir: %v", err)
	}

	// Save current working directory
	oldCwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Failed to get current working directory: %v", err)
	}
	defer os.Chdir(oldCwd) // Restore cwd after test

	// Change to temp base dir
	if err := os.Chdir(baseDir); err != nil {
		t.Fatalf("Failed to change to temp dir: %v", err)
	}

	// Resolve relative path should find it relative to cwd
	result, err := resolveWwwRoot("wwwroot")

	if err != nil {
		t.Errorf("Expected no error for relative path with cwd fallback, got %v", err)
	}
	if result != wwwrootDir {
		t.Errorf("Expected %s, got %s", wwwrootDir, result)
	}
}

// TestResolveWwwRoot_NeitherExistsReturnsError verifies that error is returned when neither candidate exists.
func TestResolveWwwRoot_NeitherExistsReturnsError(t *testing.T) {
	// Create a temp cwd with no matching wwwroot
	oldCwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Failed to get current working directory: %v", err)
	}
	defer os.Chdir(oldCwd)

	baseDir := t.TempDir()
	if err := os.Chdir(baseDir); err != nil {
		t.Fatalf("Failed to change to temp dir: %v", err)
	}

	_, err = resolveWwwRoot("nonexistent/path")

	if err == nil {
		t.Errorf("Expected error when neither candidate path exists, got nil")
	}
}

// TestResolveWwwRoot_AlreadyAbsolute_ReturnedUnchanged verifies that absolute paths are returned unchanged without normalization.
func TestResolveWwwRoot_AlreadyAbsolute_ReturnedUnchanged(t *testing.T) {
	dir := t.TempDir()
	result, err := resolveWwwRoot(dir)

	if err != nil {
		t.Fatalf("resolveWwwRoot failed: %v", err)
	}
	if result != dir {
		t.Errorf("Expected unchanged absolute path %s, got %s", dir, result)
	}
}

// TestBuildRouter_HealthRoute verifies that the router has a /health route that returns 200.
func TestBuildRouter_HealthRoute(t *testing.T) {
	dir := t.TempDir()
	vs, ls := newTestStores(t)
	cas, cam := newTestCaStores(t)
	mux := buildRouter(dir, newTestAuthSvc(t), newTestEngine(t), vs, ls, cas, cam, nil, nil)

	server := httptest.NewServer(mux)
	defer server.Close()

	resp, err := http.Get(server.URL + "/health")
	if err != nil {
		t.Fatalf("Failed to call /health: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected status 200 from /health, got %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("Failed to read response body: %v", err)
	}

	var result map[string]string
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatalf("Failed to unmarshal response: %v", err)
	}

	if result["status"] != "ok" {
		t.Errorf("Expected status=ok, got %s", result["status"])
	}
}

// TestBuildRouter_StaticIndexHTML verifies that GET / serves index.html from staticDir.
func TestBuildRouter_StaticIndexHTML(t *testing.T) {
	dir := t.TempDir()
	indexPath := filepath.Join(dir, "index.html")
	if err := os.WriteFile(indexPath, []byte("<html>Test Index</html>"), 0o644); err != nil {
		t.Fatalf("Failed to write index.html: %v", err)
	}

	vs, ls := newTestStores(t)
	cas, cam := newTestCaStores(t)
	mux := buildRouter(dir, newTestAuthSvc(t), newTestEngine(t), vs, ls, cas, cam, nil, nil)
	server := httptest.NewServer(mux)
	defer server.Close()

	resp, err := http.Get(server.URL + "/")
	if err != nil {
		t.Fatalf("Failed to call /: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected status 200 from /, got %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("Failed to read response body: %v", err)
	}

	if string(body) != "<html>Test Index</html>" {
		t.Errorf("Expected index.html content, got %s", string(body))
	}
}

// TestBuildRouter_StaticSubpathFile verifies that nested static files are served correctly.
func TestBuildRouter_StaticSubpathFile(t *testing.T) {
	dir := t.TempDir()
	adminDir := filepath.Join(dir, "admin")
	if err := os.Mkdir(adminDir, 0o755); err != nil {
		t.Fatalf("Failed to create admin dir: %v", err)
	}

	adminIndexPath := filepath.Join(adminDir, "index.html")
	if err := os.WriteFile(adminIndexPath, []byte("<html>Admin Panel</html>"), 0o644); err != nil {
		t.Fatalf("Failed to write admin/index.html: %v", err)
	}

	vs, ls := newTestStores(t)
	cas, cam := newTestCaStores(t)
	mux := buildRouter(dir, newTestAuthSvc(t), newTestEngine(t), vs, ls, cas, cam, nil, nil)
	server := httptest.NewServer(mux)
	defer server.Close()

	resp, err := http.Get(server.URL + "/admin/index.html")
	if err != nil {
		t.Fatalf("Failed to call /admin/index.html: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected status 200 from /admin/index.html, got %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("Failed to read response body: %v", err)
	}

	if string(body) != "<html>Admin Panel</html>" {
		t.Errorf("Expected admin/index.html content, got %s", string(body))
	}
}

// TestBuildRouter_StaticFileDirectPath verifies that a file like /foo.js is served directly.
func TestBuildRouter_StaticFileDirectPath(t *testing.T) {
	dir := t.TempDir()
	jsPath := filepath.Join(dir, "foo.js")
	if err := os.WriteFile(jsPath, []byte("console.log('test');"), 0o644); err != nil {
		t.Fatalf("Failed to write foo.js: %v", err)
	}

	vs, ls := newTestStores(t)
	cas, cam := newTestCaStores(t)
	mux := buildRouter(dir, newTestAuthSvc(t), newTestEngine(t), vs, ls, cas, cam, nil, nil)
	server := httptest.NewServer(mux)
	defer server.Close()

	resp, err := http.Get(server.URL + "/foo.js")
	if err != nil {
		t.Fatalf("Failed to call /foo.js: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected status 200 from /foo.js, got %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("Failed to read response body: %v", err)
	}

	if string(body) != "console.log('test');" {
		t.Errorf("Expected foo.js content, got %s", string(body))
	}
}

// === handleAdminAuthStatus tests ===

// TestHandleAdminAuthStatus_FreshDB_BootstrappedFalse verifies that GET /api/admin/auth/status
// returns {"bootstrapped":false} when no admin account exists.
func TestHandleAdminAuthStatus_FreshDB_BootstrappedFalse(t *testing.T) {
	authSvc := newTestAuthSvc(t)
	handler := handleAdminAuthStatus(authSvc)

	req := httptest.NewRequest("GET", "/api/admin/auth/status", nil)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}

	body, err := io.ReadAll(w.Body)
	if err != nil {
		t.Fatalf("Failed to read response body: %v", err)
	}

	var result authStatusResponse
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatalf("Failed to unmarshal response: %v", err)
	}

	if result.Bootstrapped {
		t.Errorf("Expected bootstrapped=false for fresh DB, got true")
	}
}

// TestHandleAdminAuthStatus_AfterLogin_BootstrappedTrue verifies that after a successful
// login, GET /api/admin/auth/status returns {"bootstrapped":true}.
func TestHandleAdminAuthStatus_AfterLogin_BootstrappedTrue(t *testing.T) {
	authSvc := newTestAuthSvc(t)

	// First, login to bootstrap
	loginReq := httptest.NewRequest("POST", "/api/admin/auth/login", bytes.NewReader([]byte(`{"email":"admin@example.com","password":"password123"}`)))
	loginReq.Header.Set("Content-Type", "application/json")
	loginW := httptest.NewRecorder()

	handleAdminLogin(authSvc).ServeHTTP(loginW, loginReq)
	if loginW.Code != http.StatusOK {
		t.Fatalf("Login failed with status %d", loginW.Code)
	}

	// Now check status
	statusReq := httptest.NewRequest("GET", "/api/admin/auth/status", nil)
	statusW := httptest.NewRecorder()

	handleAdminAuthStatus(authSvc).ServeHTTP(statusW, statusReq)

	if statusW.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", statusW.Code)
	}

	body, err := io.ReadAll(statusW.Body)
	if err != nil {
		t.Fatalf("Failed to read response body: %v", err)
	}

	var result authStatusResponse
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatalf("Failed to unmarshal response: %v", err)
	}

	if !result.Bootstrapped {
		t.Errorf("Expected bootstrapped=true after login, got false")
	}
}

// TestHandleAdminAuthStatus_ContentTypeJSON verifies that the response Content-Type is application/json.
func TestHandleAdminAuthStatus_ContentTypeJSON(t *testing.T) {
	authSvc := newTestAuthSvc(t)
	handler := handleAdminAuthStatus(authSvc)

	req := httptest.NewRequest("GET", "/api/admin/auth/status", nil)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if contentType := w.Header().Get("Content-Type"); contentType != "application/json" {
		t.Errorf("Expected Content-Type: application/json, got %q", contentType)
	}
}

// === handleAdminLogin tests ===

// TestHandleAdminLogin_FirstCall_Returns200WithToken verifies that the first call
// to POST /api/admin/auth/login returns 200 with a non-empty token.
func TestHandleAdminLogin_FirstCall_Returns200WithToken(t *testing.T) {
	authSvc := newTestAuthSvc(t)
	handler := handleAdminLogin(authSvc)

	reqBody := []byte(`{"email":"admin@example.com","password":"password123"}`)
	req := httptest.NewRequest("POST", "/api/admin/auth/login", bytes.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}

	body, err := io.ReadAll(w.Body)
	if err != nil {
		t.Fatalf("Failed to read response body: %v", err)
	}

	var result loginResponse
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatalf("Failed to unmarshal response: %v", err)
	}

	if result.Token == "" {
		t.Error("Expected non-empty token in response")
	}
}

// TestHandleAdminLogin_SecondCallCorrectCreds_Returns200WithToken verifies that a second call
// with correct credentials returns 200 with a token.
func TestHandleAdminLogin_SecondCallCorrectCreds_Returns200WithToken(t *testing.T) {
	authSvc := newTestAuthSvc(t)
	handler := handleAdminLogin(authSvc)

	email := "admin@example.com"
	password := "password123"

	// First call to bootstrap
	reqBody1 := []byte(`{"email":"` + email + `","password":"` + password + `"}`)
	req1 := httptest.NewRequest("POST", "/api/admin/auth/login", bytes.NewReader(reqBody1))
	req1.Header.Set("Content-Type", "application/json")
	w1 := httptest.NewRecorder()

	handler.ServeHTTP(w1, req1)
	if w1.Code != http.StatusOK {
		t.Fatalf("First login failed with status %d", w1.Code)
	}

	// Second call with same credentials
	reqBody2 := []byte(`{"email":"` + email + `","password":"` + password + `"}`)
	req2 := httptest.NewRequest("POST", "/api/admin/auth/login", bytes.NewReader(reqBody2))
	req2.Header.Set("Content-Type", "application/json")
	w2 := httptest.NewRecorder()

	handler.ServeHTTP(w2, req2)

	if w2.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w2.Code)
	}

	body, err := io.ReadAll(w2.Body)
	if err != nil {
		t.Fatalf("Failed to read response body: %v", err)
	}

	var result loginResponse
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatalf("Failed to unmarshal response: %v", err)
	}

	if result.Token == "" {
		t.Error("Expected non-empty token in response")
	}
}

// TestHandleAdminLogin_WrongPassword_Returns401EmptyBody verifies that a login attempt
// with wrong password returns 401 with an empty body.
func TestHandleAdminLogin_WrongPassword_Returns401EmptyBody(t *testing.T) {
	authSvc := newTestAuthSvc(t)
	handler := handleAdminLogin(authSvc)

	// First call to bootstrap
	reqBody1 := []byte(`{"email":"admin@example.com","password":"correct"}`)
	req1 := httptest.NewRequest("POST", "/api/admin/auth/login", bytes.NewReader(reqBody1))
	req1.Header.Set("Content-Type", "application/json")
	w1 := httptest.NewRecorder()

	handler.ServeHTTP(w1, req1)
	if w1.Code != http.StatusOK {
		t.Fatalf("First login failed with status %d", w1.Code)
	}

	// Second call with wrong password
	reqBody2 := []byte(`{"email":"admin@example.com","password":"wrongpassword"}`)
	req2 := httptest.NewRequest("POST", "/api/admin/auth/login", bytes.NewReader(reqBody2))
	req2.Header.Set("Content-Type", "application/json")
	w2 := httptest.NewRecorder()

	handler.ServeHTTP(w2, req2)

	if w2.Code != http.StatusUnauthorized {
		t.Errorf("Expected status 401, got %d", w2.Code)
	}

	body, err := io.ReadAll(w2.Body)
	if err != nil {
		t.Fatalf("Failed to read response body: %v", err)
	}

	if len(body) != 0 {
		t.Errorf("Expected empty body on 401, got %q", string(body))
	}
}

// TestHandleAdminLogin_WrongEmail_Returns401EmptyBody verifies that a login attempt
// with wrong email returns 401 with an empty body.
func TestHandleAdminLogin_WrongEmail_Returns401EmptyBody(t *testing.T) {
	authSvc := newTestAuthSvc(t)
	handler := handleAdminLogin(authSvc)

	// First call to bootstrap with one email
	reqBody1 := []byte(`{"email":"admin@example.com","password":"password123"}`)
	req1 := httptest.NewRequest("POST", "/api/admin/auth/login", bytes.NewReader(reqBody1))
	req1.Header.Set("Content-Type", "application/json")
	w1 := httptest.NewRecorder()

	handler.ServeHTTP(w1, req1)
	if w1.Code != http.StatusOK {
		t.Fatalf("First login failed with status %d", w1.Code)
	}

	// Second call with different email
	reqBody2 := []byte(`{"email":"different@example.com","password":"password123"}`)
	req2 := httptest.NewRequest("POST", "/api/admin/auth/login", bytes.NewReader(reqBody2))
	req2.Header.Set("Content-Type", "application/json")
	w2 := httptest.NewRecorder()

	handler.ServeHTTP(w2, req2)

	if w2.Code != http.StatusUnauthorized {
		t.Errorf("Expected status 401, got %d", w2.Code)
	}

	body, err := io.ReadAll(w2.Body)
	if err != nil {
		t.Fatalf("Failed to read response body: %v", err)
	}

	if len(body) != 0 {
		t.Errorf("Expected empty body on 401, got %q", string(body))
	}
}

// TestHandleAdminLogin_MissingEmail_Returns400WithError verifies that a login request
// without an email field returns 400 with an error message.
func TestHandleAdminLogin_MissingEmail_Returns400WithError(t *testing.T) {
	authSvc := newTestAuthSvc(t)
	handler := handleAdminLogin(authSvc)

	reqBody := []byte(`{"password":"password123"}`)
	req := httptest.NewRequest("POST", "/api/admin/auth/login", bytes.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("Expected status 400, got %d", w.Code)
	}

	body, err := io.ReadAll(w.Body)
	if err != nil {
		t.Fatalf("Failed to read response body: %v", err)
	}

	var result errorResponse
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatalf("Failed to unmarshal response: %v", err)
	}

	if result.Error == "" {
		t.Error("Expected non-empty error message")
	}
}

// TestHandleAdminLogin_MissingPassword_Returns400WithError verifies that a login request
// without a password field returns 400 with an error message.
func TestHandleAdminLogin_MissingPassword_Returns400WithError(t *testing.T) {
	authSvc := newTestAuthSvc(t)
	handler := handleAdminLogin(authSvc)

	reqBody := []byte(`{"email":"admin@example.com"}`)
	req := httptest.NewRequest("POST", "/api/admin/auth/login", bytes.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("Expected status 400, got %d", w.Code)
	}

	body, err := io.ReadAll(w.Body)
	if err != nil {
		t.Fatalf("Failed to read response body: %v", err)
	}

	var result errorResponse
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatalf("Failed to unmarshal response: %v", err)
	}

	if result.Error == "" {
		t.Error("Expected non-empty error message")
	}
}

// TestHandleAdminLogin_EmptyBodyJSON_Returns400WithError verifies that an empty JSON object
// returns 400 with an error message.
func TestHandleAdminLogin_EmptyBodyJSON_Returns400WithError(t *testing.T) {
	authSvc := newTestAuthSvc(t)
	handler := handleAdminLogin(authSvc)

	reqBody := []byte(`{}`)
	req := httptest.NewRequest("POST", "/api/admin/auth/login", bytes.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("Expected status 400, got %d", w.Code)
	}

	body, err := io.ReadAll(w.Body)
	if err != nil {
		t.Fatalf("Failed to read response body: %v", err)
	}

	var result errorResponse
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatalf("Failed to unmarshal response: %v", err)
	}

	if result.Error == "" {
		t.Error("Expected non-empty error message")
	}
}

// TestHandleAdminLogin_MalformedJSON_Returns400 verifies that malformed JSON
// returns 400.
func TestHandleAdminLogin_MalformedJSON_Returns400(t *testing.T) {
	authSvc := newTestAuthSvc(t)
	handler := handleAdminLogin(authSvc)

	reqBody := []byte(`{invalid json`)
	req := httptest.NewRequest("POST", "/api/admin/auth/login", bytes.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("Expected status 400, got %d", w.Code)
	}
}

// TestHandleAdminLogin_WhitespaceOnlyEmail_Returns400 verifies that an email containing
// only whitespace returns 400.
func TestHandleAdminLogin_WhitespaceOnlyEmail_Returns400(t *testing.T) {
	authSvc := newTestAuthSvc(t)
	handler := handleAdminLogin(authSvc)

	reqBody := []byte(`{"email":"   ","password":"password123"}`)
	req := httptest.NewRequest("POST", "/api/admin/auth/login", bytes.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("Expected status 400, got %d", w.Code)
	}

	body, err := io.ReadAll(w.Body)
	if err != nil {
		t.Fatalf("Failed to read response body: %v", err)
	}

	var result errorResponse
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatalf("Failed to unmarshal response: %v", err)
	}

	if result.Error == "" {
		t.Error("Expected non-empty error message")
	}
}

// === extractHost Tests ===

// TestExtractHost_BareHost verifies that a bare hostname is returned unchanged and lowercased.
func TestExtractHost_BareHost(t *testing.T) {
	result := extractHost("example.com")
	expected := "example.com"
	if result != expected {
		t.Errorf("expected %q, got %q", expected, result)
	}
}

// TestExtractHost_HostWithPort verifies that host:port is normalized to just the host.
func TestExtractHost_HostWithPort(t *testing.T) {
	result := extractHost("example.com:8080")
	expected := "example.com"
	if result != expected {
		t.Errorf("expected %q, got %q", expected, result)
	}
}

// TestExtractHost_FullURL_SchemeStripped verifies that a full URL is parsed and the scheme is stripped.
func TestExtractHost_FullURL_SchemeStripped(t *testing.T) {
	result := extractHost("https://example.com/path")
	expected := "example.com"
	if result != expected {
		t.Errorf("expected %q, got %q", expected, result)
	}
}

// TestExtractHost_UpperCase_Lowercased verifies that uppercase letters are converted to lowercase.
func TestExtractHost_UpperCase_Lowercased(t *testing.T) {
	result := extractHost("EXAMPLE.COM")
	expected := "example.com"
	if result != expected {
		t.Errorf("expected %q, got %q", expected, result)
	}
}

// TestExtractHost_WithPath_PathStripped verifies that a URL with a path has the path stripped.
func TestExtractHost_WithPath_PathStripped(t *testing.T) {
	result := extractHost("example.com/path/to/page")
	expected := "example.com"
	if result != expected {
		t.Errorf("expected %q, got %q", expected, result)
	}
}

// TestExtractHost_LeadingTrailingSpaces_Trimmed verifies that leading and trailing spaces are removed.
func TestExtractHost_LeadingTrailingSpaces_Trimmed(t *testing.T) {
	result := extractHost("  example.com  ")
	expected := "example.com"
	if result != expected {
		t.Errorf("expected %q, got %q", expected, result)
	}
}

// === handleListSites Tests ===

// TestHandleListSites_EmptyDB_Returns200EmptyArray verifies that an empty DB returns 200 with an empty array.
func TestHandleListSites_EmptyDB_Returns200EmptyArray(t *testing.T) {
	eng := newTestEngine(t)
	authSvc := newTestAuthSvc(t)
	token := loginAndGetToken(t, authSvc)

	handler := handleListSites(eng, authSvc)
	req := httptest.NewRequest("GET", "/api/admin/sites", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}

	body, err := io.ReadAll(w.Body)
	if err != nil {
		t.Fatalf("failed to read response body: %v", err)
	}

	var resp []sitePolicyResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if resp == nil {
		t.Error("expected empty array, got nil")
	}
	if len(resp) != 0 {
		t.Errorf("expected 0 sites, got %d", len(resp))
	}
}

// TestHandleListSites_WithSites_ReturnsAll verifies that all sites are returned.
func TestHandleListSites_WithSites_ReturnsAll(t *testing.T) {
	eng := newTestEngine(t)
	authSvc := newTestAuthSvc(t)
	token := loginAndGetToken(t, authSvc)

	// Seed some policies
	seedPolicySite(t, eng, "example.com", db.ViewModeHtmlAllowInput)
	seedPolicySite(t, eng, "app.example.com", db.ViewModeVideoAllowInput)
	eng.Invalidate()

	handler := handleListSites(eng, authSvc)
	req := httptest.NewRequest("GET", "/api/admin/sites", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}

	body, err := io.ReadAll(w.Body)
	if err != nil {
		t.Fatalf("failed to read response body: %v", err)
	}

	var resp []sitePolicyResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if len(resp) != 2 {
		t.Errorf("expected 2 sites, got %d", len(resp))
	}
}

// TestHandleListSites_NoJWT_Returns401 verifies that a request without JWT returns 401.
func TestHandleListSites_NoJWT_Returns401(t *testing.T) {
	eng := newTestEngine(t)
	authSvc := newTestAuthSvc(t)

	handler := handleListSites(eng, authSvc)
	req := httptest.NewRequest("GET", "/api/admin/sites", nil)
	// No Authorization header
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected status 401, got %d", w.Code)
	}
}

// === handleCreateSite Tests ===

// TestHandleCreateSite_Success_Returns201WithLocation verifies that a successful create returns 201 with Location header.
func TestHandleCreateSite_Success_Returns201WithLocation(t *testing.T) {
	eng := newTestEngine(t)
	authSvc := newTestAuthSvc(t)
	token := loginAndGetToken(t, authSvc)

	handler := handleCreateSite(eng, authSvc)
	reqBody := []byte(`{"hostPattern":"example.com","viewMode":"HtmlAllowInput"}`)
	req := httptest.NewRequest("POST", "/api/admin/sites", bytes.NewReader(reqBody))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Errorf("expected status 201, got %d. Body: %s", w.Code, w.Body.String())
	}

	if loc := w.Header().Get("Location"); loc == "" {
		t.Error("expected Location header, got empty")
	} else if !strings.HasPrefix(loc, "/api/admin/sites/") {
		t.Errorf("expected Location to start with /api/admin/sites/, got %q", loc)
	}
}

// TestHandleCreateSite_NormalisesHost verifies that the host is normalized before storage.
func TestHandleCreateSite_NormalisesHost(t *testing.T) {
	eng := newTestEngine(t)
	authSvc := newTestAuthSvc(t)
	token := loginAndGetToken(t, authSvc)

	handler := handleCreateSite(eng, authSvc)
	reqBody := []byte(`{"hostPattern":"https://EXAMPLE.COM:8080/path","viewMode":"HtmlAllowInput"}`)
	req := httptest.NewRequest("POST", "/api/admin/sites", bytes.NewReader(reqBody))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Errorf("expected status 201, got %d", w.Code)
	}

	body, err := io.ReadAll(w.Body)
	if err != nil {
		t.Fatalf("failed to read response: %v", err)
	}

	var resp sitePolicyResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if resp.HostPattern != "example.com" {
		t.Errorf("expected normalized HostPattern 'example.com', got %q", resp.HostPattern)
	}
}

// TestHandleCreateSite_MissingHostPattern_Returns400 verifies that missing hostPattern returns 400.
func TestHandleCreateSite_MissingHostPattern_Returns400(t *testing.T) {
	eng := newTestEngine(t)
	authSvc := newTestAuthSvc(t)
	token := loginAndGetToken(t, authSvc)

	handler := handleCreateSite(eng, authSvc)
	reqBody := []byte(`{"viewMode":"HtmlAllowInput"}`)
	req := httptest.NewRequest("POST", "/api/admin/sites", bytes.NewReader(reqBody))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", w.Code)
	}
}

// TestHandleCreateSite_EmptyHostPattern_Returns400 verifies that an empty hostPattern returns 400.
func TestHandleCreateSite_EmptyHostPattern_Returns400(t *testing.T) {
	eng := newTestEngine(t)
	authSvc := newTestAuthSvc(t)
	token := loginAndGetToken(t, authSvc)

	handler := handleCreateSite(eng, authSvc)
	reqBody := []byte(`{"hostPattern":"   ","viewMode":"HtmlAllowInput"}`)
	req := httptest.NewRequest("POST", "/api/admin/sites", bytes.NewReader(reqBody))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", w.Code)
	}
}

// TestHandleCreateSite_DuplicateHost_Returns409 verifies that creating a duplicate hostPattern returns 409.
func TestHandleCreateSite_DuplicateHost_Returns409(t *testing.T) {
	eng := newTestEngine(t)
	authSvc := newTestAuthSvc(t)
	token := loginAndGetToken(t, authSvc)

	handler := handleCreateSite(eng, authSvc)

	// Create the first site.
	reqBody1 := []byte(`{"hostPattern":"example.com","viewMode":"HtmlAllowInput"}`)
	req1 := httptest.NewRequest("POST", "/api/admin/sites", bytes.NewReader(reqBody1))
	req1.Header.Set("Authorization", "Bearer "+token)
	req1.Header.Set("Content-Type", "application/json")
	w1 := httptest.NewRecorder()

	handler.ServeHTTP(w1, req1)
	if w1.Code != http.StatusCreated {
		t.Fatalf("first create failed with status %d", w1.Code)
	}

	// Try to create a duplicate.
	reqBody2 := []byte(`{"hostPattern":"example.com","viewMode":"VideoAllowInput"}`)
	req2 := httptest.NewRequest("POST", "/api/admin/sites", bytes.NewReader(reqBody2))
	req2.Header.Set("Authorization", "Bearer "+token)
	req2.Header.Set("Content-Type", "application/json")
	w2 := httptest.NewRecorder()

	handler.ServeHTTP(w2, req2)

	if w2.Code != http.StatusConflict {
		t.Errorf("expected status 409, got %d. Body: %s", w2.Code, w2.Body.String())
	}
}

// TestHandleCreateSite_MalformedJSON_Returns400 verifies that malformed JSON returns 400.
func TestHandleCreateSite_MalformedJSON_Returns400(t *testing.T) {
	eng := newTestEngine(t)
	authSvc := newTestAuthSvc(t)
	token := loginAndGetToken(t, authSvc)

	handler := handleCreateSite(eng, authSvc)
	reqBody := []byte(`{invalid json}`)
	req := httptest.NewRequest("POST", "/api/admin/sites", bytes.NewReader(reqBody))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", w.Code)
	}
}

// TestHandleCreateSite_NoJWT_Returns401 verifies that missing JWT returns 401.
func TestHandleCreateSite_NoJWT_Returns401(t *testing.T) {
	eng := newTestEngine(t)
	authSvc := newTestAuthSvc(t)

	handler := handleCreateSite(eng, authSvc)
	reqBody := []byte(`{"hostPattern":"example.com","viewMode":"HtmlAllowInput"}`)
	req := httptest.NewRequest("POST", "/api/admin/sites", bytes.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")
	// No Authorization header
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected status 401, got %d", w.Code)
	}
}

// TestHandleCreateSite_ViewModeStoredAsStringInJSON verifies that ViewMode is returned as a string in JSON.
func TestHandleCreateSite_ViewModeStoredAsStringInJSON(t *testing.T) {
	eng := newTestEngine(t)
	authSvc := newTestAuthSvc(t)
	token := loginAndGetToken(t, authSvc)

	handler := handleCreateSite(eng, authSvc)
	reqBody := []byte(`{"hostPattern":"example.com","viewMode":"VideoNoInput"}`)
	req := httptest.NewRequest("POST", "/api/admin/sites", bytes.NewReader(reqBody))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Errorf("expected status 201, got %d", w.Code)
	}

	body, err := io.ReadAll(w.Body)
	if err != nil {
		t.Fatalf("failed to read response: %v", err)
	}

	// Verify that viewMode is returned as a string "VideoNoInput", not an integer
	if !strings.Contains(string(body), `"viewMode":"VideoNoInput"`) {
		t.Errorf("expected viewMode as string in JSON, got: %s", string(body))
	}
}

// TestHandleCreateSite_EngineInvalidated verifies that the engine cache is invalidated after create.
func TestHandleCreateSite_EngineInvalidated(t *testing.T) {
	eng := newTestEngine(t)
	authSvc := newTestAuthSvc(t)
	token := loginAndGetToken(t, authSvc)

	// Prime the cache with an empty resolve.
	eng.Resolve("example.com")

	handler := handleCreateSite(eng, authSvc)
	reqBody := []byte(`{"hostPattern":"example.com","viewMode":"HtmlAllowInput"}`)
	req := httptest.NewRequest("POST", "/api/admin/sites", bytes.NewReader(reqBody))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("create failed with status %d", w.Code)
	}

	// After create, resolve should find the new policy.
	mode, err := eng.Resolve("example.com")
	if err != nil {
		t.Fatalf("resolve error: %v", err)
	}
	if mode == nil {
		t.Error("expected engine cache to be invalidated and reloaded, but resolve returned nil")
	}
}

// === handleUpdateSite Tests ===

// TestHandleUpdateSite_Success_Returns200UpdatedBody verifies that a successful update returns 200 with the updated policy.
func TestHandleUpdateSite_Success_Returns200UpdatedBody(t *testing.T) {
	eng := newTestEngine(t)
	authSvc := newTestAuthSvc(t)
	token := loginAndGetToken(t, authSvc)

	// Create a site first.
	id := seedPolicySite(t, eng, "example.com", db.ViewModeHtmlAllowInput)
	eng.Invalidate()

	handler := handleUpdateSite(eng, authSvc)
	reqBody := []byte(`{"hostPattern":"example.com","viewMode":"VideoAllowInput"}`)
	req := httptest.NewRequest("PUT", fmt.Sprintf("/api/admin/sites/%d", id), bytes.NewReader(reqBody))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("id", fmt.Sprintf("%d", id))
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d. Body: %s", w.Code, w.Body.String())
	}

	body, err := io.ReadAll(w.Body)
	if err != nil {
		t.Fatalf("failed to read response: %v", err)
	}

	var resp sitePolicyResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if resp.ViewMode != db.ViewModeVideoAllowInput {
		t.Errorf("expected updated ViewMode VideoAllowInput, got %d", resp.ViewMode)
	}
}

// TestHandleUpdateSite_ChangesHostPattern verifies that the host pattern can be changed.
func TestHandleUpdateSite_ChangesHostPattern(t *testing.T) {
	eng := newTestEngine(t)
	authSvc := newTestAuthSvc(t)
	token := loginAndGetToken(t, authSvc)

	// Create a site.
	id := seedPolicySite(t, eng, "example.com", db.ViewModeHtmlAllowInput)
	eng.Invalidate()

	handler := handleUpdateSite(eng, authSvc)
	reqBody := []byte(`{"hostPattern":"newexample.com","viewMode":"HtmlAllowInput"}`)
	req := httptest.NewRequest("PUT", fmt.Sprintf("/api/admin/sites/%d", id), bytes.NewReader(reqBody))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("id", fmt.Sprintf("%d", id))
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}

	body, err := io.ReadAll(w.Body)
	if err != nil {
		t.Fatalf("failed to read response: %v", err)
	}

	var resp sitePolicyResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if resp.HostPattern != "newexample.com" {
		t.Errorf("expected updated HostPattern 'newexample.com', got %q", resp.HostPattern)
	}
}

// TestHandleUpdateSite_NonExistentID_Returns404 verifies that updating a non-existent ID returns 404.
func TestHandleUpdateSite_NonExistentID_Returns404(t *testing.T) {
	eng := newTestEngine(t)
	authSvc := newTestAuthSvc(t)
	token := loginAndGetToken(t, authSvc)

	handler := handleUpdateSite(eng, authSvc)
	reqBody := []byte(`{"hostPattern":"example.com","viewMode":"HtmlAllowInput"}`)
	req := httptest.NewRequest("PUT", "/api/admin/sites/9999", bytes.NewReader(reqBody))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("id", "9999")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected status 404, got %d", w.Code)
	}
}

// TestHandleUpdateSite_MissingHostPattern_Returns400 verifies that missing hostPattern returns 400.
func TestHandleUpdateSite_MissingHostPattern_Returns400(t *testing.T) {
	eng := newTestEngine(t)
	authSvc := newTestAuthSvc(t)
	token := loginAndGetToken(t, authSvc)

	id := seedPolicySite(t, eng, "example.com", db.ViewModeHtmlAllowInput)
	eng.Invalidate()

	handler := handleUpdateSite(eng, authSvc)
	reqBody := []byte(`{"viewMode":"HtmlAllowInput"}`)
	req := httptest.NewRequest("PUT", fmt.Sprintf("/api/admin/sites/%d", id), bytes.NewReader(reqBody))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("id", fmt.Sprintf("%d", id))
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", w.Code)
	}
}

// TestHandleUpdateSite_NoJWT_Returns401 verifies that missing JWT returns 401.
func TestHandleUpdateSite_NoJWT_Returns401(t *testing.T) {
	eng := newTestEngine(t)
	authSvc := newTestAuthSvc(t)

	id := seedPolicySite(t, eng, "example.com", db.ViewModeHtmlAllowInput)
	eng.Invalidate()

	handler := handleUpdateSite(eng, authSvc)
	reqBody := []byte(`{"hostPattern":"example.com","viewMode":"HtmlAllowInput"}`)
	req := httptest.NewRequest("PUT", fmt.Sprintf("/api/admin/sites/%d", id), bytes.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")
	// No Authorization header
	req.SetPathValue("id", fmt.Sprintf("%d", id))
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected status 401, got %d", w.Code)
	}
}

// TestHandleUpdateSite_InvalidIDParam_Returns400 verifies that an invalid ID parameter returns 400.
func TestHandleUpdateSite_InvalidIDParam_Returns400(t *testing.T) {
	eng := newTestEngine(t)
	authSvc := newTestAuthSvc(t)
	token := loginAndGetToken(t, authSvc)

	handler := handleUpdateSite(eng, authSvc)
	reqBody := []byte(`{"hostPattern":"example.com","viewMode":"HtmlAllowInput"}`)
	req := httptest.NewRequest("PUT", "/api/admin/sites/invalid", bytes.NewReader(reqBody))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("id", "invalid")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", w.Code)
	}
}

// === handleDeleteSite Tests ===

// TestHandleDeleteSite_Success_Returns204 verifies that a successful delete returns 204.
func TestHandleDeleteSite_Success_Returns204(t *testing.T) {
	eng := newTestEngine(t)
	authSvc := newTestAuthSvc(t)
	token := loginAndGetToken(t, authSvc)

	id := seedPolicySite(t, eng, "example.com", db.ViewModeHtmlAllowInput)
	eng.Invalidate()

	handler := handleDeleteSite(eng, authSvc)
	req := httptest.NewRequest("DELETE", fmt.Sprintf("/api/admin/sites/%d", id), nil)
	req.Header.Set("Authorization", "Bearer "+token)
	req.SetPathValue("id", fmt.Sprintf("%d", id))
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Errorf("expected status 204, got %d", w.Code)
	}
}

// TestHandleDeleteSite_NonExistentID_Returns404 verifies that deleting a non-existent ID returns 404.
func TestHandleDeleteSite_NonExistentID_Returns404(t *testing.T) {
	eng := newTestEngine(t)
	authSvc := newTestAuthSvc(t)
	token := loginAndGetToken(t, authSvc)

	handler := handleDeleteSite(eng, authSvc)
	req := httptest.NewRequest("DELETE", "/api/admin/sites/9999", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	req.SetPathValue("id", "9999")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected status 404, got %d", w.Code)
	}
}

// TestHandleDeleteSite_NoJWT_Returns401 verifies that missing JWT returns 401.
func TestHandleDeleteSite_NoJWT_Returns401(t *testing.T) {
	eng := newTestEngine(t)
	authSvc := newTestAuthSvc(t)

	id := seedPolicySite(t, eng, "example.com", db.ViewModeHtmlAllowInput)
	eng.Invalidate()

	handler := handleDeleteSite(eng, authSvc)
	req := httptest.NewRequest("DELETE", fmt.Sprintf("/api/admin/sites/%d", id), nil)
	// No Authorization header
	req.SetPathValue("id", fmt.Sprintf("%d", id))
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected status 401, got %d", w.Code)
	}
}

// TestHandleDeleteSite_InvalidIDParam_Returns400 verifies that an invalid ID parameter returns 400.
func TestHandleDeleteSite_InvalidIDParam_Returns400(t *testing.T) {
	eng := newTestEngine(t)
	authSvc := newTestAuthSvc(t)
	token := loginAndGetToken(t, authSvc)

	handler := handleDeleteSite(eng, authSvc)
	req := httptest.NewRequest("DELETE", "/api/admin/sites/invalid", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	req.SetPathValue("id", "invalid")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", w.Code)
	}
}

// TestHandleDeleteSite_EngineInvalidated verifies that the engine cache is invalidated after delete.
func TestHandleDeleteSite_EngineInvalidated(t *testing.T) {
	eng := newTestEngine(t)
	authSvc := newTestAuthSvc(t)
	token := loginAndGetToken(t, authSvc)

	id := seedPolicySite(t, eng, "example.com", db.ViewModeHtmlAllowInput)
	eng.Invalidate()

	// Verify it resolves before delete.
	mode, err := eng.Resolve("example.com")
	if err != nil {
		t.Fatalf("resolve before delete error: %v", err)
	}
	if mode == nil {
		t.Fatal("expected mode to exist before delete")
	}

	handler := handleDeleteSite(eng, authSvc)
	req := httptest.NewRequest("DELETE", fmt.Sprintf("/api/admin/sites/%d", id), nil)
	req.Header.Set("Authorization", "Bearer "+token)
	req.SetPathValue("id", fmt.Sprintf("%d", id))
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("delete failed with status %d", w.Code)
	}

	// After delete and cache invalidation, resolve should return nil.
	mode, err = eng.Resolve("example.com")
	if err != nil {
		t.Fatalf("resolve after delete error: %v", err)
	}
	if mode != nil {
		t.Error("expected engine cache to be invalidated and reloaded, but resolve returned a mode")
	}
}

// === handlePolicyResolve Tests ===

// TestHandlePolicyResolve_AllowedHost_Returns200WithMode verifies that an allowed host returns 200 with the mode.
func TestHandlePolicyResolve_AllowedHost_Returns200WithMode(t *testing.T) {
	eng := newTestEngine(t)
	seedPolicySite(t, eng, "example.com", db.ViewModeHtmlAllowInput)
	eng.Invalidate()

	handler := handlePolicyResolve(eng)
	req := httptest.NewRequest("GET", "/api/policy/resolve?url=https://example.com/path", nil)
	req.RemoteAddr = "127.0.0.1:12345"
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d. Body: %s", w.Code, w.Body.String())
	}

	body, err := io.ReadAll(w.Body)
	if err != nil {
		t.Fatalf("failed to read response: %v", err)
	}

	var resp struct {
		Mode string `json:"mode"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if resp.Mode != "HtmlAllowInput" {
		t.Errorf("expected mode HtmlAllowInput, got %q", resp.Mode)
	}
}

// TestHandlePolicyResolve_DeniedHost_Returns403 verifies that a denied host returns 403.
func TestHandlePolicyResolve_DeniedHost_Returns403(t *testing.T) {
	eng := newTestEngine(t)
	// Don't seed any policies, so any host is denied.

	handler := handlePolicyResolve(eng)
	req := httptest.NewRequest("GET", "/api/policy/resolve?url=https://unmatched.com", nil)
	req.RemoteAddr = "127.0.0.1:12345"
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("expected status 403, got %d", w.Code)
	}

	body, err := io.ReadAll(w.Body)
	if err != nil {
		t.Fatalf("failed to read response: %v", err)
	}

	var resp struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if resp.Error == "" {
		t.Error("expected error message in response")
	}
}

// TestHandlePolicyResolve_MissingURLParam_Returns400 verifies that missing url param returns 400.
func TestHandlePolicyResolve_MissingURLParam_Returns400(t *testing.T) {
	eng := newTestEngine(t)

	handler := handlePolicyResolve(eng)
	req := httptest.NewRequest("GET", "/api/policy/resolve", nil)
	req.RemoteAddr = "127.0.0.1:12345"
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", w.Code)
	}
}

// TestHandlePolicyResolve_RelativeURL_Returns400 verifies that a relative URL returns 400.
func TestHandlePolicyResolve_RelativeURL_Returns400(t *testing.T) {
	eng := newTestEngine(t)

	handler := handlePolicyResolve(eng)
	req := httptest.NewRequest("GET", "/api/policy/resolve?url=/relative/path", nil)
	req.RemoteAddr = "127.0.0.1:12345"
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", w.Code)
	}
}

// TestHandlePolicyResolve_NoJWTRequired_Returns200 verifies that the endpoint works without JWT.
func TestHandlePolicyResolve_NoJWTRequired_Returns200(t *testing.T) {
	eng := newTestEngine(t)
	seedPolicySite(t, eng, "example.com", db.ViewModeHtmlAllowInput)
	eng.Invalidate()

	handler := handlePolicyResolve(eng)
	req := httptest.NewRequest("GET", "/api/policy/resolve?url=https://example.com", nil)
	req.RemoteAddr = "127.0.0.1:12345"
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d (JWT should not be required)", w.Code)
	}
}

// TestHandlePolicyResolve_WritesAuditLog_OnAllow verifies that an allow decision writes a log row.
func TestHandlePolicyResolve_WritesAuditLog_OnAllow(t *testing.T) {
	eng := newTestEngine(t)
	seedPolicySite(t, eng, "example.com", db.ViewModeHtmlAllowInput)
	eng.Invalidate()

	handler := handlePolicyResolve(eng)
	req := httptest.NewRequest("GET", "/api/policy/resolve?url=https://example.com/path", nil)
	req.RemoteAddr = "127.0.0.1:12345"
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("request failed with status %d", w.Code)
	}

	// Check that a log row was inserted.
	var count int
	if err := eng.SQLDB().QueryRow(`SELECT COUNT(*) FROM RequestLogs WHERE Allowed = 1`).Scan(&count); err != nil {
		t.Fatalf("query error: %v", err)
	}

	if count == 0 {
		t.Error("expected audit log row for allowed request, got 0")
	}
}

// TestHandlePolicyResolve_WritesAuditLog_OnDeny verifies that a deny decision writes a log row.
func TestHandlePolicyResolve_WritesAuditLog_OnDeny(t *testing.T) {
	eng := newTestEngine(t)
	// No policies, so all hosts are denied.

	handler := handlePolicyResolve(eng)
	req := httptest.NewRequest("GET", "/api/policy/resolve?url=https://unmatched.com", nil)
	req.RemoteAddr = "127.0.0.1:12345"
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", w.Code)
	}

	// Check that a log row was inserted with Allowed=0.
	var count int
	if err := eng.SQLDB().QueryRow(`SELECT COUNT(*) FROM RequestLogs WHERE Allowed = 0`).Scan(&count); err != nil {
		t.Fatalf("query error: %v", err)
	}

	if count == 0 {
		t.Error("expected audit log row for denied request, got 0")
	}
}

// TestHandlePolicyResolve_ModeReturnedAsStringName verifies that the mode is returned as its string name.
func TestHandlePolicyResolve_ModeReturnedAsStringName(t *testing.T) {
	eng := newTestEngine(t)
	seedPolicySite(t, eng, "example.com", db.ViewModeVideoNoInput)
	eng.Invalidate()

	handler := handlePolicyResolve(eng)
	req := httptest.NewRequest("GET", "/api/policy/resolve?url=https://example.com", nil)
	req.RemoteAddr = "127.0.0.1:12345"
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	body, err := io.ReadAll(w.Body)
	if err != nil {
		t.Fatalf("failed to read response: %v", err)
	}

	var resp struct {
		Mode string `json:"mode"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if resp.Mode != "VideoNoInput" {
		t.Errorf("expected mode as string 'VideoNoInput', got %q", resp.Mode)
	}
}

// === handleAdminLogs Tests ===

// TestHandleAdminLogs_EmptyDB_Returns200EmptyArray verifies that an empty DB returns 200 with an empty array.
func TestHandleAdminLogs_EmptyDB_Returns200EmptyArray(t *testing.T) {
	eng := newTestEngine(t)
	authSvc := newTestAuthSvc(t)
	token := loginAndGetToken(t, authSvc)

	handler := handleAdminLogs(eng, authSvc)
	req := httptest.NewRequest("GET", "/api/admin/logs", nil)
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

	var resp []requestLogResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if resp == nil {
		t.Error("expected empty array, got nil")
	}
	if len(resp) != 0 {
		t.Errorf("expected 0 logs, got %d", len(resp))
	}
}

// TestHandleAdminLogs_Returns_NewestFirst verifies that logs are returned newest-first.
func TestHandleAdminLogs_Returns_NewestFirst(t *testing.T) {
	eng := newTestEngine(t)
	authSvc := newTestAuthSvc(t)
	token := loginAndGetToken(t, authSvc)

	// Insert logs in order 1, 2, 3
	seedLog(t, eng, "https://first.com", "first.com", "allow")
	seedLog(t, eng, "https://second.com", "second.com", "deny")
	seedLog(t, eng, "https://third.com", "third.com", "allow")

	handler := handleAdminLogs(eng, authSvc)
	req := httptest.NewRequest("GET", "/api/admin/logs", nil)
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

	var resp []requestLogResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if len(resp) < 1 {
		t.Fatal("expected at least 1 log")
	}

	// The most recent log should be first (third.com).
	if resp[0].Url != "https://third.com" {
		t.Errorf("expected newest log first, but got Url=%q (expected https://third.com)", resp[0].Url)
	}
}

// TestHandleAdminLogs_DefaultLimit50 verifies that the default limit is 50.
func TestHandleAdminLogs_DefaultLimit50(t *testing.T) {
	eng := newTestEngine(t)
	authSvc := newTestAuthSvc(t)
	token := loginAndGetToken(t, authSvc)

	// Insert 60 logs.
	for i := 0; i < 60; i++ {
		seedLog(t, eng, fmt.Sprintf("https://example%d.com", i), fmt.Sprintf("example%d.com", i), "allow")
	}

	handler := handleAdminLogs(eng, authSvc)
	req := httptest.NewRequest("GET", "/api/admin/logs", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	body, err := io.ReadAll(w.Body)
	if err != nil {
		t.Fatalf("failed to read response: %v", err)
	}

	var resp []requestLogResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if len(resp) != 50 {
		t.Errorf("expected default limit of 50, got %d", len(resp))
	}
}

// TestHandleAdminLogs_CustomLimit verifies that a custom limit is respected.
func TestHandleAdminLogs_CustomLimit(t *testing.T) {
	eng := newTestEngine(t)
	authSvc := newTestAuthSvc(t)
	token := loginAndGetToken(t, authSvc)

	// Insert 30 logs.
	for i := 0; i < 30; i++ {
		seedLog(t, eng, fmt.Sprintf("https://example%d.com", i), fmt.Sprintf("example%d.com", i), "allow")
	}

	handler := handleAdminLogs(eng, authSvc)
	req := httptest.NewRequest("GET", "/api/admin/logs?limit=10", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	body, err := io.ReadAll(w.Body)
	if err != nil {
		t.Fatalf("failed to read response: %v", err)
	}

	var resp []requestLogResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if len(resp) != 10 {
		t.Errorf("expected custom limit of 10, got %d", len(resp))
	}
}

// TestHandleAdminLogs_LimitClamped_AboveMax verifies that limit above max is clamped to 500.
func TestHandleAdminLogs_LimitClamped_AboveMax(t *testing.T) {
	eng := newTestEngine(t)
	authSvc := newTestAuthSvc(t)
	token := loginAndGetToken(t, authSvc)

	// Insert 600 logs.
	for i := 0; i < 600; i++ {
		seedLog(t, eng, fmt.Sprintf("https://example%d.com", i), fmt.Sprintf("example%d.com", i), "allow")
	}

	handler := handleAdminLogs(eng, authSvc)
	req := httptest.NewRequest("GET", "/api/admin/logs?limit=1000", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	body, err := io.ReadAll(w.Body)
	if err != nil {
		t.Fatalf("failed to read response: %v", err)
	}

	var resp []requestLogResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if len(resp) != 500 {
		t.Errorf("expected limit clamped to 500, got %d", len(resp))
	}
}

// TestHandleAdminLogs_LimitClamped_BelowMin verifies that limit < 1 is clamped to 1.
func TestHandleAdminLogs_LimitClamped_BelowMin(t *testing.T) {
	eng := newTestEngine(t)
	authSvc := newTestAuthSvc(t)
	token := loginAndGetToken(t, authSvc)

	seedLog(t, eng, "https://example.com", "example.com", "allow")

	handler := handleAdminLogs(eng, authSvc)
	req := httptest.NewRequest("GET", "/api/admin/logs?limit=0", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	body, err := io.ReadAll(w.Body)
	if err != nil {
		t.Fatalf("failed to read response: %v", err)
	}

	var resp []requestLogResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if len(resp) != 1 {
		t.Errorf("expected limit clamped to 1, got %d", len(resp))
	}
}

// TestHandleAdminLogs_OffsetSkipsRows verifies that offset correctly skips rows.
func TestHandleAdminLogs_OffsetSkipsRows(t *testing.T) {
	eng := newTestEngine(t)
	authSvc := newTestAuthSvc(t)
	token := loginAndGetToken(t, authSvc)

	// Insert 5 logs (IDs 1-5).
	for i := 1; i <= 5; i++ {
		seedLog(t, eng, fmt.Sprintf("https://example%d.com", i), fmt.Sprintf("example%d.com", i), "allow")
	}

	handler := handleAdminLogs(eng, authSvc)
	req := httptest.NewRequest("GET", "/api/admin/logs?limit=100&offset=2", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	body, err := io.ReadAll(w.Body)
	if err != nil {
		t.Fatalf("failed to read response: %v", err)
	}

	var resp []requestLogResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if len(resp) != 3 {
		t.Errorf("expected 3 rows after offset 2, got %d", len(resp))
	}
}

// TestHandleAdminLogs_NegativeOffset_TreatedAsZero verifies that negative offset is clamped to 0.
func TestHandleAdminLogs_NegativeOffset_TreatedAsZero(t *testing.T) {
	eng := newTestEngine(t)
	authSvc := newTestAuthSvc(t)
	token := loginAndGetToken(t, authSvc)

	seedLog(t, eng, "https://example.com", "example.com", "allow")

	handler := handleAdminLogs(eng, authSvc)
	req := httptest.NewRequest("GET", "/api/admin/logs?offset=-1", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	body, err := io.ReadAll(w.Body)
	if err != nil {
		t.Fatalf("failed to read response: %v", err)
	}

	var resp []requestLogResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if len(resp) != 1 {
		t.Errorf("expected negative offset to be treated as 0, got %d rows", len(resp))
	}
}

// TestHandleAdminLogs_ClientIpNullable_NullStoredAsJSONNull verifies that NULL ClientIp is returned as JSON null.
func TestHandleAdminLogs_ClientIpNullable_NullStoredAsJSONNull(t *testing.T) {
	eng := newTestEngine(t)
	authSvc := newTestAuthSvc(t)
	token := loginAndGetToken(t, authSvc)

	// Seed a log with no ClientIP.
	seedLogWithClientIP(t, eng, "https://example.com", "example.com", "allow", "")

	handler := handleAdminLogs(eng, authSvc)
	req := httptest.NewRequest("GET", "/api/admin/logs", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	body, err := io.ReadAll(w.Body)
	if err != nil {
		t.Fatalf("failed to read response: %v", err)
	}

	var resp []requestLogResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if len(resp) != 1 {
		t.Fatalf("expected 1 log")
	}

	if resp[0].ClientIp != nil {
		t.Errorf("expected ClientIp to be nil for NULL in DB, got %v", *resp[0].ClientIp)
	}
}

// TestHandleAdminLogs_ClientIpPresent_ReturnedAsString verifies that a non-NULL ClientIp is returned as a string.
func TestHandleAdminLogs_ClientIpPresent_ReturnedAsString(t *testing.T) {
	eng := newTestEngine(t)
	authSvc := newTestAuthSvc(t)
	token := loginAndGetToken(t, authSvc)

	seedLogWithClientIP(t, eng, "https://example.com", "example.com", "allow", "10.0.0.1")

	handler := handleAdminLogs(eng, authSvc)
	req := httptest.NewRequest("GET", "/api/admin/logs", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	body, err := io.ReadAll(w.Body)
	if err != nil {
		t.Fatalf("failed to read response: %v", err)
	}

	var resp []requestLogResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if len(resp) != 1 {
		t.Fatalf("expected 1 log")
	}

	if resp[0].ClientIp == nil {
		t.Error("expected ClientIp to be non-nil")
	} else if *resp[0].ClientIp != "10.0.0.1" {
		t.Errorf("expected ClientIp '10.0.0.1', got %q", *resp[0].ClientIp)
	}
}

// TestHandleAdminLogs_NoJWT_Returns401 verifies that missing JWT returns 401.
func TestHandleAdminLogs_NoJWT_Returns401(t *testing.T) {
	eng := newTestEngine(t)
	authSvc := newTestAuthSvc(t)

	handler := handleAdminLogs(eng, authSvc)
	req := httptest.NewRequest("GET", "/api/admin/logs", nil)
	// No Authorization header
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected status 401, got %d", w.Code)
	}
}

// TestHandleAdminLogs_AllowedField_BooleanInJSON verifies that the Allowed field is a boolean in JSON.
func TestHandleAdminLogs_AllowedField_BooleanInJSON(t *testing.T) {
	eng := newTestEngine(t)
	authSvc := newTestAuthSvc(t)
	token := loginAndGetToken(t, authSvc)

	seedLog(t, eng, "https://example.com", "example.com", "allow")

	handler := handleAdminLogs(eng, authSvc)
	req := httptest.NewRequest("GET", "/api/admin/logs", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	body, err := io.ReadAll(w.Body)
	if err != nil {
		t.Fatalf("failed to read response: %v", err)
	}

	// Verify JSON contains "allowed":true
	if !strings.Contains(string(body), `"allowed":true`) {
		t.Errorf("expected boolean 'allowed' field in JSON, got: %s", string(body))
	}
}

// === parseQueryInt Tests ===

// TestParseQueryInt_KeyPresent_ValidInt verifies that a valid integer is parsed correctly.
func TestParseQueryInt_KeyPresent_ValidInt(t *testing.T) {
	req := httptest.NewRequest("GET", "/test?limit=25", nil)
	result := parseQueryInt(req, "limit", 50)
	if result != 25 {
		t.Errorf("expected 25, got %d", result)
	}
}

// TestParseQueryInt_KeyAbsent_ReturnsDefault verifies that the default is returned when the key is absent.
func TestParseQueryInt_KeyAbsent_ReturnsDefault(t *testing.T) {
	req := httptest.NewRequest("GET", "/test", nil)
	result := parseQueryInt(req, "limit", 50)
	if result != 50 {
		t.Errorf("expected 50, got %d", result)
	}
}

// TestParseQueryInt_KeyPresentNonNumeric_ReturnsDefault verifies that the default is returned for non-numeric values.
func TestParseQueryInt_KeyPresentNonNumeric_ReturnsDefault(t *testing.T) {
	req := httptest.NewRequest("GET", "/test?limit=abc", nil)
	result := parseQueryInt(req, "limit", 50)
	if result != 50 {
		t.Errorf("expected default 50 for non-numeric value, got %d", result)
	}
}

// TestParseQueryInt_KeyPresentEmpty_ReturnsDefault verifies that the default is returned for empty string values.
func TestParseQueryInt_KeyPresentEmpty_ReturnsDefault(t *testing.T) {
	req := httptest.NewRequest("GET", "/test?limit=", nil)
	result := parseQueryInt(req, "limit", 50)
	if result != 50 {
		t.Errorf("expected default 50 for empty value, got %d", result)
	}
}

// === Helper functions for seeding data ===

// seedPolicySite inserts a site policy into the database and returns its ID.
func seedPolicySite(t *testing.T, eng *policy.Engine, hostPattern string, mode db.ViewMode) int64 {
	t.Helper()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	res, err := eng.SQLDB().Exec(
		`INSERT INTO SitePolicies (HostPattern, ViewMode, CreatedAt, UpdatedAt)
		 VALUES (?, ?, ?, ?)`,
		hostPattern, int(mode), now, now,
	)
	if err != nil {
		t.Fatalf("seedPolicySite: %v", err)
	}
	id, _ := res.LastInsertId()
	return id
}

// seedLog inserts a request log with default values.
func seedLog(t *testing.T, eng *policy.Engine, rawURL, host, decision string) {
	t.Helper()
	seedLogWithClientIP(t, eng, rawURL, host, decision, "127.0.0.1")
}

// seedLogWithClientIP inserts a request log with a specified ClientIP.
func seedLogWithClientIP(t *testing.T, eng *policy.Engine, rawURL, host, decision string, clientIP string) {
	t.Helper()
	allowed := decision != "deny"
	if err := policy.WriteRequestLog(eng.SQLDB(), rawURL, host, decision, allowed, clientIP); err != nil {
		t.Fatalf("seedLog: %v", err)
	}
}
