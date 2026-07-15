package middleware

import (
	"bytes"
	"crypto/tls"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestRequestLogger_CallsNextHandler verifies that the middleware calls the next handler.
func TestRequestLogger_CallsNextHandler(t *testing.T) {
	// Save and restore default logger
	oldLogger := slog.Default()
	defer slog.SetDefault(oldLogger)

	// Set up logger with buffer
	logBuffer := &bytes.Buffer{}
	logger := slog.New(slog.NewTextHandler(logBuffer, nil))
	slog.SetDefault(logger)

	nextHandlerCalled := false
	nextHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		nextHandlerCalled = true
		w.WriteHeader(http.StatusOK)
	})

	middleware := RequestLogger(nextHandler)

	req := httptest.NewRequest("GET", "http://localhost:5000/test", nil)
	w := httptest.NewRecorder()

	middleware.ServeHTTP(w, req)

	if !nextHandlerCalled {
		t.Error("Expected next handler to be called, but it was not")
	}
}

// TestRequestLogger_ForwardsResponseStatus verifies that the response status from the next handler is preserved.
func TestRequestLogger_ForwardsResponseStatus(t *testing.T) {
	// Save and restore default logger
	oldLogger := slog.Default()
	defer slog.SetDefault(oldLogger)

	// Set up logger with buffer
	logBuffer := &bytes.Buffer{}
	logger := slog.New(slog.NewTextHandler(logBuffer, nil))
	slog.SetDefault(logger)

	nextHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
	})

	middleware := RequestLogger(nextHandler)

	req := httptest.NewRequest("POST", "http://localhost:5000/api/create", nil)
	w := httptest.NewRecorder()

	middleware.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Errorf("Expected status 201, got %d", w.Code)
	}
}

// TestRequestLogger_LogsMethod verifies that the HTTP method is included in the log.
func TestRequestLogger_LogsMethod(t *testing.T) {
	// Save and restore default logger
	oldLogger := slog.Default()
	defer slog.SetDefault(oldLogger)

	// Set up logger with buffer
	logBuffer := &bytes.Buffer{}
	logger := slog.New(slog.NewTextHandler(logBuffer, nil))
	slog.SetDefault(logger)

	nextHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	middleware := RequestLogger(nextHandler)

	req := httptest.NewRequest("DELETE", "http://localhost:5000/resource", nil)
	w := httptest.NewRecorder()

	middleware.ServeHTTP(w, req)

	logOutput := logBuffer.String()
	if !strings.Contains(logOutput, "DELETE") {
		t.Errorf("Expected 'DELETE' in log output, got: %s", logOutput)
	}
}

// TestRequestLogger_LogsFullURL verifies that the full URL (including scheme, host, path) is logged.
func TestRequestLogger_LogsFullURL(t *testing.T) {
	// Save and restore default logger
	oldLogger := slog.Default()
	defer slog.SetDefault(oldLogger)

	// Set up logger with buffer
	logBuffer := &bytes.Buffer{}
	logger := slog.New(slog.NewTextHandler(logBuffer, nil))
	slog.SetDefault(logger)

	nextHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	middleware := RequestLogger(nextHandler)

	req := httptest.NewRequest("GET", "http://example.com:9000/api/v1/resource?key=value", nil)
	w := httptest.NewRecorder()

	middleware.ServeHTTP(w, req)

	logOutput := logBuffer.String()
	// Should contain the full URL
	if !strings.Contains(logOutput, "example.com:9000") || !strings.Contains(logOutput, "/api/v1/resource") {
		t.Errorf("Expected full URL in log output, got: %s", logOutput)
	}
}

// TestRequestLogger_HTTPSScheme verifies that HTTPS URLs use "https://" scheme in logs.
func TestRequestLogger_HTTPSScheme(t *testing.T) {
	// Save and restore default logger
	oldLogger := slog.Default()
	defer slog.SetDefault(oldLogger)

	// Set up logger with buffer
	logBuffer := &bytes.Buffer{}
	logger := slog.New(slog.NewTextHandler(logBuffer, nil))
	slog.SetDefault(logger)

	nextHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	middleware := RequestLogger(nextHandler)

	req := httptest.NewRequest("GET", "https://localhost:5000/secure", nil)
	// Set TLS to indicate HTTPS
	req.TLS = &tls.ConnectionState{}
	w := httptest.NewRecorder()

	middleware.ServeHTTP(w, req)

	logOutput := logBuffer.String()
	if !strings.Contains(logOutput, "https://") {
		t.Errorf("Expected 'https://' in log output for HTTPS request, got: %s", logOutput)
	}
}

// TestRequestLogger_LogsTimestamp verifies that a timestamp is included in the log output.
func TestRequestLogger_LogsTimestamp(t *testing.T) {
	// Save and restore default logger
	oldLogger := slog.Default()
	defer slog.SetDefault(oldLogger)

	// Set up logger with buffer
	logBuffer := &bytes.Buffer{}
	logger := slog.New(slog.NewTextHandler(logBuffer, nil))
	slog.SetDefault(logger)

	nextHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	middleware := RequestLogger(nextHandler)

	req := httptest.NewRequest("GET", "http://localhost:5000/test", nil)
	w := httptest.NewRecorder()

	middleware.ServeHTTP(w, req)

	logOutput := logBuffer.String()
	// Should contain timestamp (in RFC3339Nano format: YYYY-MM-DDTHH:MM:SS.nnnnnnnnnZ)
	if !strings.Contains(logOutput, "T") || !strings.Contains(logOutput, "Z") {
		t.Errorf("Expected RFC3339Nano timestamp in log output, got: %s", logOutput)
	}
}
