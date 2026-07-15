package video

import (
	"context"
	"encoding/base64"
	"log/slog"
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
func StartPipeline(sessCtx context.Context, videoTrack *pion.TrackLocalStaticSample, browserSess *browser.Session) error {
	enc, err := NewVP8Encoder(browserSess.ViewportW, browserSess.ViewportH)
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
	// The transcode goroutine is started only after this succeeds so that enc
	// has exactly one owner: if StartScreencast fails, enc.Close() is called
	// here and no goroutine is running; if it succeeds, transcodeLoop owns
	// enc via its deferred Close and no explicit call is needed here.
	if err := chromedp.Run(sessCtx, page.StartScreencast().
		WithFormat(page.ScreencastFormatJpeg).
		WithQuality(screencastQuality).
		WithMaxWidth(screencastMaxDim).
		WithMaxHeight(screencastMaxDim)); err != nil {
		enc.Close()
		return err
	}

	// Start the transcode goroutine now that StartScreencast has succeeded.
	// It will drain the mailbox until sessCtx is cancelled.
	go transcodeLoop(sessCtx, mailbox, videoTrack, enc, browserSess.TargetURL, browserSess.ViewportW, browserSess.ViewportH)

	return nil
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
	enc VP8Encoder,
	targetURL string,
	viewportW, viewportH int,
) {
	defer enc.Close()

	var (
		lastFrameAt  = time.Now()
		lastKeyFrame = time.Now()
		frameCount   int64
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

		t0 := time.Now()

		i420, w, h, err := decodeJPEGToI420(jpegBytes)
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

		forceKey := time.Since(lastKeyFrame) >= keyframeInterval
		if forceKey {
			lastKeyFrame = time.Now()
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
