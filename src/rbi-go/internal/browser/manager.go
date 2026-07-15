// Package browser implements the headless Chromium session manager for rbi-go.
// It drives a headless Chromium process per isolated session via chromedp (a
// pure-Go CDP client), providing the same per-session isolation as the C#
// HeadlessBrowserSessionManager (one fresh IBrowserContext per session via
// NewContextAsync).
//
// Architecture note: Chrome 150 (new headless mode) does not support
// Target.createTarget within a Target.createBrowserContext-created context
// ("Failed to open new tab - no browser is open", CDP -32000). As a result,
// the original design of one shared Chrome process with per-session isolated
// BrowserContexts via chromedp.WithNewBrowserContext cannot be used. Instead,
// each Session gets its own short-lived Chrome process. Isolation is therefore
// at the process level (stronger than browser-context level) — no cross-session
// cookie or storage leakage is possible.
//
// Lifetime separation: Chrome's exec.CommandContext uses sessCtx (not setupCtx)
// so that cancelling the per-call setup context after navigation completes does
// not kill the Chrome process. The browser is allocated in a dedicated first Run
// call with sessCtx; subsequent Run calls for navigation actions are issued with
// a child setupCtx and skip re-allocation (c.Browser != nil path in chromedp).
package browser

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"strings"

	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"

	"rbi-go/internal/config"
)

// Session holds one isolated headless browser session: a dedicated Chromium
// process (one per session) with a tab navigated to the target URL. Part 11
// uses the Context field to issue CDP screencast and input dispatch commands
// without touching other concurrent sessions (which run in entirely separate
// processes).
type Session struct {
	// Context is the chromedp context for this session's tab. It is backed by
	// the session-owned Chrome process. Part 11 calls chromedp.Run(Context, ...)
	// to drive Page.startScreencast and Input.dispatchMouseEvent /
	// Input.dispatchKeyEvent.
	Context context.Context
	cancel  context.CancelFunc

	// allocCancel cancels the chromedp ExecAllocator context for this session's
	// Chrome process, which causes chromedp to send SIGKILL to Chrome and wait
	// for it to exit. Called in Close after the tab-level context is cancelled.
	allocCancel context.CancelFunc

	// userDataDir is the temporary directory created exclusively for this
	// session's Chrome process. Removing it in Close prevents leftover profile
	// lock files from interfering with the next session's Chrome launch.
	userDataDir string

	// TargetURL is the URL the page was navigated to at session creation.
	TargetURL string
	// ViewportW and ViewportH are the emulated viewport dimensions in pixels.
	// They match the screencast frame size so client canvas coordinates map 1:1
	// to page coordinates (mirrors Playwright ViewportSize in the C# equivalent).
	ViewportW int
	ViewportH int
}

// Close cancels the session's tab context, shuts down the session's dedicated
// Chromium process, and removes the temporary user-data-dir. It is safe to
// call multiple times because context cancels are idempotent and os.RemoveAll
// on a non-existent path is a no-op.
func (s *Session) Close() {
	s.cancel()
	// allocCancel is chromedp's cancelWait: it kills Chrome and blocks until
	// the process has fully exited. Calling it AFTER s.cancel() allows the
	// tab context's cleanup goroutine to fire first, giving Chrome a moment
	// to process the tab close before the process is forcibly killed.
	s.allocCancel()
	if s.userDataDir != "" {
		os.RemoveAll(s.userDataDir)
	}
}

// Manager is a factory for isolated headless-browser sessions. It validates
// the Chromium binary path at construction time; the actual Chrome process is
// started per-session in CreateSession, not at Manager creation.
//
// Manager is intended to be constructed once at server startup (via NewManager)
// and shared for all video-mode sessions. Close is a no-op but is provided for
// symmetry with the C# HeadlessBrowserSessionManager interface.
type Manager struct {
	cfg *config.BrowserConfig
}

// NewManager validates the Chromium binary and returns a Manager ready to
// create sessions. Returns an error if the Chromium binary cannot be found.
// Unlike the C# equivalent, no Chrome process is started at Manager creation;
// Chrome processes are started per-session in CreateSession.
func NewManager(cfg *config.BrowserConfig) (*Manager, error) {
	// Validate the Chromium binary path now so that callers get an early,
	// actionable error rather than a cryptic CDP failure at session-creation
	// time. chromedp's own ExecPath lookup also resolves the path, but it
	// produces a lower-level "no such file" error that is harder to diagnose.
	if err := validateChromiumBinary(cfg); err != nil {
		return nil, err
	}

	slog.Info("browser: Chromium binary validated, Manager ready")

	return &Manager{cfg: cfg}, nil
}

// validateChromiumBinary checks that the Chromium binary is reachable. If
// cfg.ChromiumPath is set, the path must exist on disk. If it is empty,
// chromedp's auto-detection (which searches PATH for common names such as
// "chromium", "chromium-browser", and "google-chrome") is used.
func validateChromiumBinary(cfg *config.BrowserConfig) error {
	if cfg.ChromiumPath != "" {
		// Absolute or relative path explicitly provided — verify it exists.
		if _, err := os.Stat(cfg.ChromiumPath); err != nil {
			return fmt.Errorf("browser: ChromiumPath %q: %w", cfg.ChromiumPath, err)
		}
		return nil
	}
	// No explicit path: replicate chromedp's own fallback search so that
	// NewManager fails fast with a clear error instead of waiting until the
	// first CreateSession call.
	for _, name := range []string{"chromium", "chromium-browser", "google-chrome", "google-chrome-stable"} {
		if _, err := exec.LookPath(name); err == nil {
			return nil
		}
	}
	return fmt.Errorf("browser: no Chromium binary found in PATH (tried chromium, chromium-browser, google-chrome, google-chrome-stable)")
}

// Close is a no-op for Manager because no Chrome process is started at the
// Manager level. Each Session owns its Chrome process and cleans it up in
// Session.Close. Close is provided for symmetry with the C# interface and is
// safe to call multiple times.
func (m *Manager) Close() {}

// CreateSession launches a dedicated headless Chromium process for this
// session, navigates its single tab to targetURL (waiting for DOMContentLoaded),
// and returns a Session ready for Part 11's CDP screencast and input commands.
//
// Each call starts a fresh Chrome process with a unique --user-data-dir so
// that sessions are fully isolated at the process level: cookies, localStorage,
// sessionStorage, and cached credentials cannot leak between sessions.
//
// The caller's ctx bounds only the setup (navigation) phase. The returned
// Session's lifetime is independent of ctx and ends only when Session.Close
// is called. Call Session.Close when the associated WebRTC session ends to
// terminate the Chrome process and release the user-data-dir.
//
// Returns a Session whose Context field is ready for Part 11 to issue
// Page.startScreencast / Input.dispatch* CDP commands.
func (m *Manager) CreateSession(ctx context.Context, viewportWidth, viewportHeight int, targetURL string) (*Session, error) {
	// Reject non-HTTP(S) URLs before starting Chrome. Chrome 150 headless
	// serves file:// and other schemes without navigation errors, so without
	// this check a malicious or misconfigured policy could expose the host
	// filesystem via the screencast stream.
	if !strings.HasPrefix(targetURL, "http://") && !strings.HasPrefix(targetURL, "https://") {
		return nil, fmt.Errorf("browser: unsupported URL scheme in %q (only http and https are allowed)", targetURL)
	}

	// Create a unique temporary directory for this session's Chrome process.
	// Each Chrome instance writes a SingletonLock file into its user-data-dir;
	// a fresh directory per session ensures no lock conflicts between concurrent
	// or sequential sessions.
	tmpDir, err := os.MkdirTemp("", "rbi-chromium-*")
	if err != nil {
		return nil, fmt.Errorf("browser: create user-data-dir: %w", err)
	}

	// Build ExecAllocator options from the chromedp defaults, then add flags
	// required for container / root-user environments.
	//
	// We deliberately copy defaults into a fresh slice rather than appending to
	// DefaultExecAllocatorOptions[:] so that concurrent calls do not share a
	// backing array: append(slice-of-len==cap, ...) always allocates a new array,
	// but with a different capacity it can silently alias the original in Go.
	//
	// DefaultExecAllocatorOptions already includes --headless, --disable-dev-shm-usage,
	// and a set of automation-friendly defaults (matching Puppeteer's baseline).
	//
	// --no-sandbox: Required when Chromium runs as root (common in Docker).
	//   Chrome's SUID/namespace sandbox cannot operate as UID 0 and exits with
	//   SIGSEGV on the sandbox helper without this flag.
	//
	// --user-data-dir: Unique per session; see tmpDir comment above.
	defaults := chromedp.DefaultExecAllocatorOptions
	opts := make([]chromedp.ExecAllocatorOption, len(defaults), len(defaults)+3)
	copy(opts, defaults[:])
	opts = append(opts, chromedp.Flag("no-sandbox", true))
	opts = append(opts, chromedp.UserDataDir(tmpDir))
	if m.cfg.ChromiumPath != "" {
		opts = append(opts, chromedp.ExecPath(m.cfg.ChromiumPath))
	}

	allocCtx, allocCancel := chromedp.NewExecAllocator(context.Background(), opts...)

	// Create the session's tab context. This is the first (and only) child of
	// allocCtx, so chromedp will attach it to the first page that Chrome opens.
	// c.first == true here, so chromedp waits for Chrome's initial "about:blank"
	// tab rather than trying Target.createBrowserContext (which is broken in
	// Chrome 150 new headless mode).
	sessCtx, sessCancel := chromedp.NewContext(allocCtx)

	// Track whether setup succeeds so the cleanup defer can decide what to do.
	setupOK := false
	defer func() {
		if !setupOK {
			sessCancel()
			allocCancel()
			os.RemoveAll(tmpDir)
		}
	}()

	// Phase 1: allocate the Chrome process using sessCtx as the lifetime context.
	//
	// CRITICAL: we must NOT use setupCtx (the caller-timeout context) for this
	// first Run call. chromedp.Run passes its ctx argument to ExecAllocator.Allocate,
	// which uses exec.CommandContext(ctx, chromium, ...) to start Chrome. If we
	// used setupCtx here, Chrome's OS process would be tied to setupCtx. When
	// setupCtx is cancelled at the end of CreateSession (deferred), Chrome would
	// receive SIGKILL, the browser's WebSocket connection would drop, and chromedp
	// would call c.cancel() on sessCtx — cancelling Session.Context the moment
	// CreateSession returns. By using sessCtx here, Chrome lives until
	// Session.Close() calls sessCancel().
	//
	// Since c.Browser is nil, this Run also sets c.Target (attaches to Chrome's
	// initial about:blank tab). All subsequent Run calls skip re-allocation.
	if err = chromedp.Run(sessCtx); err != nil {
		return nil, fmt.Errorf("browser: allocate Chrome for %q: %w", targetURL, err)
	}

	// Phase 2: run setup actions (viewport + navigation) with a cancellable
	// child context so that the caller's ctx deadline/cancellation aborts the
	// navigation without killing the Chrome process (which is now tied to sessCtx).
	setupCtx, setupCancel := context.WithCancel(sessCtx)
	defer setupCancel()
	go func() {
		select {
		case <-ctx.Done():
			setupCancel() // propagate caller's timeout/cancellation into navigation
		case <-setupCtx.Done():
			// setup finished or session closed — goroutine exits cleanly
		}
	}()

	if err = runSessionSetup(setupCtx, viewportWidth, viewportHeight, targetURL); err != nil {
		return nil, fmt.Errorf("browser: session setup for %q: %w", targetURL, err)
	}

	setupOK = true

	slog.Info("browser: session ready",
		"url", targetURL,
		"viewport", fmt.Sprintf("%dx%d", viewportWidth, viewportHeight),
	)

	return &Session{
		Context:     sessCtx,
		cancel:      sessCancel,
		allocCancel: allocCancel,
		userDataDir: tmpDir,
		TargetURL:   targetURL,
		ViewportW:   viewportWidth,
		ViewportH:   viewportHeight,
	}, nil
}

// runSessionSetup sets the emulated viewport, enables CDP lifecycle events,
// starts navigation, and waits for DOMContentLoaded.
//
// DOMContentLoaded (not full "load") is intentional and matches the C# equivalent
// (HeadlessBrowserSessionManager.CreateSessionAsync with WaitUntilState.DOMContentLoaded).
// Video-mode sites are risky/untrusted and often have slow subresources (ads,
// trackers, redirect chains). Waiting for full "load" would block the first
// screencast frame on all of that; CDP screencast (started by Part 11 right
// after this returns) captures whatever is painted at DOMContentLoaded, and VP8
// delta frames update the client as the page continues loading.
func runSessionSetup(ctx context.Context, width, height int, targetURL string) error {
	// Buffered channel receives the DOMContentLoaded signal. Buffer size 1
	// prevents the listener from blocking if the select below is slow.
	domReady := make(chan struct{}, 1)

	// Register the lifecycle event listener BEFORE any CDP commands to guarantee
	// we cannot miss DOMContentLoaded even if the page loads very quickly.
	chromedp.ListenTarget(ctx, func(ev any) {
		if lifecycle, ok := ev.(*page.EventLifecycleEvent); ok {
			if lifecycle.Name == "DOMContentLoaded" {
				select {
				case domReady <- struct{}{}:
				default:
				}
			}
		}
	})

	// Set viewport, enable lifecycle events, and start navigation in a single Run.
	//
	// EmulateViewport: sets the emulated display size so that the page's layout
	// matches the requested dimensions (required for correct screencast framing and
	// 1:1 canvas-to-page coordinate mapping on the client).
	//
	// SetLifecycleEventsEnabled: activates the CDP Page.lifecycleEvent stream so
	// the listener above receives DOMContentLoaded.
	//
	// page.Navigate: issues the CDP Page.navigate command, which returns once the
	// navigation response is received — NOT after any DOM event. DOMContentLoaded
	// is delivered asynchronously via the listener registered above.
	if err := chromedp.Run(ctx,
		chromedp.EmulateViewport(int64(width), int64(height)),
		page.SetLifecycleEventsEnabled(true),
		chromedp.ActionFunc(func(ctx context.Context) error {
			// page.Navigate returns (frameID, loaderID, errorText, isDownload, err).
			// The Go err covers only CDP protocol failures; navigation-level failures
			// (e.g. net::ERR_NAME_NOT_RESOLVED) are reported via errorText while CDP
			// itself succeeds and Chrome serves a built-in error page. We must check
			// both so CreateSession does not falsely succeed on an unreachable target.
			_, _, errorText, _, err := page.Navigate(targetURL).Do(ctx)
			if err != nil {
				return err
			}
			if errorText != "" {
				return fmt.Errorf("navigate to %q: %s", targetURL, errorText)
			}
			return nil
		}),
	); err != nil {
		return fmt.Errorf("navigate to %q: %w", targetURL, err)
	}

	// Block until DOMContentLoaded fires or the context is cancelled.
	select {
	case <-domReady:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("waiting for DOMContentLoaded: %w", ctx.Err())
	}
}
