using System.Runtime.InteropServices;
using FFmpeg.AutoGen;

namespace RemoteBrowserIsolation.Server.Services;

// Decodes MJPEG (JPEG) frames straight to a contiguous I420 (planar YUV 4:2:0) buffer using
// FFmpeg.AutoGen directly, bypassing SIPSorceryMedia.FFmpeg's DecodeFaster. DecodeFaster always
// converts the decoded frame to RGB24 via an sws_scale pass, and the VP8 encoder then converts
// RGB24 back to YUV420P via a second sws_scale pass -- two full-frame conversions per frame that
// cancel out, since MJPEG's native decode output (YUVJ420P) is already what the VP8 encoder wants.
// Not thread-safe: owned and driven by exactly one transcode loop, same lifetime discipline as the
// FFmpegVideoEncoder it feeds (the decoder context/frame/packet live exactly as long as the stream).
public sealed unsafe class MjpegToI420Decoder : IDisposable
{
    private AVCodecContext* context;
    private AVPacket* packet;
    private AVFrame* frame;

    private IntPtr i420Buffer = IntPtr.Zero;
    private int bufferWidth;
    private int bufferHeight;

    private bool isDisposed;

    // Allocates and opens the MJPEG decoder context. Throws if the MJPEG decoder isn't
    // available in the linked FFmpeg build (should never happen -- MJPEG is a core codec).
    public MjpegToI420Decoder()
    {
        var codec = ffmpeg.avcodec_find_decoder(AVCodecID.AV_CODEC_ID_MJPEG);
        if (codec == null)
        {
            throw new InvalidOperationException("FFmpeg build has no MJPEG decoder available.");
        }

        context = ffmpeg.avcodec_alloc_context3(codec);
        if (context == null)
        {
            throw new InvalidOperationException("Failed to allocate MJPEG decoder context.");
        }

        var openResult = ffmpeg.avcodec_open2(context, codec, null);
        if (openResult < 0)
        {
            throw new InvalidOperationException($"Failed to open MJPEG decoder (error {openResult}).");
        }

        packet = ffmpeg.av_packet_alloc();
        frame = ffmpeg.av_frame_alloc();
    }

    // Decodes one JPEG image synchronously (MJPEG is intra-only: one packet in, one frame out).
    // On success, 'i420' points at an internal reusable buffer laid out as contiguous I420
    // (full Y plane, then full U plane, then full V plane, no row padding) valid until the next
    // TryDecode call. Returns false if the packet produced no frame, or if the decoded pixel
    // format isn't the 4:2:0 chroma layout I420 output assumes (defensive; Chromium's JPEG
    // screencast output is always 4:2:0 in practice).
    public bool TryDecode(byte[] jpegBytes, out IntPtr i420, out int width, out int height)
    {
        ObjectDisposedException.ThrowIf(isDisposed, this);

        fixed (byte* pJpeg = jpegBytes)
        {
            packet->data = pJpeg;
            packet->size = jpegBytes.Length;

            var sendResult = ffmpeg.avcodec_send_packet(context, packet);
            if (sendResult < 0)
            {
                width = 0;
                height = 0;
                i420 = IntPtr.Zero;
                return false;
            }

            var receiveResult = ffmpeg.avcodec_receive_frame(context, frame);
            if (receiveResult < 0)
            {
                width = 0;
                height = 0;
                i420 = IntPtr.Zero;
                return false;
            }
        }

        try
        {
            // Chromium's JPEG screencast output is 4:2:0, decoded by libavcodec as
            // AV_PIX_FMT_YUVJ420P (full-range). We deliberately treat it as the encoder's
            // AV_PIX_FMT_YUV420P (limited-range) rather than spend an sws_scale pass on a
            // range conversion -- a one-time slight contrast shift, invisible after VP8
            // quantisation, and standard practice in real-time pipelines.
            if (frame->format != (int)AVPixelFormat.AV_PIX_FMT_YUVJ420P
                && frame->format != (int)AVPixelFormat.AV_PIX_FMT_YUV420P)
            {
                width = 0;
                height = 0;
                i420 = IntPtr.Zero;
                return false;
            }

            width = frame->width;
            height = frame->height;

            EnsureBuffer(width, height);

            var chromaWidth = (width + 1) / 2;
            var chromaHeight = (height + 1) / 2;
            var ySize = width * height;
            var chromaPlaneSize = chromaWidth * chromaHeight;

            CopyPlane((IntPtr)frame->data[0], frame->linesize[0], i420Buffer, width, width, height);
            CopyPlane((IntPtr)frame->data[1], frame->linesize[1], i420Buffer + ySize, chromaWidth, chromaWidth, chromaHeight);
            CopyPlane((IntPtr)frame->data[2], frame->linesize[2], i420Buffer + ySize + chromaPlaneSize, chromaWidth, chromaWidth, chromaHeight);

            i420 = i420Buffer;
            return true;
        }
        finally
        {
            ffmpeg.av_frame_unref(frame);
        }
    }

    // (Re)allocates the reusable I420 output buffer to fit the given frame size. A no-op once
    // the session's viewport size has been seen (viewport is fixed for the lifetime of a
    // session -- see HeadlessBrowserSessionManager), so this only runs on the first frame.
    private void EnsureBuffer(int width, int height)
    {
        if (i420Buffer != IntPtr.Zero && width == bufferWidth && height == bufferHeight)
        {
            return;
        }

        if (i420Buffer != IntPtr.Zero)
        {
            Marshal.FreeHGlobal(i420Buffer);
        }

        var chromaWidth = (width + 1) / 2;
        var chromaHeight = (height + 1) / 2;
        var totalSize = (width * height) + 2 * (chromaWidth * chromaHeight);

        i420Buffer = Marshal.AllocHGlobal(totalSize);
        bufferWidth = width;
        bufferHeight = height;
    }

    // Copies one plane row-by-row from FFmpeg's (possibly padded) linesize layout into a
    // tightly-packed destination -- required because RawImage/EncodeVideoFaster's I420 path
    // (via av_image_fill_arrays) assumes standard tightly-packed planar layout with no row
    // padding, while FFmpeg's decoded frame linesize is often wider than the plane's pixel
    // width (e.g. a 1280-wide plane may have linesize 1312).
    private static void CopyPlane(IntPtr source, int sourceStride, IntPtr destination, int rowBytes, int width, int height)
    {
        for (var row = 0; row < height; row++)
        {
            Buffer.MemoryCopy(
                (byte*)source + row * sourceStride,
                (byte*)destination + row * rowBytes,
                rowBytes,
                rowBytes);
        }
    }

    // Frees the decoder context, packet, frame, and reusable I420 buffer.
    public void Dispose()
    {
        if (isDisposed)
        {
            return;
        }

        isDisposed = true;

        if (frame != null)
        {
            fixed (AVFrame** pFrame = &frame)
            {
                ffmpeg.av_frame_free(pFrame);
            }
        }

        if (packet != null)
        {
            fixed (AVPacket** pPacket = &packet)
            {
                ffmpeg.av_packet_free(pPacket);
            }
        }

        if (context != null)
        {
            fixed (AVCodecContext** pContext = &context)
            {
                ffmpeg.avcodec_free_context(pContext);
            }
        }

        if (i420Buffer != IntPtr.Zero)
        {
            Marshal.FreeHGlobal(i420Buffer);
            i420Buffer = IntPtr.Zero;
        }
    }
}
