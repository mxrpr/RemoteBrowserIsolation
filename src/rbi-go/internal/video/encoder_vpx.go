//go:build vpx && cgo

// Real VP8 encoder using libvpx via cgo. Compiled only when building with
// -tags vpx (and cgo enabled), which requires libvpx-dev at build time and
// libvpx.so at runtime. The Go Dockerfile installs libvpx-dev and uses
// -tags vpx so that the production binary produces real VP8 output.
//
// Encoder tuning mirrors the C# VideoTrackStreamer:
//   - deadline=realtime + cpu-used=8: speed over compression (screen content).
//   - lag-in-frames=0: forbids lookahead, keeping latency to one frame.
//   - static-thresh=100: skip macroblocks below the residual threshold;
//     screencast content is mostly static between frames.
//   - Bitrate: 6 Mbit/s target (bumped from 3 Mbit/s to match the raised
//     1080p viewport cap in cmd/server/session.go).
package video

/*
#cgo CFLAGS: -I/usr/include
#cgo LDFLAGS: -lvpx

#include <stdlib.h>
#include <string.h>
#include <vpx/vpx_encoder.h>
#include <vpx/vp8cx.h>

// vpxEncCreate allocates and initialises a VP8 encoder context configured for
// real-time screen capture use. Returns NULL on any failure and fills errBuf
// (capacity errBufLen) with a null-terminated error string.
vpx_codec_ctx_t* vpxEncCreate(int width, int height, int bitrate_kbps, char* errBuf, int errBufLen) {
    vpx_codec_enc_cfg_t cfg;
    if (vpx_codec_enc_config_default(vpx_codec_vp8_cx(), &cfg, 0) != VPX_CODEC_OK) {
        snprintf(errBuf, errBufLen, "vpx_codec_enc_config_default failed");
        return NULL;
    }

    cfg.g_w = (unsigned int)width;
    cfg.g_h = (unsigned int)height;
    cfg.g_timebase.num = 1;
    cfg.g_timebase.den = 90000;         // 90 kHz RTP clock
    cfg.rc_target_bitrate = (unsigned int)bitrate_kbps;
    cfg.rc_min_quantizer = 4;
    cfg.rc_max_quantizer = 56;
    cfg.g_lag_in_frames = 0;             // no lookahead — zero extra latency
    // g_threads is deliberately pinned to 1, not left at libvpx's own default.
    // libvpx does NOT auto-detect cpu_count -- thread count is whatever the
    // caller supplies, and VP8 only parallelizes across token partitions. One
    // session = one encoder = one thread keeps N concurrent sessions mapped to
    // N cores; leaving this unset/higher would oversubscribe the scheduler as
    // session count grows.
    cfg.g_threads = 1;
    cfg.rc_end_usage = VPX_CBR;          // constant bitrate for streaming

    vpx_codec_ctx_t* ctx = (vpx_codec_ctx_t*)calloc(1, sizeof(vpx_codec_ctx_t));
    if (!ctx) {
        snprintf(errBuf, errBufLen, "calloc vpx_codec_ctx_t failed");
        return NULL;
    }

    vpx_codec_err_t err = vpx_codec_enc_init(ctx, vpx_codec_vp8_cx(), &cfg, 0);
    if (err != VPX_CODEC_OK) {
        snprintf(errBuf, errBufLen, "vpx_codec_enc_init: %s", vpx_codec_err_to_string(err));
        free(ctx);
        return NULL;
    }

    // deadline=realtime + cpu-used=8: fastest preset, trades compression for speed.
    vpx_codec_control(ctx, VP8E_SET_CPUUSED, 8);

    // static-thresh=100: skip macroblocks whose residual energy is below the
    // threshold; screen content is mostly static between frames.
    vpx_codec_control(ctx, VP8E_SET_STATIC_THRESHOLD, 100);

    return ctx;
}

// vpxEncodeFrame encodes one I420 frame. pts is the presentation timestamp in
// 90 kHz units. flags: use VPX_EFLAG_FORCE_KF for a forced keyframe.
// Returns the number of output bytes on success (stored in outBuf, capacity
// outCap), or -1 on error (errBuf filled).
int vpxEncodeFrame(vpx_codec_ctx_t* ctx,
                   const uint8_t* y, const uint8_t* u, const uint8_t* v,
                   int width, int height,
                   long long pts, vpx_enc_frame_flags_t flags,
                   uint8_t* outBuf, int outCap,
                   char* errBuf, int errBufLen) {
    vpx_image_t img;
    memset(&img, 0, sizeof(img));
    img.fmt = VPX_IMG_FMT_I420;
    img.w = img.d_w = (unsigned int)width;
    img.h = img.d_h = (unsigned int)height;
    img.planes[VPX_PLANE_Y] = (uint8_t*)y;
    img.planes[VPX_PLANE_U] = (uint8_t*)u;
    img.planes[VPX_PLANE_V] = (uint8_t*)v;
    img.stride[VPX_PLANE_Y] = width;
    img.stride[VPX_PLANE_U] = (width + 1) / 2;
    img.stride[VPX_PLANE_V] = (width + 1) / 2;

    vpx_codec_err_t err = vpx_codec_encode(ctx, &img, (vpx_codec_pts_t)pts, 1, flags, VPX_DL_REALTIME);
    if (err != VPX_CODEC_OK) {
        snprintf(errBuf, errBufLen, "vpx_codec_encode: %s", vpx_codec_err_to_string(err));
        return -1;
    }

    vpx_codec_iter_t iter = NULL;
    const vpx_codec_cx_pkt_t* pkt;
    int total = 0;
    while ((pkt = vpx_codec_get_cx_data(ctx, &iter)) != NULL) {
        if (pkt->kind != VPX_CODEC_CX_FRAME_PKT) {
            continue;
        }
        int sz = (int)pkt->data.frame.sz;
        if (total + sz > outCap) {
            snprintf(errBuf, errBufLen, "output buffer overflow: need %d bytes", total + sz);
            return -1;
        }
        memcpy(outBuf + total, pkt->data.frame.buf, sz);
        total += sz;
    }
    return total;
}

// vpxEncDestroy frees the encoder context allocated by vpxEncCreate.
void vpxEncDestroy(vpx_codec_ctx_t* ctx) {
    if (ctx) {
        vpx_codec_destroy(ctx);
        free(ctx);
    }
}
*/
import "C"

import (
	"fmt"
	"unsafe"
)

const (
	// vpxTargetBitrateKbps is the CBR target, reverted to the C# VideoTrackStreamer's
	// 3 Mbit/s alongside the 720p viewport cap reversion in cmd/server/session.go
	// (see that constant's comment for the concurrent-session capacity tradeoff).
	vpxTargetBitrateKbps = 3000

	// vpxOutputBufSize is the maximum encoded frame size we allocate. A 1280×720
	// keyframe at 3 Mbit/s is typically well under this; 1 MB provides a generous margin.
	vpxOutputBufSize = 1024 * 1024

	// vpxErrBufSize is the size of the C error string buffer used by vpxEncCreate
	// and vpxEncodeFrame. 512 bytes is more than enough for any libvpx error string.
	vpxErrBufSize = 512
)

// vpxEncoder implements VideoEncoder using libvpx via cgo. One instance per
// session; not thread-safe (owned by a single transcode goroutine).
type vpxEncoder struct {
	// ctx is the heap-allocated vpx_codec_ctx_t. Freed in Close.
	ctx *C.vpx_codec_ctx_t

	// pts is the presentation timestamp in 90 kHz units, incremented by 1 each
	// frame (libvpx uses pts to determine inter-frame ordering, not wall time).
	pts C.longlong

	// outBuf is a reusable C-heap buffer for encoded frame output, avoiding a
	// malloc/free on every Encode call.
	outBuf *C.uint8_t

	// errBuf is a reusable C-heap buffer for libvpx error strings, avoiding a
	// malloc/free pair (plus two cgo call transitions) on every Encode call —
	// it's only ever read on the (rare) error path.
	errBuf *C.char

	// closed indicates Close has been called. Subsequent Encode calls return an error.
	closed bool
}

// NewVP8Encoder creates a VP8 encoder sized for frames of the given dimensions.
// The encoder is tuned for real-time screen capture (CBR, realtime deadline,
// no lookahead). Returns an error if libvpx cannot be initialised.
func NewVP8Encoder(width, height int) (VideoEncoder, error) {
	errBuf := (*C.char)(C.malloc(vpxErrBufSize))
	defer C.free(unsafe.Pointer(errBuf))

	ctx := C.vpxEncCreate(C.int(width), C.int(height), C.int(vpxTargetBitrateKbps), errBuf, C.int(vpxErrBufSize))
	if ctx == nil {
		return nil, fmt.Errorf("video: VP8 encoder init: %s", C.GoString(errBuf))
	}

	outBuf := (*C.uint8_t)(C.malloc(vpxOutputBufSize))
	encErrBuf := (*C.char)(C.malloc(vpxErrBufSize))

	return &vpxEncoder{ctx: ctx, outBuf: outBuf, errBuf: encErrBuf}, nil
}

// Encode encodes one I420 frame to VP8. yuvI420 must be a packed I420 buffer
// (Y plane then U plane then V plane, no row padding) with byte length
// width*height*3/2. forceKeyFrame emits a full intra frame.
func (e *vpxEncoder) Encode(yuvI420 []byte, width, height int, forceKeyFrame bool) ([]byte, error) {
	if e.closed {
		return nil, fmt.Errorf("video: Encode called on closed VP8 encoder")
	}

	chromaW := (width + 1) / 2
	chromaH := (height + 1) / 2
	ySize := width * height
	uvSize := chromaW * chromaH

	expectedLen := ySize + 2*uvSize
	if len(yuvI420) < expectedLen {
		return nil, fmt.Errorf("video: I420 buffer too small: have %d, need %d", len(yuvI420), expectedLen)
	}

	yPtr := (*C.uint8_t)(unsafe.Pointer(&yuvI420[0]))
	uPtr := (*C.uint8_t)(unsafe.Pointer(&yuvI420[ySize]))
	vPtr := (*C.uint8_t)(unsafe.Pointer(&yuvI420[ySize+uvSize]))

	var flags C.vpx_enc_frame_flags_t
	if forceKeyFrame {
		flags = C.VPX_EFLAG_FORCE_KF
	}

	n := C.vpxEncodeFrame(
		e.ctx,
		yPtr, uPtr, vPtr,
		C.int(width), C.int(height),
		e.pts, flags,
		e.outBuf, C.int(vpxOutputBufSize),
		e.errBuf, C.int(vpxErrBufSize),
	)
	e.pts++

	if n < 0 {
		return nil, fmt.Errorf("video: VP8 encode: %s", C.GoString(e.errBuf))
	}
	if n == 0 {
		return nil, nil
	}

	// Copy the encoded bytes out of the C-heap buffer into a Go slice.
	out := make([]byte, int(n))
	copy(out, (*[1 << 30]byte)(unsafe.Pointer(e.outBuf))[:n:n])
	return out, nil
}

// Close destroys the libvpx encoder context and frees the output buffer.
// Must be called exactly once at session end to avoid resource leaks.
func (e *vpxEncoder) Close() {
	if e.closed {
		return
	}
	e.closed = true
	C.vpxEncDestroy(e.ctx)
	e.ctx = nil
	if e.outBuf != nil {
		C.free(unsafe.Pointer(e.outBuf))
		e.outBuf = nil
	}
	if e.errBuf != nil {
		C.free(unsafe.Pointer(e.errBuf))
		e.errBuf = nil
	}
}
