// Package video implements the server-side video pipeline for rbi-go:
// CDP screencast JPEG frames are decoded to I420, VP8-encoded, and written to
// a pion send-only track. The package also handles input event forwarding from
// the data channel to the headless browser via CDP Input.dispatch* commands.
//
// Both the VP8 encoder and the JPEG-to-I420 decoder are provided by a
// build-tag-selected backend:
//   - Default build (no tags): stub VP8 encoder that returns an error at
//     runtime (encoder_stub.go), and a pure-Go JPEG decoder (decoder_stub.go)
//     — together these let `go build ./...` work without libvpx-dev or
//     libjpeg-turbo-dev installed.
//   - `-tags vpx`: real cgo backends wrapping libvpx (encoder_vpx.go) and
//     libjpeg-turbo (decoder_turbojpeg.go) — requires the matching -dev
//     packages at build time and their .so's at runtime (both installed
//     together in the Go Docker image).
package video

// VP8Encoder encodes raw I420 frames to VP8 bitstream packets. Implementations
// are not concurrency-safe: each encoder is owned by exactly one transcode
// loop goroutine. The encoder is stateful (delta frames reference prior frames)
// and must not be shared across sessions.
type VP8Encoder interface {
	// Encode converts one I420 frame (packed Y+U+V planes, no padding) to a
	// VP8 bitstream packet. forceKeyFrame=true emits a full intra frame, which
	// resets the reference chain so a new viewer can lock on without prior frames.
	// Returns the encoded packet bytes; returns (nil, nil) if the codec produced
	// no output for this frame (normal for B-frame pipelines, rare for VP8).
	Encode(yuvI420 []byte, width, height int, forceKeyFrame bool) ([]byte, error)

	// Close frees the encoder's internal resources. Must be called exactly once
	// when the session ends. Subsequent calls to Encode after Close return an error.
	Close()
}
