//go:build !vpx

// This file provides the default (stub) VP8 encoder used when building without
// the "vpx" build tag. It compiles on any platform without cgo or libvpx-dev,
// making `go build ./...` work on development machines that don't have FFmpeg /
// libvpx installed. The stub returns an error at runtime so session creation
// fails fast with a clear message rather than silently producing no video.
//
// To enable real VP8 encoding (required for production / Docker):
//
//	go build -tags vpx ./...
//
// The Go Docker image (Part 12) installs libvpx-dev and passes this tag.
package video

import "errors"

// errVpxNotBuilt is returned by the stub encoder. Surfacing the build tag in
// the error message helps operators quickly identify the root cause.
var errVpxNotBuilt = errors.New(
	"VP8 encoder not available: rebuild with -tags vpx (requires libvpx-dev)",
)

// stubVP8Encoder satisfies the VP8Encoder interface but always returns an error
// from Encode. It exists solely to satisfy the interface type so non-vpx builds
// compile without the real cgo encoder.
type stubVP8Encoder struct{}

// NewVP8Encoder returns an error in the stub build. This causes session
// creation to fail immediately with a human-readable message, rather than
// crashing or silently streaming no video.
func NewVP8Encoder(_ int, _ int) (VP8Encoder, error) {
	return nil, errVpxNotBuilt
}

// Encode is unreachable in the stub because NewVP8Encoder always returns an
// error, so no stubVP8Encoder is ever created.
func (s *stubVP8Encoder) Encode(_ []byte, _, _ int, _ bool) ([]byte, error) {
	return nil, errVpxNotBuilt
}

// Close is a no-op for the stub encoder.
func (s *stubVP8Encoder) Close() {}
