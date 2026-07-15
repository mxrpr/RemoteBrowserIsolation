package browser

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"testing"
	"time"

	"rbi-go/internal/config"
)

// skipIfNoChromium resolves the Chromium binary via exec.LookPath("chromium"),
// then exec.LookPath("chromium-browser"), then RBI_BROWSER_CHROMIUM_PATH env var.
// If none are found, t.Skip is called. Returns a *config.BrowserConfig ready for
// NewManager if the binary is found.
func skipIfNoChromium(t *testing.T) *config.BrowserConfig {
	t.Helper()

	// Try to find chromium in PATH
	if _, err := exec.LookPath("chromium"); err == nil {
		return &config.BrowserConfig{ChromiumPath: ""}
	}

	// Try chromium-browser in PATH
	if _, err := exec.LookPath("chromium-browser"); err == nil {
		return &config.BrowserConfig{ChromiumPath: ""}
	}

	// Try RBI_BROWSER_CHROMIUM_PATH env var
	if path := os.Getenv("RBI_BROWSER_CHROMIUM_PATH"); path != "" {
		if _, err := os.Stat(path); err == nil {
			return &config.BrowserConfig{ChromiumPath: path}
		}
	}

	t.Skip("Chromium binary not found (checked chromium, chromium-browser, RBI_BROWSER_CHROMIUM_PATH)")
	return nil
}

// newTestManager calls skipIfNoChromium, creates a Manager via NewManager,
// calls t.Fatal on error, and registers t.Cleanup(m.Close).
func newTestManager(t *testing.T) *Manager {
	t.Helper()

	cfg := skipIfNoChromium(t)
	m, err := NewManager(cfg)
	if err != nil {
		t.Fatalf("newTestManager: NewManager failed: %v", err)
	}
	t.Cleanup(func() { m.Close() })
	return m
}

// === Manager Creation Tests ===

// TestNewManager_HappyPath_ReturnsNonNilManager verifies that NewManager with
// a valid Chromium binary and empty ChromiumPath returns a non-nil Manager.
func TestNewManager_HappyPath_ReturnsNonNilManager(t *testing.T) {
	cfg := skipIfNoChromium(t)
	m, err := NewManager(cfg)
	if err != nil {
		t.Fatalf("NewManager failed: %v", err)
	}
	defer m.Close()

	if m == nil {
		t.Error("expected non-nil Manager")
	}
}

// TestNewManager_HappyPath_CloseIsIdempotent verifies that calling Manager.Close
// multiple times does not panic.
func TestNewManager_HappyPath_CloseIsIdempotent(t *testing.T) {
	cfg := skipIfNoChromium(t)
	m, err := NewManager(cfg)
	if err != nil {
		t.Fatalf("NewManager failed: %v", err)
	}

	// Close multiple times; should not panic
	m.Close()
	m.Close()
	m.Close()
}

// TestNewManager_BadChromiumPath_ReturnsError verifies that NewManager with
// an invalid ChromiumPath returns an error. This test does not require Chromium
// to be installed.
func TestNewManager_BadChromiumPath_ReturnsError(t *testing.T) {
	cfg := &config.BrowserConfig{
		ChromiumPath: "/this/path/does/not/exist/chromium",
	}
	m, err := NewManager(cfg)

	if err == nil {
		t.Error("expected error with invalid ChromiumPath")
		if m != nil {
			m.Close()
		}
	}
	if m != nil {
		t.Error("expected nil Manager with error")
	}
}

// === Session Creation Tests ===

// TestCreateSession_HappyPath_ReturnsNonNilSession verifies that CreateSession
// with a valid URL and reasonable timeout returns a non-nil Session without error.
func TestCreateSession_HappyPath_ReturnsNonNilSession(t *testing.T) {
	m := newTestManager(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	sess, err := m.CreateSession(ctx, 1280, 720, "http://example.com")

	if err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}
	if sess == nil {
		t.Fatal("expected non-nil Session")
	}
	sess.Close()
}

// TestCreateSession_SessionFields_StoredCorrectly verifies that CreateSession
// stores the target URL and viewport dimensions correctly in the returned Session.
func TestCreateSession_SessionFields_StoredCorrectly(t *testing.T) {
	m := newTestManager(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	targetURL := "http://example.com"
	viewportW := 1280
	viewportH := 720

	sess, err := m.CreateSession(ctx, viewportW, viewportH, targetURL)
	if err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}
	defer sess.Close()

	if sess.TargetURL != targetURL {
		t.Errorf("expected TargetURL %q, got %q", targetURL, sess.TargetURL)
	}
	if sess.ViewportW != viewportW {
		t.Errorf("expected ViewportW %d, got %d", viewportW, sess.ViewportW)
	}
	if sess.ViewportH != viewportH {
		t.Errorf("expected ViewportH %d, got %d", viewportH, sess.ViewportH)
	}
}

// TestCreateSession_SessionContext_NonNil verifies that the returned Session's
// Context field is non-nil.
func TestCreateSession_SessionContext_NonNil(t *testing.T) {
	m := newTestManager(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	sess, err := m.CreateSession(ctx, 1280, 720, "http://example.com")
	if err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}
	defer sess.Close()

	if sess.Context == nil {
		t.Error("expected Session.Context to be non-nil")
	}
}

// TestCreateSession_SessionContext_NotYetCancelled verifies that the returned
// Session's Context is not yet cancelled (no error when checked immediately).
func TestCreateSession_SessionContext_NotYetCancelled(t *testing.T) {
	m := newTestManager(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	sess, err := m.CreateSession(ctx, 1280, 720, "http://example.com")
	if err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}
	defer sess.Close()

	if sess.Context.Err() != nil {
		t.Errorf("expected Session.Context not cancelled immediately after creation, got error: %v", sess.Context.Err())
	}
}

// === Session Close Tests ===

// TestSession_Close_CancelsSessionContext verifies that calling Session.Close
// cancels the session's Context (Err() returns non-nil).
func TestSession_Close_CancelsSessionContext(t *testing.T) {
	m := newTestManager(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	sess, err := m.CreateSession(ctx, 1280, 720, "http://example.com")
	if err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}

	sess.Close()

	// Poll with a timeout to ensure the context is cancelled
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if sess.Context.Err() != nil {
			return // Context is cancelled as expected
		}
		time.Sleep(100 * time.Millisecond)
	}

	t.Error("expected Session.Context to be cancelled after Close")
}

// TestSession_Close_DoesNotAffectManagerOrSubsequentSessions verifies that closing
// one session does not affect the Manager or prevent creating new sessions.
func TestSession_Close_DoesNotAffectManagerOrSubsequentSessions(t *testing.T) {
	m := newTestManager(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Create first session
	sess1, err := m.CreateSession(ctx, 1280, 720, "http://example.com")
	if err != nil {
		t.Fatalf("CreateSession (1st) failed: %v", err)
	}

	// Close first session
	sess1.Close()

	// Create second session with a fresh timeout
	ctx2, cancel2 := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel2()

	sess2, err := m.CreateSession(ctx2, 1280, 720, "http://example.org")
	if err != nil {
		t.Fatalf("CreateSession (2nd) failed: %v", err)
	}
	defer sess2.Close()

	if sess2 == nil {
		t.Error("expected non-nil second session after closing first")
	}
}

// === Caller Context Tests ===

// TestCreateSession_CallerContextCancelled_ReturnsError verifies that CreateSession
// with a pre-cancelled context returns an error and nil session.
func TestCreateSession_CallerContextCancelled_ReturnsError(t *testing.T) {
	m := newTestManager(t)

	// Use a pre-cancelled context
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	sess, err := m.CreateSession(ctx, 1280, 720, "http://example.com")

	if err == nil {
		t.Error("expected error with cancelled context")
		if sess != nil {
			sess.Close()
		}
	}
	if sess != nil {
		t.Error("expected nil session with error from cancelled context")
		sess.Close()
	}
}

// TestCreateSession_CallerContextTimeout_ReturnsError verifies that CreateSession
// respects the caller's context timeout. It uses a local net.Listener that accepts
// but never responds to reliably force a timeout.
func TestCreateSession_CallerContextTimeout_ReturnsError(t *testing.T) {
	m := newTestManager(t)

	// Set up a local listener that accepts connections but never responds
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to create test listener: %v", err)
	}
	defer listener.Close()

	addr := listener.Addr().String()

	// Accept connections in the background (but never send a response)
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			conn.Close()
		}
	}()

	// Try to navigate to the listener with a very short timeout
	// (shorter than the default TCP dial timeout)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	sess, err := m.CreateSession(ctx, 1280, 720, fmt.Sprintf("http://%s", addr))

	if err == nil {
		t.Error("expected error with timeout context")
		if sess != nil {
			sess.Close()
		}
	}
	if sess != nil {
		t.Error("expected nil session with timeout error")
		sess.Close()
	}
}

// === Navigation Error Tests ===

// TestCreateSession_NavigationError_ERRNameNotResolved_ReturnsError verifies that
// CreateSession returns an error when navigating to a non-existent domain. This
// guards against the regression where navigation-level errors (errorText from
// page.Navigate) were silently swallowed.
func TestCreateSession_NavigationError_ERRNameNotResolved_ReturnsError(t *testing.T) {
	m := newTestManager(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	sess, err := m.CreateSession(ctx, 1280, 720, "http://this-domain-definitely-does-not-exist.invalid")

	if err == nil {
		t.Error("expected error navigating to non-existent domain")
		if sess != nil {
			sess.Close()
		}
	} else {
		// Verify error message mentions navigation or ERR_*
		errStr := err.Error()
		if errStr == "" {
			t.Error("expected non-empty error message")
		}
		// The error should contain either "ERR_" (Chrome error code) or "navigate"
		if !(containsAny(errStr, "ERR_", "navigate", "name")) {
			t.Errorf("expected error containing 'ERR_' or 'navigate' or 'name', got: %v", err)
		}
	}
	if sess != nil {
		t.Error("expected nil session with navigation error")
		sess.Close()
	}
}

// TestCreateSession_NavigationError_NonHTTPScheme_ReturnsError verifies that
// CreateSession returns an error when navigating to a non-HTTP scheme URL
// (like file:// or ftp://).
func TestCreateSession_NavigationError_NonHTTPScheme_ReturnsError(t *testing.T) {
	m := newTestManager(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	sess, err := m.CreateSession(ctx, 1280, 720, "file:///etc/passwd")

	if err == nil {
		t.Error("expected error navigating to file:// scheme")
		if sess != nil {
			sess.Close()
		}
	}
	if sess != nil {
		t.Error("expected nil session with file:// scheme error")
		sess.Close()
	}
}

// === Multiple Sessions Tests ===

// TestCreateSession_MultipleSessions_Independent verifies that two concurrent
// sessions to different URLs have independent, non-nil contexts and can be
// closed independently.
func TestCreateSession_MultipleSessions_Independent(t *testing.T) {
	m := newTestManager(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Create first session
	sess1, err := m.CreateSession(ctx, 1280, 720, "http://example.com")
	if err != nil {
		t.Fatalf("CreateSession (1st) failed: %v", err)
	}
	if sess1 == nil {
		t.Fatal("expected non-nil first session")
	}

	// Create second session
	sess2, err := m.CreateSession(ctx, 1280, 720, "http://example.org")
	if err != nil {
		t.Fatalf("CreateSession (2nd) failed: %v", err)
	}
	if sess2 == nil {
		t.Fatal("expected non-nil second session")
	}

	// Verify contexts are distinct and non-nil
	if sess1.Context == nil {
		t.Error("expected sess1.Context non-nil")
	}
	if sess2.Context == nil {
		t.Error("expected sess2.Context non-nil")
	}
	if sess1.Context == sess2.Context {
		t.Error("expected distinct contexts for sess1 and sess2")
	}

	// Close first session
	sess1.Close()

	// Verify first session's context is cancelled
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if sess1.Context.Err() != nil {
			break // Cancelled as expected
		}
		time.Sleep(100 * time.Millisecond)
	}
	if sess1.Context.Err() == nil {
		t.Error("expected sess1.Context cancelled after Close")
	}

	// Verify second session's context is still not cancelled
	if sess2.Context.Err() != nil {
		t.Error("expected sess2.Context still not cancelled after closing sess1")
	}

	// Close second session
	sess2.Close()
}

// === Helper Functions ===

// containsAny checks if s contains any of the given substrings.
func containsAny(s string, substrings ...string) bool {
	for _, substr := range substrings {
		if len(substr) > 0 && len(s) >= len(substr) {
			for i := 0; i <= len(s)-len(substr); i++ {
				if s[i:i+len(substr)] == substr {
					return true
				}
			}
		}
	}
	return false
}
