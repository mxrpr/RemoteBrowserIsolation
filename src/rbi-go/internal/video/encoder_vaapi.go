//go:build vpx && cgo

// Real hardware H.264 encoder using libavcodec's h264_vaapi encoder via cgo.
// Compiled only under -tags vpx (same production tag as encoder_vpx.go /
// decoder_turbojpeg.go), so one Docker build produces a single binary with both
// the software VP8 path and this hardware H.264 path compiled in; the codec is
// chosen at runtime per session (see NewVideoEncoder + the codec resolution in
// cmd/server).
//
// Why H.264 and not VP8 here: the target host's Intel Iris Xe (Gen12) media
// engine has no VP8 *encode* support — QuickSync dropped VP8 encode after older
// generations — but does hardware-encode H.264/HEVC/VP9/AV1. H.264 Constrained
// Baseline is chosen for the lowest realtime latency (no B-frames → no reorder)
// and universal WebRTC browser support. pion's H.264 RTP payloader consumes the
// Annex-B byte-stream that h264_vaapi emits.
//
// !! BUILD/RUNTIME NOTE: this file requires libavcodec/libavutil (-dev at build,
// .so at runtime), the Intel iHD VAAPI driver, and a VAAPI render node
// (/dev/dri/renderD128) passed into the container. It cannot be compiled or
// exercised without those, so it is verified in the GPU container, not on a
// dev box without the headers/GPU. See plans/12_vaapi_hw_encode.md Step 5.
package video

/*
#cgo CFLAGS: -I/usr/include
#cgo LDFLAGS: -lavcodec -lavutil

#include <stdlib.h>
#include <string.h>
#include <libavcodec/avcodec.h>
#include <libavutil/hwcontext.h>
#include <libavutil/opt.h>

// H264VaapiEnc bundles the libavcodec VAAPI encoder state for one session.
typedef struct {
    AVBufferRef*    hwDevice;   // VAAPI device (render node)
    AVCodecContext* avctx;      // h264_vaapi encoder context
    AVFrame*        swFrame;    // reusable software NV12 frame (CPU side)
    AVFrame*        hwFrame;    // reusable GPU surface (uploaded target)
    AVPacket*       pkt;        // reusable output packet
    long long       pts;        // presentation timestamp, incremented per frame
} H264VaapiEnc;

// Forward declaration: create's error paths free partially-built state through this.
void h264vaapiEncDestroyImpl(H264VaapiEnc* e);

// h264vaapiEncCreate opens a VAAPI H.264 encoder on renderNode sized width x
// height at bitrate_kbps. Returns NULL and fills errBuf on any failure. All
// dimensions must be even (H.264 4:2:0). Constrained Baseline, no B-frames,
// large GOP (keyframes are forced explicitly per-frame like the VP8 path).
H264VaapiEnc* h264vaapiEncCreate(int width, int height, int bitrate_kbps,
                                 const char* renderNode,
                                 char* errBuf, int errBufLen) {
    H264VaapiEnc* e = (H264VaapiEnc*)calloc(1, sizeof(H264VaapiEnc));
    if (!e) { snprintf(errBuf, errBufLen, "calloc H264VaapiEnc failed"); return NULL; }

    int ret = av_hwdevice_ctx_create(&e->hwDevice, AV_HWDEVICE_TYPE_VAAPI,
                                     renderNode, NULL, 0);
    if (ret < 0) {
        snprintf(errBuf, errBufLen, "av_hwdevice_ctx_create(%s): %d", renderNode, ret);
        free(e);
        return NULL;
    }

    const AVCodec* codec = avcodec_find_encoder_by_name("h264_vaapi");
    if (!codec) {
        snprintf(errBuf, errBufLen, "h264_vaapi encoder not found in this libavcodec build");
        av_buffer_unref(&e->hwDevice);
        free(e);
        return NULL;
    }

    e->avctx = avcodec_alloc_context3(codec);
    if (!e->avctx) {
        snprintf(errBuf, errBufLen, "avcodec_alloc_context3 failed");
        av_buffer_unref(&e->hwDevice);
        free(e);
        return NULL;
    }

    e->avctx->width       = width;
    e->avctx->height      = height;
    e->avctx->pix_fmt     = AV_PIX_FMT_VAAPI;   // frames fed to the encoder are GPU surfaces
    e->avctx->time_base.num = 1;
    e->avctx->time_base.den = 90000;            // 90 kHz RTP clock
    e->avctx->framerate.num = 0;                // variable (screencast) — let rc use time_base
    e->avctx->framerate.den = 1;
    e->avctx->bit_rate    = (long long)bitrate_kbps * 1000;
    e->avctx->gop_size    = 3000;               // effectively "only forced" keyframes
    e->avctx->max_b_frames = 0;                 // no reorder latency (realtime)
    e->avctx->profile     = FF_PROFILE_H264_CONSTRAINED_BASELINE;

    // Build the NV12 hardware frame pool the encoder pulls from.
    AVBufferRef* framesRef = av_hwframe_ctx_alloc(e->hwDevice);
    if (!framesRef) {
        snprintf(errBuf, errBufLen, "av_hwframe_ctx_alloc failed");
        avcodec_free_context(&e->avctx);
        av_buffer_unref(&e->hwDevice);
        free(e);
        return NULL;
    }
    AVHWFramesContext* framesCtx = (AVHWFramesContext*)framesRef->data;
    framesCtx->format            = AV_PIX_FMT_VAAPI;
    framesCtx->sw_format         = AV_PIX_FMT_NV12;  // VAAPI encoders consume NV12
    framesCtx->width             = width;
    framesCtx->height            = height;
    framesCtx->initial_pool_size = 4;
    ret = av_hwframe_ctx_init(framesRef);
    if (ret < 0) {
        snprintf(errBuf, errBufLen, "av_hwframe_ctx_init: %d", ret);
        av_buffer_unref(&framesRef);
        avcodec_free_context(&e->avctx);
        av_buffer_unref(&e->hwDevice);
        free(e);
        return NULL;
    }
    e->avctx->hw_frames_ctx = av_buffer_ref(framesRef);
    av_buffer_unref(&framesRef);

    // Low-latency VAAPI RC: constant bitrate, minimal async depth.
    av_opt_set(e->avctx->priv_data, "rc_mode", "CBR", 0);
    e->avctx->max_b_frames = 0;

    ret = avcodec_open2(e->avctx, codec, NULL);
    if (ret < 0) {
        snprintf(errBuf, errBufLen, "avcodec_open2(h264_vaapi): %d", ret);
        avcodec_free_context(&e->avctx);
        av_buffer_unref(&e->hwDevice);
        free(e);
        return NULL;
    }

    e->swFrame = av_frame_alloc();
    e->hwFrame = av_frame_alloc();
    e->pkt     = av_packet_alloc();
    if (!e->swFrame || !e->hwFrame || !e->pkt) {
        snprintf(errBuf, errBufLen, "av_frame/av_packet alloc failed");
        h264vaapiEncDestroyImpl(e);
        return NULL;
    }

    // The software NV12 frame is reused every call: allocate its backing buffer once.
    e->swFrame->format = AV_PIX_FMT_NV12;
    e->swFrame->width  = width;
    e->swFrame->height = height;
    ret = av_frame_get_buffer(e->swFrame, 0);
    if (ret < 0) {
        snprintf(errBuf, errBufLen, "av_frame_get_buffer(NV12): %d", ret);
        h264vaapiEncDestroyImpl(e);
        return NULL;
    }

    return e;
}

// h264vaapiEncDestroyImpl frees all encoder resources. Split from the exported
// destroy so create's error paths can reuse it.
void h264vaapiEncDestroyImpl(H264VaapiEnc* e) {
    if (!e) return;
    if (e->pkt)     av_packet_free(&e->pkt);
    if (e->hwFrame) av_frame_free(&e->hwFrame);
    if (e->swFrame) av_frame_free(&e->swFrame);
    if (e->avctx)   avcodec_free_context(&e->avctx);
    if (e->hwDevice) av_buffer_unref(&e->hwDevice);
    free(e);
}

// packI420ToNV12 fills the (already-allocated) NV12 swFrame from a packed I420
// input buffer. Y is copied row by row (respecting the frame's linesize
// padding); U and V are interleaved into the single NV12 chroma plane.
static void packI420ToNV12(AVFrame* f, const uint8_t* i420, int w, int h) {
    int cw = w / 2, ch = h / 2;
    const uint8_t* srcY = i420;
    const uint8_t* srcU = srcY + w * h;
    const uint8_t* srcV = srcU + cw * ch;

    for (int y = 0; y < h; y++) {
        memcpy(f->data[0] + y * f->linesize[0], srcY + y * w, w);
    }
    for (int y = 0; y < ch; y++) {
        uint8_t* dst = f->data[1] + y * f->linesize[1];
        const uint8_t* u = srcU + y * cw;
        const uint8_t* v = srcV + y * cw;
        for (int x = 0; x < cw; x++) {
            dst[2 * x]     = u[x];
            dst[2 * x + 1] = v[x];
        }
    }
}

// h264vaapiEncodeFrame encodes one packed-I420 frame. It packs to NV12, uploads
// to a GPU surface, submits, and copies any produced Annex-B bytes to outBuf.
// Returns the number of output bytes, or -1 (errBuf filled) on error.
int h264vaapiEncodeFrame(H264VaapiEnc* e, const uint8_t* i420, int width, int height,
                         int forceKeyFrame, uint8_t* outBuf, int outCap,
                         char* errBuf, int errBufLen) {
    packI420ToNV12(e->swFrame, i420, width, height);

    av_frame_unref(e->hwFrame);
    int ret = av_hwframe_get_buffer(e->avctx->hw_frames_ctx, e->hwFrame, 0);
    if (ret < 0) { snprintf(errBuf, errBufLen, "av_hwframe_get_buffer: %d", ret); return -1; }

    ret = av_hwframe_transfer_data(e->hwFrame, e->swFrame, 0);
    if (ret < 0) { snprintf(errBuf, errBufLen, "av_hwframe_transfer_data: %d", ret); return -1; }

    e->hwFrame->pts = e->pts++;
    e->hwFrame->pict_type = forceKeyFrame ? AV_PICTURE_TYPE_I : AV_PICTURE_TYPE_NONE;

    ret = avcodec_send_frame(e->avctx, e->hwFrame);
    if (ret < 0) { snprintf(errBuf, errBufLen, "avcodec_send_frame: %d", ret); return -1; }

    int total = 0;
    while (1) {
        ret = avcodec_receive_packet(e->avctx, e->pkt);
        if (ret == AVERROR(EAGAIN) || ret == AVERROR_EOF) break;
        if (ret < 0) { snprintf(errBuf, errBufLen, "avcodec_receive_packet: %d", ret); return -1; }

        if (total + e->pkt->size > outCap) {
            snprintf(errBuf, errBufLen, "output buffer overflow: need %d", total + e->pkt->size);
            av_packet_unref(e->pkt);
            return -1;
        }
        memcpy(outBuf + total, e->pkt->data, e->pkt->size);
        total += e->pkt->size;
        av_packet_unref(e->pkt);
    }
    return total;
}

// h264vaapiEncDestroy is the exported cleanup entry point.
void h264vaapiEncDestroy(H264VaapiEnc* e) { h264vaapiEncDestroyImpl(e); }
*/
import "C"

import (
	"fmt"
	"sync"
	"unsafe"
)

const (
	// vaapiRenderNode is the DRM render node used for VAAPI. Fixed to the
	// canonical Linux path; the container must pass --device /dev/dri/renderD128.
	vaapiRenderNode = "/dev/dri/renderD128"

	// h264TargetBitrateKbps matches the VP8 path's target so switching codecs
	// does not change the bandwidth budget.
	h264TargetBitrateKbps = 3000

	// h264OutputBufSize is the max encoded frame size we allocate. A 720p H.264
	// IDR at 3 Mbit/s is well under this; 1 MB is a generous margin.
	h264OutputBufSize = 1024 * 1024

	// h264ErrBufSize is the C error-string buffer size.
	h264ErrBufSize = 512
)

// h264Encoder implements VideoEncoder using libavcodec's h264_vaapi via cgo. One
// instance per session; not thread-safe (owned by a single transcode goroutine).
type h264Encoder struct {
	enc    *C.H264VaapiEnc
	outBuf *C.uint8_t
	errBuf *C.char
	closed bool
}

// NewH264Encoder creates a VAAPI H.264 encoder sized for width×height frames.
// Returns an error if VAAPI or the h264_vaapi encoder cannot be initialised —
// callers treat that as "GPU encode unavailable" and either fall back (Auto) or
// fail the session loudly (Gpu).
func NewH264Encoder(width, height int) (VideoEncoder, error) {
	// H.264 4:2:0 requires even dimensions; the viewport bounds already enforce
	// this, but guard so a bad value fails here rather than deep in libavcodec.
	if width%2 != 0 || height%2 != 0 {
		return nil, fmt.Errorf("video: H.264 requires even dimensions, got %dx%d", width, height)
	}

	errBuf := (*C.char)(C.malloc(h264ErrBufSize))
	defer C.free(unsafe.Pointer(errBuf))

	node := C.CString(vaapiRenderNode)
	defer C.free(unsafe.Pointer(node))

	enc := C.h264vaapiEncCreate(C.int(width), C.int(height), C.int(h264TargetBitrateKbps),
		node, errBuf, C.int(h264ErrBufSize))
	if enc == nil {
		return nil, fmt.Errorf("video: VAAPI H.264 init: %s", C.GoString(errBuf))
	}

	return &h264Encoder{
		enc:    enc,
		outBuf: (*C.uint8_t)(C.malloc(h264OutputBufSize)),
		errBuf: (*C.char)(C.malloc(h264ErrBufSize)),
	}, nil
}

// Encode encodes one packed-I420 frame to H.264 Annex-B.
func (e *h264Encoder) Encode(yuvI420 []byte, width, height int, forceKeyFrame bool) ([]byte, error) {
	if e.closed {
		return nil, fmt.Errorf("video: Encode called on closed H.264 encoder")
	}

	chromaW := (width + 1) / 2
	chromaH := (height + 1) / 2
	expectedLen := width*height + 2*chromaW*chromaH
	if len(yuvI420) < expectedLen {
		return nil, fmt.Errorf("video: I420 buffer too small: have %d, need %d", len(yuvI420), expectedLen)
	}

	var forceKey C.int
	if forceKeyFrame {
		forceKey = 1
	}

	n := C.h264vaapiEncodeFrame(
		e.enc,
		(*C.uint8_t)(unsafe.Pointer(&yuvI420[0])),
		C.int(width), C.int(height),
		forceKey,
		e.outBuf, C.int(h264OutputBufSize),
		e.errBuf, C.int(h264ErrBufSize),
	)
	if n < 0 {
		return nil, fmt.Errorf("video: H.264 encode: %s", C.GoString(e.errBuf))
	}
	if n == 0 {
		return nil, nil
	}

	out := make([]byte, int(n))
	copy(out, (*[1 << 30]byte)(unsafe.Pointer(e.outBuf))[:n:n])
	return out, nil
}

// Close destroys the encoder and frees the output/error buffers. Idempotent.
func (e *h264Encoder) Close() {
	if e.closed {
		return
	}
	e.closed = true
	C.h264vaapiEncDestroy(e.enc)
	e.enc = nil
	if e.outBuf != nil {
		C.free(unsafe.Pointer(e.outBuf))
		e.outBuf = nil
	}
	if e.errBuf != nil {
		C.free(unsafe.Pointer(e.errBuf))
		e.errBuf = nil
	}
}

// h264AvailableOnce caches the one-shot hardware probe result.
var (
	h264AvailableOnce sync.Once
	h264AvailableVal  bool
)

// H264Available reports whether a usable VAAPI H.264 encoder can actually be
// created on this host. It really constructs (and immediately destroys) a small
// encoder once, so codec resolution (Auto/Gpu) reflects true encode capability,
// not merely the presence of a render node. Result is cached for process life.
func H264Available() bool {
	h264AvailableOnce.Do(func() {
		enc, err := NewH264Encoder(320, 240)
		if err != nil {
			h264AvailableVal = false
			return
		}
		enc.Close()
		h264AvailableVal = true
	})
	return h264AvailableVal
}
