// Package video implements the server-side video pipeline for rbi-go:
// CDP screencast JPEG frames are decoded to I420, encoded (VP8 in software, or
// H.264 on the GPU via VAAPI), and written to a pion send-only track. The
// package also handles input event forwarding from the data channel to the
// headless browser via CDP Input.dispatch* commands.
//
// Encoder/decoder backends are build-tag-selected:
//   - Default build (no tags): stub encoders that return an error at runtime
//     (encoder_stub.go), and a pure-Go JPEG decoder (decoder_stub.go) — together
//     these let `go build ./...` work without libvpx-dev, libjpeg-turbo, or the
//     VAAPI/libavcodec headers installed.
//   - `-tags vpx` (with cgo): real cgo backends — libvpx VP8 (encoder_vpx.go),
//     libjpeg-turbo decode (decoder_turbojpeg.go), and VAAPI H.264 via libavcodec
//     (encoder_vaapi.go). Requires the matching -dev packages at build time and
//     their .so's (plus a VAAPI render node) at runtime.
package video

import "fmt"

// Codec identifies which video codec the pipeline encodes to for a session. The
// choice is resolved once per session from the admin video-encoder mode + a
// hardware probe, and must match the codec advertised on the pion video track.
type Codec int

const (
	// CodecVP8 is software VP8 via libvpx (encoder_vpx.go). The universal
	// fallback: always available in the production (vpx-tagged) build regardless
	// of GPU presence.
	CodecVP8 Codec = iota
	// CodecH264 is hardware H.264 (Constrained Baseline) via VAAPI/libavcodec
	// (encoder_vaapi.go). Selected only when a usable VAAPI encoder is present so
	// the per-frame encode runs on the GPU's fixed-function media engine.
	CodecH264
)

// String returns a short human-readable codec name for logs.
func (c Codec) String() string {
	switch c {
	case CodecVP8:
		return "VP8"
	case CodecH264:
		return "H264"
	default:
		return fmt.Sprintf("Codec(%d)", int(c))
	}
}

// VideoEncoder encodes raw I420 frames to a codec bitstream. Implementations are
// not concurrency-safe: each encoder is owned by exactly one transcode loop
// goroutine. The encoder is stateful (delta frames reference prior frames) and
// must not be shared across sessions.
type VideoEncoder interface {
	// Encode converts one I420 frame (packed Y+U+V planes, no padding) to a
	// codec bitstream packet (VP8, or H.264 Annex-B). forceKeyFrame=true emits a
	// full intra frame, which resets the reference chain so a new viewer can lock
	// on without prior frames. Returns the encoded packet bytes; returns
	// (nil, nil) if the codec produced no output for this frame.
	Encode(yuvI420 []byte, width, height int, forceKeyFrame bool) ([]byte, error)

	// Close frees the encoder's internal resources. Must be called exactly once
	// when the session ends. Subsequent calls to Encode after Close return an error.
	Close()
}

// NewVideoEncoder constructs the encoder for the given codec, sized for
// width×height frames. It dispatches to the build-tag-selected backend
// (NewVP8Encoder / NewH264Encoder); in the stub build both return an error so
// session creation fails fast with a clear, actionable message.
func NewVideoEncoder(codec Codec, width, height int) (VideoEncoder, error) {
	switch codec {
	case CodecVP8:
		return NewVP8Encoder(width, height)
	case CodecH264:
		return NewH264Encoder(width, height)
	default:
		return nil, fmt.Errorf("video: unknown codec %v", codec)
	}
}
