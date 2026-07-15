// Package video implements the server-side video pipeline for rbi-go:
// CDP screencast JPEG frames are decoded to I420, VP8-encoded, and written to
// a pion send-only track. The package also handles input event forwarding from
// the data channel to the headless browser via CDP Input.dispatch* commands.
//
// VP8 encoding is provided by a build-tag-selected backend:
//   - Default build (no tags): stub backend that returns an error at runtime,
//     allowing `go build ./...` without libvpx-dev installed.
//   - `-tags vpx`: real cgo backend that wraps libvpx (requires libvpx-dev at
//     build time and libvpx at runtime — available in the Go Docker image).
package video

import (
	"bytes"
	"fmt"
	"image"
	"image/jpeg"
)

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

// decodeJPEGToI420 decodes a JPEG-compressed frame (as produced by
// Page.startScreencast with format="jpeg") into a packed I420 buffer.
//
// The returned buffer layout matches what libvpx expects:
//
//	[Y plane: width×height bytes]
//	[U plane: chromaW×chromaH bytes]
//	[V plane: chromaW×chromaH bytes]
//
// where chromaW = (width+1)/2, chromaH = (height+1)/2.
//
// Chromium's JPEG screencast output is YCbCr 4:2:0 internally; image/jpeg
// decodes it as *image.YCbCr with SubsampleRatio420 so the fast copy-path is
// hit on virtually every frame. A slower pixel-by-pixel conversion handles the
// edge case of other subsampling ratios.
func decodeJPEGToI420(jpegBytes []byte) (i420 []byte, width, height int, err error) {
	img, decErr := jpeg.Decode(bytes.NewReader(jpegBytes))
	if decErr != nil {
		return nil, 0, 0, fmt.Errorf("video: JPEG decode: %w", decErr)
	}

	rect := img.Bounds()
	width = rect.Dx()
	height = rect.Dy()
	if width <= 0 || height <= 0 {
		return nil, 0, 0, fmt.Errorf("video: decoded image has zero dimension: %dx%d", width, height)
	}

	i420 = convertToI420(img, rect, width, height)
	return i420, width, height, nil
}

// convertToI420 converts an image.Image to a packed I420 byte slice.
// Fast path for *image.YCbCr with SubsampleRatio420 (the common Chromium
// screencast output); generic slow path for other pixel formats.
func convertToI420(img image.Image, rect image.Rectangle, width, height int) []byte {
	chromaW := (width + 1) / 2
	chromaH := (height + 1) / 2
	ySize := width * height
	uvSize := chromaW * chromaH
	buf := make([]byte, ySize+2*uvSize)

	if ycbcr, ok := img.(*image.YCbCr); ok && ycbcr.SubsampleRatio == image.YCbCrSubsampleRatio420 {
		// Fast path: image is already planar YCbCr 4:2:0. Copy each luma row
		// directly from the Y plane (which may have stride padding).
		yDst := 0
		for row := 0; row < height; row++ {
			srcStart := ycbcr.YOffset(0, row)
			copy(buf[yDst:yDst+width], ycbcr.Y[srcStart:srcStart+width])
			yDst += width
		}

		// Copy chroma planes. COffset(0, row*2) gives the byte index in the
		// Cb/Cr slices for the chroma row at source-pixel row row*2, which
		// equals CStride*row for 4:2:0. CStride may include padding.
		uDst := ySize
		vDst := ySize + uvSize
		for cRow := 0; cRow < chromaH; cRow++ {
			srcStart := ycbcr.COffset(0, cRow*2)
			copy(buf[uDst:uDst+chromaW], ycbcr.Cb[srcStart:srcStart+chromaW])
			copy(buf[vDst:vDst+chromaW], ycbcr.Cr[srcStart:srcStart+chromaW])
			uDst += chromaW
			vDst += chromaW
		}
		return buf
	}

	// Slow generic path: sample each pixel via At() and convert to YUV.
	// This handles non-420 subsampling and non-YCbCr pixel formats.
	yDst := 0
	for row := 0; row < height; row++ {
		for col := 0; col < width; col++ {
			r32, g32, b32, _ := img.At(rect.Min.X+col, rect.Min.Y+row).RGBA()
			y, u, v := rgbToYUV(uint8(r32>>8), uint8(g32>>8), uint8(b32>>8))
			buf[yDst] = y
			yDst++
			if row%2 == 0 && col%2 == 0 {
				uvIdx := (row/2)*chromaW + col/2
				buf[ySize+uvIdx] = u
				buf[ySize+uvSize+uvIdx] = v
			}
		}
	}
	return buf
}

// rgbToYUV converts an 8-bit R, G, B triplet to Y, Cb, Cr values using the
// BT.601 limited-range coefficients (standard for JPEG/screencast content).
func rgbToYUV(r, g, b uint8) (y, u, v uint8) {
	rf, gf, bf := float64(r), float64(g), float64(b)
	yf := 0.299*rf + 0.587*gf + 0.114*bf
	uf := -0.168736*rf - 0.331264*gf + 0.5*bf + 128
	vf := 0.5*rf - 0.418688*gf - 0.081312*bf + 128
	return clamp8(yf), clamp8(uf), clamp8(vf)
}

// clamp8 clamps a float64 to the [0, 255] uint8 range.
func clamp8(v float64) uint8 {
	if v < 0 {
		return 0
	}
	if v > 255 {
		return 255
	}
	return uint8(v)
}
