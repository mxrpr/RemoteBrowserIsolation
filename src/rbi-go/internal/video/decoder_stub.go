//go:build !vpx

// This file provides the default (pure-Go) JPEG decoder used when building
// without the "vpx" build tag — see encoder_stub.go for why that tag gate
// exists. The real build (-tags vpx) uses decoder_turbojpeg.go instead, which
// decodes straight to I420 via libjpeg-turbo and is meaningfully faster.
package video

import (
	"bytes"
	"fmt"
	"image"
	"image/jpeg"
)

// decodeJPEGToI420 decodes a JPEG-compressed frame (as produced by
// Page.startScreencast with format="jpeg") into a packed I420 buffer.
//
// dst, if it has enough capacity for the current frame's I420 layout, is
// reused in place instead of allocating a new buffer — the transcode loop is
// the sole owner of both the decode step and the encoder that consumes the
// result (fully copied into libvpx before the next frame is decoded), so
// reusing dst across calls has no aliasing hazard. Pass nil to always
// allocate (e.g. in tests). The returned slice aliases dst when reused, so
// callers must treat the previous call's returned slice as invalidated once
// decodeJPEGToI420 is called again with the same dst.
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
func decodeJPEGToI420(jpegBytes []byte, dst []byte) (i420 []byte, width, height int, err error) {
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

	i420 = convertToI420(img, rect, width, height, dst)
	return i420, width, height, nil
}

// convertToI420 converts an image.Image to a packed I420 byte slice, reusing
// dst when it already has sufficient capacity (see decodeJPEGToI420).
// Fast path for *image.YCbCr with SubsampleRatio420 (the common Chromium
// screencast output); generic slow path for other pixel formats.
func convertToI420(img image.Image, rect image.Rectangle, width, height int, dst []byte) []byte {
	chromaW := (width + 1) / 2
	chromaH := (height + 1) / 2
	ySize := width * height
	uvSize := chromaW * chromaH
	needed := ySize + 2*uvSize

	var buf []byte
	if cap(dst) >= needed {
		buf = dst[:needed]
	} else {
		buf = make([]byte, needed)
	}

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
