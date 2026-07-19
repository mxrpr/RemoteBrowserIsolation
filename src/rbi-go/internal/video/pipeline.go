package video

import (
	"context"
	"encoding/base64"
	"fmt"
	"log/slog"
	"strings"
	"sync/atomic"
	"time"

	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"
	pion "github.com/pion/webrtc/v3"
	"github.com/pion/webrtc/v3/pkg/media"

	"rbi-go/internal/browser"
)

const (
	// screencastQuality is the JPEG quality for Page.startScreencast.
	// 80 matches the C# VideoTrackStreamer constant and gives a good balance
	// of fidelity vs. decode speed for I420 conversion.
	screencastQuality = 80

	// screencastMaxDim caps the screencast output dimensions so Chrome does not
	// scale down the session viewport. 4096 is large enough to pass through any
	// viewport we support (≤1280×720).
	screencastMaxDim = 4096

	// keyframeInterval is how often a forced VP8 keyframe is emitted. Clients
	// that join mid-stream or experience packet loss can re-lock onto the stream
	// after at most this interval. Matches the C# KeyFrameInterval.
	keyframeInterval = 5 * time.Second

	// maxFrameRate caps how many frames per second the transcode loop actually
	// decodes+encodes. screencastEveryNthFrame throttles Chromium's capture, but
	// a busy page still yields ~30fps of repaints; JPEG-decoding and VP8-encoding
	// every one of them is the dominant per-session CPU cost. Human browsing does
	// not need more than ~20fps, so frames arriving faster than minFrameInterval
	// are dropped before the expensive decode/encode work — the cheapest CPU lever
	// after screencastEveryNthFrame for raising concurrent-session capacity.
	maxFrameRate = 20

	// minFrameInterval is the minimum wall-clock gap between two encoded frames,
	// derived from maxFrameRate. A frame arriving sooner is dropped (unless a
	// keyframe is due).
	minFrameInterval = time.Second / maxFrameRate

	// rtpVideoClockRate is the RTP timestamp clock rate for video (90 kHz, RFC 3550).
	// pion's TrackLocalStaticSample.WriteSample converts sample.Duration to RTP
	// timestamp units using this rate.
	rtpVideoClockRate = 90_000
)

// StartPipeline wires the CDP screencast for browserSess to videoTrack and
// starts the transcode goroutine. It returns once the screencast CDP command
// has been sent; the actual frame delivery and encoding run in background
// goroutines. The pipeline exits when sessCtx is cancelled (i.e. when the
// browser session is closed).
//
// Pipeline:
//  1. Register a chromedp event listener for page.EventScreencastFrame on
//     browserSess.Context; ack each frame immediately (in a goroutine) so
//     Chromium keeps producing new frames.
//  2. Push JPEG frame bytes into a capacity-1 "latest-wins" mailbox channel
//     (drop old frame if the encoder hasn't consumed the previous one yet).
//  3. A single transcode goroutine reads from the mailbox, decodes JPEG to
//     I420, VP8-encodes, and calls videoTrack.WriteSample.
//  4. A forced keyframe is emitted every keyframeInterval.
func StartPipeline(sessCtx context.Context, videoTrack *pion.TrackLocalStaticSample, browserSess *browser.Session, codec Codec) error {
	enc, err := NewVideoEncoder(codec, browserSess.ViewportW, browserSess.ViewportH)
	if err != nil {
		return err
	}

	// mailbox is the latest-wins frame buffer. Capacity 1 means at most one
	// pending frame is queued; the screencast listener drops old frames if the
	// encoder hasn't kept up. This matches the C# BoundedChannelFullMode.DropOldest.
	mailbox := make(chan []byte, 1)

	// Register screencast event listener on the browser session's chromedp context.
	// Note: the function passed to ListenTarget is called synchronously from
	// chromedp's receive goroutine and MUST NOT block. CDP acks are sent from
	// separate goroutines to avoid deadlocking the receive loop.
	chromedp.ListenTarget(sessCtx, func(ev any) {
		frame, ok := ev.(*page.EventScreencastFrame)
		if !ok {
			return
		}

		// Decode base64 immediately (cheap) so the listener goroutine doesn't
		// hold a reference to the raw CDP JSON.
		jpegBytes, decErr := base64.StdEncoding.DecodeString(frame.Data)
		if decErr != nil {
			slog.Warn("video: base64 decode screencast frame", "err", decErr)
			return
		}

		// Latest-wins mailbox: drain old frame then write new.
		// Single writer (chromedp's goroutine) so there is no race between
		// the drain and write.
		select {
		case <-mailbox:
		default:
		}
		select {
		case mailbox <- jpegBytes:
		default:
		}

		// Ack asynchronously so Chromium keeps streaming without waiting for the
		// encoder. The goroutine is intentionally fire-and-forget; errors are
		// logged but don't abort the pipeline.
		sessionID := frame.SessionID
		go func() {
			if ackErr := chromedp.Run(sessCtx, page.ScreencastFrameAck(sessionID)); ackErr != nil {
				// sessCtx cancellation during teardown produces an error here —
				// expected and not worth logging as a warning.
				if sessCtx.Err() == nil {
					slog.Warn("video: screencast frame ack", "err", ackErr)
				}
			}
		}()
	})

	// Issue Page.startScreencast via the browser session's chromedp context.
	// maxWidth/maxHeight are set to screencastMaxDim so the viewport dimensions
	// from CreateSession are used unchanged (Chrome only scales down, never up).
	//
	// We deliberately do NOT set WithEveryNthFrame: CDP's every-Nth-frame counter
	// does not special-case the first paint, so a page that paints exactly once
	// and never repaints (static text, no cursor/animation) can land entirely on
	// dropped counts and emit ZERO screencast frames — a permanently blank/frozen
	// client stream. Source-side throttling is therefore unsafe. Frame-rate
	// limiting is instead done downstream in transcodeLoop's minFrameInterval
	// gate, which guarantees the first frame always passes (its lastEncodeAt
	// starts at the zero time) and only drops surplus frames on busy pages.
	//
	// The transcode goroutine is started only after this succeeds so that enc
	// has exactly one owner: if StartScreencast fails, enc.Close() is called
	// here and no goroutine is running; if it succeeds, transcodeLoop owns
	// enc via its deferred Close and no explicit call is needed here.
	//
	// Retry on "-32000 Not attached to an active page": under concurrent session
	// startup the page occasionally is mid-navigation-commit (swapping to the
	// freshly navigated document) at the instant StartScreencast is dispatched,
	// even though DOMContentLoaded already fired — Chrome briefly reports no
	// "active page" for the target. This is a transient window (no target
	// detach/destroy/crash — verified via CDP lifecycle listeners), so a short
	// bounded retry lands on the settled page. Without it, ~1 in 8-10 concurrent
	// sessions failed to start under load.
	if err := startScreencastWithRetry(sessCtx); err != nil {
		enc.Close()
		return fmt.Errorf("video: start screencast for %q: %w", browserSess.TargetURL, err)
	}

	// firstFrame is set by transcodeLoop once a real frame has been encoded and
	// sent. forceInitialFrame watches it so its nudges stop as soon as frames
	// flow — dynamic pages (which repaint on their own) are never nudged.
	var firstFrame atomic.Bool

	// Start the transcode goroutine now that StartScreencast has succeeded.
	// It will drain the mailbox until sessCtx is cancelled.
	go transcodeLoop(sessCtx, mailbox, videoTrack, enc, browserSess.TargetURL, browserSess.ViewportW, browserSess.ViewportH, &firstFrame)

	// Force an initial compositor frame for otherwise-static pages. CDP
	// Page.startScreencast only emits on a repaint/commit — a page that finished
	// painting BEFORE screencast was enabled and is then idle (static text pages,
	// example.com) never repaints, so zero frames arrive and the client stays
	// permanently blank. forceInitialFrame nudges the compositor until a frame
	// actually flows.
	go forceInitialFrame(sessCtx, browserSess.ViewportW, browserSess.ViewportH, browserSess.TargetURL, &firstFrame)

	return nil
}

// forceInitialFrame nudges Chromium into producing at least one screencast frame
// for a static, already-painted page (see StartPipeline call site). Re-applying
// the SAME viewport is a Chrome no-op (no metrics change → no commit), so this
// briefly resizes the viewport by +2px and back: the resize forces a relayout +
// compositor commit. The transient +2px frame is dropped by transcodeLoop's
// frame-size guard; the restored-size frame that follows passes through. It
// stops as soon as firstFrame is set, so pages that already stream frames get
// at most one harmless nudge (usually none, since their frames arrive first).
func forceInitialFrame(sessCtx context.Context, w, h int, targetURL string, firstFrame *atomic.Bool) {
	for attempt := 0; attempt < 5; attempt++ {
		select {
		case <-sessCtx.Done():
			return
		case <-time.After(time.Duration(attempt+1) * 200 * time.Millisecond):
		}
		if firstFrame.Load() {
			return
		}
		// Resize to h+2 then back to h to force two compositor commits.
		if err := chromedp.Run(sessCtx,
			chromedp.EmulateViewport(int64(w), int64(h+2)),
			chromedp.EmulateViewport(int64(w), int64(h)),
		); err != nil {
			if sessCtx.Err() == nil {
				slog.Warn("video: force initial frame via viewport resize", "url", targetURL, "err", err)
			}
			return
		}
	}
}

// screencastRetries is how many extra attempts startScreencastWithRetry makes
// after the first, and screencastRetryDelay is the wait between them. Four extra
// tries at 150ms covers the observed navigation-commit window (well under a
// second) without materially delaying a healthy session's first frame.
const (
	screencastRetries    = 4
	screencastRetryDelay = 150 * time.Millisecond
)

// startScreencastWithRetry issues Page.startScreencast, retrying on the transient
// "-32000 Not attached to an active page" error that occurs when the page is
// mid-navigation-commit at dispatch time (see StartPipeline's call site for the
// full rationale). Any other error, or a cancelled context, returns immediately.
// The final attempt's error is returned if all attempts fail.
func startScreencastWithRetry(sessCtx context.Context) error {
	action := page.StartScreencast().
		WithFormat(page.ScreencastFormatJpeg).
		WithQuality(screencastQuality).
		WithMaxWidth(screencastMaxDim).
		WithMaxHeight(screencastMaxDim)

	var err error
	for attempt := 0; attempt <= screencastRetries; attempt++ {
		if attempt > 0 {
			select {
			case <-sessCtx.Done():
				return sessCtx.Err()
			case <-time.After(screencastRetryDelay):
			}
		}
		err = chromedp.Run(sessCtx, action)
		if err == nil {
			if attempt > 0 {
				slog.Info("video: screencast started after retry", "attempts", attempt+1)
			}
			return nil
		}
		// Only the "not attached to an active page" race is worth retrying; any
		// other failure (real navigation failure, closed context) is terminal.
		if !strings.Contains(err.Error(), "Not attached to an active page") {
			return err
		}
	}
	return err
}

// transcodeLoop is the single consumer goroutine for the screencast mailbox.
// It reads JPEG frames, decodes them to I420, VP8-encodes them, and writes the
// resulting RTP sample to videoTrack. It exits only when sessCtx is cancelled;
// the mailbox channel is never closed by any code path.
//
// Frames whose decoded dimensions differ from viewportW×viewportH are skipped
// with an error log to avoid passing mismatched dimensions to libvpx
// (which would return VPX_CODEC_INVALID_PARAM and silently freeze the stream).
//
// Keyframes: a forced VP8 keyframe is emitted every keyframeInterval so new
// viewers and loss-recovery paths can lock onto the stream.
func transcodeLoop(
	sessCtx context.Context,
	mailbox <-chan []byte,
	videoTrack *pion.TrackLocalStaticSample,
	enc VideoEncoder,
	targetURL string,
	viewportW, viewportH int,
	firstFrame *atomic.Bool,
) {
	defer enc.Close()

	var (
		lastFrameAt  = time.Now()
		lastKeyFrame = time.Now()
		lastEncodeAt time.Time // zero value: first frame always passes the cap
		frameCount   int64

		// i420Buf is reused across frames by decodeJPEGToI420 instead of
		// allocating a fresh ~viewportW*viewportH*1.5-byte buffer every frame
		// (this loop is decodeJPEGToI420's sole caller, so reuse is safe — see
		// its doc comment).
		i420Buf []byte
	)

	for {
		var jpegBytes []byte

		// Block until a frame arrives or the session ends. The mailbox channel
		// is never closed; the only exit path is sessCtx.Done().
		select {
		case b := <-mailbox:
			jpegBytes = b
		case <-sessCtx.Done():
			slog.Info("video: transcode loop: context done", "url", targetURL, "frames", frameCount)
			return
		}

		// Framerate cap: a keyframe is due on schedule regardless; otherwise drop
		// any frame arriving sooner than minFrameInterval since the last encode.
		// This gate runs BEFORE the costly JPEG-decode + VP8-encode so dropped
		// frames cost only a time comparison.
		now := time.Now()
		forceKey := now.Sub(lastKeyFrame) >= keyframeInterval
		if !forceKey && now.Sub(lastEncodeAt) < minFrameInterval {
			continue
		}
		lastEncodeAt = now
		if forceKey {
			lastKeyFrame = now
		}

		t0 := time.Now()

		i420, w, h, err := decodeJPEGToI420(jpegBytes, i420Buf)
		if err == nil {
			i420Buf = i420
		}
		if err != nil {
			slog.Warn("video: JPEG decode failed", "url", targetURL, "err", err)
			continue
		}

		// Guard against screencast frames arriving at a size different from the
		// encoder's configured dimensions (DPR scaling, screencastMaxDim clamp,
		// etc.). Passing mismatched dims to libvpx yields VPX_CODEC_INVALID_PARAM
		// on every subsequent frame, silently freezing the client stream.
		if w != viewportW || h != viewportH {
			slog.Error("video: frame size mismatch — skipping frame",
				"url", targetURL,
				"frame_w", w, "frame_h", h,
				"viewport_w", viewportW, "viewport_h", viewportH,
			)
			continue
		}

		encoded, err := enc.Encode(i420, w, h, forceKey)
		if err != nil {
			slog.Warn("video: VP8 encode failed", "url", targetURL, "err", err)
			continue
		}
		if len(encoded) == 0 {
			continue
		}

		// Duration is the wall-clock time since the previous frame. pion converts
		// this to RTP timestamp units (90 kHz) internally via WriteSample.
		frameDur := time.Since(lastFrameAt)
		if frameDur < time.Millisecond {
			frameDur = time.Millisecond // guard against zero-duration on same-tick frames
		}
		lastFrameAt = time.Now()

		if err := videoTrack.WriteSample(media.Sample{
			Data:     encoded,
			Duration: frameDur,
		}); err != nil {
			// WriteSample failing usually means the peer connection is closed.
			// Log and exit; teardown of the session is handled by the caller
			// via OnConnectionStateChange.
			slog.Warn("video: WriteSample failed", "url", targetURL, "err", err)
			return
		}

		frameCount++
		// Signal forceInitialFrame that real frames are now flowing so it stops
		// nudging the compositor.
		firstFrame.Store(true)
		slog.Debug("video: frame",
			"count", frameCount,
			"url", targetURL,
			"jpeg_bytes", len(jpegBytes),
			"vp8_bytes", len(encoded),
			"ms", time.Since(t0).Milliseconds(),
			"keyframe", forceKey,
		)
	}
}
