//go:build vpx && cgo

// Real JPEG decoder using libjpeg-turbo's TurboJPEG API via cgo. Compiled
// only when building with -tags vpx (same tag as encoder_vpx.go — both cgo
// backends require their -dev headers at build time and .so's at runtime,
// installed together in the Go Dockerfile).
//
// tjDecompressToYUV2 decodes straight from the JPEG's native YCbCr planes
// into a packed I420 buffer, skipping both the pure-Go JPEG decode
// (decoder_stub.go's image/jpeg.Decode) and the RGB round trip that a
// generic decoder would otherwise need — this is the actual CPU win over the
// stub build, not just "a faster JPEG library".
package video

/*
#cgo CFLAGS: -I/usr/include
#cgo LDFLAGS: -lturbojpeg

#include <stdlib.h>
#include <turbojpeg.h>

// tjPeekI420Size parses a JPEG header (no pixel decode) to report the image
// dimensions and the exact byte size a packed I420 (4:2:0, no row padding)
// output buffer must have. Returns -1 (errBuf filled) if the header can't be
// parsed or the JPEG isn't 4:2:0 subsampled — Chromium's screencast JPEG
// output is always 4:2:0, so anything else is treated as an error the caller
// skips the frame for, matching the dimension-mismatch guard already in
// transcodeLoop.
int tjPeekI420Size(const unsigned char *jpegBuf, unsigned long jpegSize,
                    int *outW, int *outH, unsigned long *outSize,
                    char *errBuf, int errBufLen) {
    tjhandle tjh = tjInitDecompress();
    if (!tjh) {
        snprintf(errBuf, errBufLen, "tjInitDecompress failed");
        return -1;
    }

    int width, height, subsamp, colorspace;
    if (tjDecompressHeader3(tjh, jpegBuf, jpegSize, &width, &height, &subsamp, &colorspace) != 0) {
        snprintf(errBuf, errBufLen, "tjDecompressHeader3: %s", tjGetErrorStr2(tjh));
        tjDestroy(tjh);
        return -1;
    }
    if (subsamp != TJSAMP_420) {
        snprintf(errBuf, errBufLen, "unsupported JPEG subsampling: %d (want TJSAMP_420=%d)", subsamp, TJSAMP_420);
        tjDestroy(tjh);
        return -1;
    }

    *outW = width;
    *outH = height;
    *outSize = tjBufSizeYUV2(width, 1, height, subsamp); // pad=1: no row padding, matches I420 layout
    tjDestroy(tjh);
    return 0;
}

// tjDecodeI420 decodes one 4:2:0 JPEG image directly into dst as a packed
// I420 buffer (Y plane, then U, then V, no row padding). dstCap must be at
// least the size tjPeekI420Size reported for this same jpegBuf.
int tjDecodeI420(const unsigned char *jpegBuf, unsigned long jpegSize,
                  unsigned char *dst, unsigned long dstCap,
                  char *errBuf, int errBufLen) {
    tjhandle tjh = tjInitDecompress();
    if (!tjh) {
        snprintf(errBuf, errBufLen, "tjInitDecompress failed");
        return -1;
    }

    int width, height, subsamp, colorspace;
    if (tjDecompressHeader3(tjh, jpegBuf, jpegSize, &width, &height, &subsamp, &colorspace) != 0) {
        snprintf(errBuf, errBufLen, "tjDecompressHeader3: %s", tjGetErrorStr2(tjh));
        tjDestroy(tjh);
        return -1;
    }
    if (subsamp != TJSAMP_420) {
        snprintf(errBuf, errBufLen, "unsupported JPEG subsampling: %d (want TJSAMP_420=%d)", subsamp, TJSAMP_420);
        tjDestroy(tjh);
        return -1;
    }

    unsigned long needed = tjBufSizeYUV2(width, 1, height, subsamp);
    if (needed > dstCap) {
        snprintf(errBuf, errBufLen, "output buffer too small: need %lu, have %lu", needed, dstCap);
        tjDestroy(tjh);
        return -1;
    }

    if (tjDecompressToYUV2(tjh, jpegBuf, jpegSize, dst, width, 1, height, 0) != 0) {
        snprintf(errBuf, errBufLen, "tjDecompressToYUV2: %s", tjGetErrorStr2(tjh));
        tjDestroy(tjh);
        return -1;
    }

    tjDestroy(tjh);
    return 0;
}
*/
import "C"

import (
	"fmt"
	"unsafe"
)

// tjErrBufSize is the size of the C error string buffer used by the
// tjPeekI420Size/tjDecodeI420 calls. 512 bytes is more than enough for any
// libjpeg-turbo error string.
const tjErrBufSize = 512

// decodeJPEGToI420 decodes a JPEG-compressed screencast frame directly to a
// packed I420 buffer via libjpeg-turbo, without the RGB intermediate that
// the default (non-vpx) build's decoder needs (see decoder_stub.go, whose
// doc comment this mirrors for the aliasing/reuse contract).
//
// dst, if it has enough capacity, is reused in place instead of allocating —
// transcodeLoop is the sole caller/owner (see decoder_stub.go's
// decodeJPEGToI420 doc comment for the full aliasing contract, identical
// here).
func decodeJPEGToI420(jpegBytes []byte, dst []byte) (i420 []byte, width, height int, err error) {
	if len(jpegBytes) == 0 {
		return nil, 0, 0, fmt.Errorf("video: empty JPEG buffer")
	}

	errBuf := (*C.char)(C.malloc(tjErrBufSize))
	defer C.free(unsafe.Pointer(errBuf))

	jpegPtr := (*C.uchar)(unsafe.Pointer(&jpegBytes[0]))

	var w, h C.int
	var needed C.ulong
	if C.tjPeekI420Size(jpegPtr, C.ulong(len(jpegBytes)), &w, &h, &needed, errBuf, C.int(tjErrBufSize)) != 0 {
		return nil, 0, 0, fmt.Errorf("video: JPEG header: %s", C.GoString(errBuf))
	}
	if w <= 0 || h <= 0 {
		return nil, 0, 0, fmt.Errorf("video: decoded image has zero dimension: %dx%d", int(w), int(h))
	}

	var buf []byte
	if cap(dst) >= int(needed) {
		buf = dst[:needed]
	} else {
		buf = make([]byte, needed)
	}

	dstPtr := (*C.uchar)(unsafe.Pointer(&buf[0]))
	if C.tjDecodeI420(jpegPtr, C.ulong(len(jpegBytes)), dstPtr, needed, errBuf, C.int(tjErrBufSize)) != 0 {
		return nil, 0, 0, fmt.Errorf("video: JPEG decode: %s", C.GoString(errBuf))
	}

	return buf, int(w), int(h), nil
}
