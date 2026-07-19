//go:build !vpx

// This file provides the default (stub) encoders used when building without the
// "vpx" build tag. They compile on any platform without cgo, libvpx-dev, or the
// VAAPI/libavcodec headers, making `go build ./...` work on development machines
// that don't have those installed. The stubs return an error at runtime so
// session creation fails fast with a clear message rather than silently
// producing no video.
//
// To enable real encoding (required for production / Docker):
//
//	go build -tags vpx ./...
//
// The Go Docker image installs libvpx-dev + the VAAPI/libavcodec runtime and
// passes this tag.
package video

import "errors"

// errVpxNotBuilt is returned by the stub VP8 encoder.
var errVpxNotBuilt = errors.New(
	"VP8 encoder not available: rebuild with -tags vpx (requires libvpx-dev)",
)

// errVaapiNotBuilt is returned by the stub H.264 encoder.
var errVaapiNotBuilt = errors.New(
	"H.264 encoder not available: rebuild with -tags vpx (requires libavcodec + VAAPI)",
)

// stubEncoder satisfies the VideoEncoder interface but always returns an error
// from Encode. It exists solely to satisfy the interface type so non-vpx builds
// compile without the real cgo encoders.
type stubEncoder struct{}

// NewVP8Encoder returns an error in the stub build so session creation fails
// immediately with a human-readable message instead of streaming no video.
func NewVP8Encoder(_ int, _ int) (VideoEncoder, error) {
	return nil, errVpxNotBuilt
}

// NewH264Encoder returns an error in the stub build (no VAAPI/libavcodec linked).
func NewH264Encoder(_ int, _ int) (VideoEncoder, error) {
	return nil, errVaapiNotBuilt
}

// H264Available reports false in the stub build: without the cgo VAAPI backend
// there is no hardware H.264 encoder, so codec resolution must never pick H.264.
func H264Available() bool { return false }

// Encode is unreachable in the stub because the New* constructors always return
// an error, so no stubEncoder is ever created.
func (s *stubEncoder) Encode(_ []byte, _, _ int, _ bool) ([]byte, error) {
	return nil, errVpxNotBuilt
}

// Close is a no-op for the stub encoder.
func (s *stubEncoder) Close() {}
