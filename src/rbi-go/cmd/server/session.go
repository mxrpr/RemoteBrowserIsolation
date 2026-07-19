package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"net"
	"net/http"
	"net/url"
	"sync"
	"sync/atomic"
	"time"

	"rbi-go/internal/browser"
	"rbi-go/internal/db"
	"rbi-go/internal/policy"
	"rbi-go/internal/settings"
	"rbi-go/internal/video"
	rtcmgr "rbi-go/internal/webrtc"

	pion "github.com/pion/webrtc/v3"
)

// offerRequest is the JSON body expected by POST /api/session/offer.
// Matches the C# OfferRequest record (camelCase via json struct tags).
// Width/Height are optional — omitting them uses defViewportWidth/Height.
type offerRequest struct {
	URL    string `json:"url"`
	SDP    string `json:"sdp"`
	Width  *int   `json:"width,omitempty"`
	Height *int   `json:"height,omitempty"`
}

// answerResponse is the JSON body returned on a successful offer exchange.
// Matches the C# AnswerResponse record.
type answerResponse struct {
	SDP string `json:"sdp"`
}

// Viewport bounds for client-requested dimensions. Lower bound guards
// degenerate values; upper bound caps the VP8 encoder's per-frame CPU cost.
// maxViewportWidth/Height reverted to 720p (matching the C# backend's cap):
// a load test showed 1080p limited this host to ~7-10 concurrent video
// sessions before CPU saturation caused failures/stalling; 720p (2.25x fewer
// pixels to JPEG-decode + VP8-encode per frame) trades peak single-session
// sharpness for materially higher concurrent-session capacity. See
// encoder_vpx.go's vpxTargetBitrateKbps for the matching bitrate reversion.
const (
	minViewportWidth  = 320
	minViewportHeight = 180
	maxViewportWidth  = 1280
	maxViewportHeight = 720
	defViewportWidth  = 1280
	defViewportHeight = 720

	// browserSetupTimeout is the maximum time allowed for Chrome to launch and
	// navigate to the target URL (DOMContentLoaded).
	browserSetupTimeout = 30 * time.Second
)

// handleSessionOffer returns an http.HandlerFunc for POST /api/session/offer.
// It re-resolves the site policy server-side (the client's prior knowledge of
// the mode is not trusted), then:
//   - 400 if the URL is missing/invalid or the SDP is empty.
//   - 403 if no policy matches the host (deny-by-default).
//   - 409 if the matched mode is HtmlAllowInput or HtmlNoInput (video session
//     requested for an HTML-mode site — use the TLS proxy instead).
//   - 200 + {"sdp":"..."} on success after WebRTC answer is negotiated.
//
// On success the HTTP response is returned immediately; a background goroutine
// waits for the peer connection to reach "connected", then starts the browser
// session, video pipeline, and input forwarder, tearing everything down when
// the connection eventually closes.
func handleSessionOffer(
	eng *policy.Engine,
	webrtcMgr *rtcmgr.Manager,
	browserMgr *browser.Manager,
	videoStore *settings.VideoEncoderStore,
) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req offerRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid JSON body"})
			return
		}

		if req.URL == "" || req.SDP == "" {
			writeJSON(w, http.StatusBadRequest, errorResponse{Error: "url and sdp are required"})
			return
		}

		targetURL, parseErr := url.ParseRequestURI(req.URL)
		if parseErr != nil || !targetURL.IsAbs() {
			writeJSON(w, http.StatusBadRequest, errorResponse{Error: "Invalid URL"})
			return
		}
		host := targetURL.Hostname()

		// Best-effort client IP for the request log.
		clientIP := r.RemoteAddr
		if h, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
			clientIP = h
		}

		// Re-resolve policy server-side — never trust any client-sent hint.
		mode, resolveErr := eng.Resolve(host)
		if resolveErr != nil {
			slog.Error("session offer: policy resolve", "err", resolveErr)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}

		if mode == nil {
			// Unmatched host — deny.
			if logErr := policy.WriteRequestLog(eng.SQLDB(), req.URL, host, "deny", false, clientIP); logErr != nil {
				slog.Error("session offer: write deny log", "err", logErr)
			}
			writeJSON(w, http.StatusForbidden, errorResponse{Error: "This site is not permitted by policy."})
			return
		}

		if *mode == db.ViewModeHtmlAllowInput || *mode == db.ViewModeHtmlNoInput {
			// Policy says HTML mode but client is requesting a video session.
			decision := "mode-mismatch:" + mode.String()
			if logErr := policy.WriteRequestLog(eng.SQLDB(), req.URL, host, decision, false, clientIP); logErr != nil {
				slog.Error("session offer: write mode-mismatch log", "err", logErr)
			}
			writeJSON(w, http.StatusConflict, errorResponse{Error: "This site's policy requires HTML mode, not video."})
			return
		}

		// Only VideoAllowInput and VideoNoInput remain.
		// allowKeyboard=false for VideoNoInput — server-authoritative: keyboard
		// events arriving on the data channel are dropped before CDP dispatch.
		allowKeyboard := *mode == db.ViewModeVideoAllowInput

		// Log the permitted video session.
		if logErr := policy.WriteRequestLog(eng.SQLDB(), req.URL, host, mode.String(), true, clientIP); logErr != nil {
			slog.Error("session offer: write allow log", "err", logErr)
		}

		// Clamp viewport to allowed bounds (nil field → default).
		width := clampViewport(req.Width, defViewportWidth, minViewportWidth, maxViewportWidth)
		height := clampViewport(req.Height, defViewportHeight, minViewportHeight, maxViewportHeight)

		// Resolve which codec this session encodes to from the admin mode + a
		// real hardware probe. Must happen BEFORE CreateSession because the pion
		// track codec is fixed at answer-negotiation time and must match the
		// encoder the pipeline will later use.
		codec, codecErr := resolveVideoCodec(videoStore)
		if codecErr != nil {
			// Only reachable for Gpu mode with no usable hardware encoder — fail
			// loudly (matches the C# "fail loudly" decision) rather than silently
			// answering with a software codec the admin explicitly disabled.
			slog.Error("session offer: resolve video codec", "err", codecErr)
			writeJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "Video encoder unavailable: GPU mode is configured but no hardware H.264 encoder is available."})
			return
		}

		// Negotiate the WebRTC answer synchronously (ICE gathering runs to
		// completion before CreateSession returns, matching the no-trickle model).
		answerSDP, rtcSess, err := webrtcMgr.CreateSession(r.Context(), req.SDP, codec)
		if err != nil {
			slog.Error("session offer: WebRTC create session", "err", err)
			writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "Failed to create WebRTC session."})
			return
		}

		// Launch the background lifecycle manager. The HTTP response is sent
		// immediately after; the manager runs independently of this request.
		go manageRenderingSession(rtcSess, browserMgr, req.URL, width, height, allowKeyboard, codec)

		writeJSON(w, http.StatusOK, answerResponse{SDP: answerSDP})
	}
}

// manageRenderingSession registers an OnConnectionStateChange callback on
// rtcSess.PC that drives the rendering session lifecycle:
//
//   - "connected": launch a browser session (CreateSession), wire the input
//     channel forwarder, start the video pipeline. Close the PC if any step
//     fails so the client sees a clean disconnect.
//   - "closed" / "failed" / "disconnected": close the browser session and the
//     peer connection, releasing all resources.
//
// Race handling: a mutex-guarded ownership flag (torndown + bSessPtr) eliminates
// the TOCTOU race that the prior channel-select design had. Exactly one of
// (connected goroutine, teardown) will call bSess.Close():
//   - If teardown runs first, it sets torndown=true while bSessPtr is nil; the
//     connected goroutine then sees torndown=true and closes bSess itself.
//   - If the connected goroutine finishes first, it stores bSess in bSessPtr;
//     teardown then finds bSessPtr non-nil and closes it.
//   - Both paths hold sessMu for the check-and-act, so no window for double-close
//     or leak exists.
//
// Called as a goroutine from handleSessionOffer; must not reference the HTTP
// request context (already done by the time the peer connection state changes).
func manageRenderingSession(
	rtcSess *rtcmgr.Session,
	browserMgr *browser.Manager,
	targetURL string,
	width, height int,
	allowKeyboard bool,
	codec video.Codec,
) {
	// sessMu guards torndown and bSessPtr, establishing clear ownership of the
	// browser session between the connected goroutine and the teardown path.
	var sessMu sync.Mutex
	var torndown bool
	var bSessPtr *browser.Session

	// teardownOnce ensures the cleanup block runs exactly once even if pion
	// delivers multiple terminal state-change events (e.g. Failed then Closed).
	var teardownOnce sync.Once

	// started is set atomically the first time we see PeerConnectionStateConnected
	// so pion's re-entrant state-change callbacks don't spawn duplicate goroutines.
	var started atomic.Bool

	rtcSess.PC.OnConnectionStateChange(func(state pion.PeerConnectionState) {
		switch state {
		case pion.PeerConnectionStateConnected:
			// Swap returns the old value; if it was already true, we've started.
			if started.Swap(true) {
				return
			}
			slog.Info("session: peer connected, starting render", "url", targetURL)

			// Start Chrome and the video pipeline in a goroutine so the pion
			// state-change callback (which holds an internal mutex) returns fast.
			go func() {
				bSess, err := startRender(rtcSess, browserMgr, targetURL, width, height, allowKeyboard, codec)
				if err != nil {
					slog.Error("session: start render failed", "url", targetURL, "err", err)
					if pcErr := rtcSess.PC.Close(); pcErr != nil {
						slog.Warn("session: close PC after render error", "err", pcErr)
					}
					return
				}
				if bSess == nil {
					return
				}

				// Hand bSess to the teardown path, or close it ourselves if
				// teardown already ran. The mutex ensures no window between the
				// check and the store/close.
				sessMu.Lock()
				if torndown {
					// Teardown already ran and will not visit bSessPtr again;
					// we are responsible for closing.
					sessMu.Unlock()
					bSess.Close()
					slog.Info("session: browser session closed (late start)", "url", targetURL)
				} else {
					// Teardown has not run yet; store the session so teardown
					// will find and close it.
					bSessPtr = bSess
					sessMu.Unlock()
				}
			}()

		case pion.PeerConnectionStateClosed,
			pion.PeerConnectionStateFailed,
			pion.PeerConnectionStateDisconnected:

			slog.Info("session: peer disconnected", "state", state, "url", targetURL)

			teardownOnce.Do(func() {
				// Mark teardown as having run and capture any browser session
				// that the connected goroutine may have already stored.
				sessMu.Lock()
				torndown = true
				toClose := bSessPtr
				bSessPtr = nil
				sessMu.Unlock()

				if toClose != nil {
					toClose.Close()
					slog.Info("session: browser session closed", "url", targetURL)
				}
				// If toClose is nil, the connected goroutine either hasn't
				// finished startRender yet or hasn't started; it will see
				// torndown=true and close bSess itself.

				// Close the peer connection. pion's Close is idempotent.
				if err := rtcSess.PC.Close(); err != nil {
					slog.Warn("session: close PC on disconnect", "err", err)
				}
			})
		}
	})
}

// startRender creates the browser session, wires input forwarding, and starts
// the video pipeline. Returns the browser.Session (caller owns Close), or
// (nil, nil) if the peer connection already died before Chrome finished
// starting, or (nil, err) on a hard failure.
func startRender(
	rtcSess *rtcmgr.Session,
	browserMgr *browser.Manager,
	targetURL string,
	width, height int,
	allowKeyboard bool,
	codec video.Codec,
) (*browser.Session, error) {
	// Use context.Background() for browser setup — the HTTP request context is
	// already done. browserSetupTimeout bounds only the Chrome launch + navigation
	// phase; the session's own long-lived context is managed inside Manager.
	setupCtx, cancel := context.WithTimeout(context.Background(), browserSetupTimeout)
	defer cancel()

	bSess, err := browserMgr.CreateSession(setupCtx, width, height, targetURL)
	if err != nil {
		return nil, err
	}

	// Guard: if the peer connection died while Chrome was starting, close the
	// browser session immediately and return nil (no active session to track).
	if state := rtcSess.PC.ConnectionState(); state == pion.PeerConnectionStateClosed ||
		state == pion.PeerConnectionStateFailed ||
		state == pion.PeerConnectionStateDisconnected {
		bSess.Close()
		slog.Warn("session: peer already disconnected after browser start",
			"url", targetURL, "state", state)
		return nil, nil
	}

	// Wire input forwarding on the pre-negotiated data channel (id=1).
	// Mouse events are always replayed; keyboard events only if allowKeyboard.
	video.WireInputForwarder(rtcSess.InputChannel, bSess.Context, targetURL, allowKeyboard)

	// Start the screencast → encode → RTP pipeline with the resolved codec.
	if err := video.StartPipeline(bSess.Context, rtcSess.VideoTrack, bSess, codec); err != nil {
		bSess.Close()
		return nil, err
	}

	slog.Info("session: render started",
		"url", targetURL,
		"viewport_w", width,
		"viewport_h", height,
		"allow_keyboard", allowKeyboard,
		"codec", codec.String(),
	)
	return bSess, nil
}

// resolveVideoCodec maps the admin-configured video encoder mode to the codec a
// session encodes to, consulting video.H264Available (a real, cached VAAPI
// encoder probe — not just render-node presence):
//   - Cpu  → always VP8 (software).
//   - Gpu  → H.264 if hardware is usable; otherwise an error (fail loudly).
//   - Auto → H.264 if hardware is usable, else VP8.
//
// On any DB read error the mode defaults to Auto (matching VideoEncoderStore),
// so a settings glitch degrades to the safe software path rather than failing.
func resolveVideoCodec(store *settings.VideoEncoderStore) (video.Codec, error) {
	mode, err := store.GetMode()
	if err != nil {
		slog.Warn("session offer: read video encoder mode, defaulting to Auto", "err", err)
		mode = db.VideoEncoderModeAuto
	}

	switch mode {
	case db.VideoEncoderModeCpu:
		return video.CodecVP8, nil
	case db.VideoEncoderModeGpu:
		if !video.H264Available() {
			return 0, fmt.Errorf("video encoder mode is Gpu but no usable VAAPI H.264 encoder is available")
		}
		return video.CodecH264, nil
	default: // Auto
		if video.H264Available() {
			return video.CodecH264, nil
		}
		return video.CodecVP8, nil
	}
}

// clampViewport returns the requested dimension clamped to [minimum, maximum].
// If req is nil, def is returned.
func clampViewport(req *int, def, minimum, maximum int) int {
	if req == nil {
		return def
	}
	return int(math.Max(float64(minimum), math.Min(float64(maximum), float64(*req))))
}
