using System.Diagnostics;
using System.Threading.Channels;
using Microsoft.Playwright;
using SIPSorcery.Net;
using SIPSorceryMedia.Abstractions;
using SIPSorceryMedia.FFmpeg;

namespace RemoteBrowserIsolation.Server.Services;

public interface IVideoTrackStreamer
{
    Task StartAsync(RTCPeerConnection pc, HeadlessSession session, Uri targetUrl, CancellationToken cancellationToken = default);
}

// Streams the server-side rendered page to the client as a real VP8 video track over RTP.
// Pipeline per frame: CDP screencast JPEG -> MjpegToI420Decoder (MJPEG decode straight to I420,
// no RGB round-trip) -> FFmpeg VP8 encode -> RTCPeerConnection.SendVideo. This replaces iteration
// 2's JPEG-over-datachannel transport: RTP has
// no SIPSorcery-imposed throughput ceiling (the data channel's SCTP sender drains at a fixed
// ~75KB/s), and VP8 delta frames make unchanged screen regions nearly free, so quality and
// resolution can both go up while latency goes down.
public sealed class VideoTrackStreamer(ILogger<VideoTrackStreamer> logger) : IVideoTrackStreamer
{
    private const int JpegQuality = 80;

    // Target ~3Mbit/s for 720p-class content: high enough that text stays crisp after VP8,
    // low enough that a keyframe (~20KB) still leaves the encoder in a few ms.
    private const long TargetBitrate = 3_000_000;
    private const long MinBitrate = 1_000_000;
    private const long MaxBitrate = 4_000_000;

    // VP8 delta frames only decode against prior frames, so a client that joins mid-stream or
    // suffers packet loss needs periodic keyframes to (re)lock onto the stream.
    private static readonly TimeSpan KeyFrameInterval = TimeSpan.FromSeconds(5);

    // RTP video clock is fixed at 90kHz by RFC; SendVideo takes frame duration in these units.
    private const int RtpVideoClockRate = 90_000;

    // Opens a CDP screencast on the session's page and pumps frames through decode->encode->RTP.
    // Frames flow through a latest-wins mailbox (capacity 1, DropOldest) exactly like iteration 2:
    // the CDP handler acks immediately and only stores the newest JPEG, while a single consumer
    // transcodes and sends. This bounds staleness under constant page animation and keeps the
    // stateful VP8 encoder strictly single-threaded.
    public async Task StartAsync(RTCPeerConnection pc, HeadlessSession session, Uri targetUrl, CancellationToken cancellationToken = default)
    {
        var cdp = await session.Context.NewCDPSessionAsync(session.Page);

        var mailbox = Channel.CreateBounded<byte[]>(new BoundedChannelOptions(1)
        {
            FullMode = BoundedChannelFullMode.DropOldest,
            SingleReader = true,
        });

        // End the transcode loop as soon as the peer connection dies; teardown of the browser
        // context itself is WebRtcSessionManager's job.
        pc.onconnectionstatechange += state =>
        {
            if (state is RTCPeerConnectionState.closed or RTCPeerConnectionState.failed or RTCPeerConnectionState.disconnected)
            {
                mailbox.Writer.TryComplete();
            }
        };

        cdp.Event("Page.screencastFrame").OnEvent += async (_, json) =>
        {
            if (json is null)
            {
                return;
            }

            try
            {
                var element = json.Value;
                var frameBytes = Convert.FromBase64String(element.GetProperty("data").GetString()!);
                var sessionId = element.GetProperty("sessionId").GetInt32();

                mailbox.Writer.TryWrite(frameBytes);

                // Ack right away, independent of transcode progress, so Chromium keeps producing
                // fresh frames for the mailbox to conflate instead of stalling the screencast.
                await cdp.SendAsync("Page.screencastFrameAck", new Dictionary<string, object> { ["sessionId"] = sessionId });
            }
            catch (PlaywrightException ex) when (ex.Message.Contains("closed", StringComparison.OrdinalIgnoreCase))
            {
                // Session tearing down while an ack was in flight — routine, not worth a stack trace.
            }
            catch (Exception ex)
            {
                logger.LogWarning(ex, "Failed to receive/ack a screencast frame for {Url}", targetUrl);
            }
        };

        _ = TranscodeLoopAsync(pc, mailbox.Reader, targetUrl);

        // The page viewport already matches the requested size (see HeadlessBrowserSessionManager),
        // so maxWidth/maxHeight just need to not scale it down; screencast output == viewport.
        await cdp.SendAsync("Page.startScreencast", new Dictionary<string, object>
        {
            ["format"] = "jpeg",
            ["quality"] = JpegQuality,
            ["maxWidth"] = 4096,
            ["maxHeight"] = 4096,
        });
    }

    // Single consumer: decodes the newest JPEG, VP8-encodes it, and pushes it onto the RTP track,
    // until the mailbox closes on disconnect. Owns the FFmpeg decoder/encoder pair for the session —
    // the VP8 encoder is stateful (delta frames reference prior frames), so it must live exactly as
    // long as the stream and never be shared across sessions.
    private async Task TranscodeLoopAsync(RTCPeerConnection pc, ChannelReader<byte[]> reader, Uri targetUrl)
    {
        using var decoder = new MjpegToI420Decoder();
        // libvpx realtime tuning: "deadline=realtime" caps how long the encoder may spend per
        // frame, "cpu-used=8" trades a little compression efficiency for speed, "lag-in-frames=0"
        // forbids lookahead buffering (which would add whole frames of latency). Measured ~35%
        // faster per frame than the default "good" deadline at 720p.
        // "static-thresh=100" lets the encoder skip macroblocks whose residual is below the
        // threshold -- screencast content is mostly static between frames, so this both speeds
        // up encoding and saves bits. "token_partitions=3" splits the bitstream into 8
        // independent partitions so the encoder threads (SetThreadCount below) can parallelise
        // within one frame. (row-mt is VP9-only, does not apply to VP8.)
        var realtimeOptions = new Dictionary<string, string>
        {
            ["deadline"] = "realtime",
            ["cpu-used"] = "8",
            ["lag-in-frames"] = "0",
            ["static-thresh"] = "100",
            ["token_partitions"] = "3",
        };
        using var encoder = new FFmpegVideoEncoder(realtimeOptions);
        encoder.SetBitrate(TargetBitrate, null, MinBitrate, MaxBitrate);
        // libvpx multithreading: thread_count is applied to the codec context when the encoder
        // is initialised on the first frame. Without this the encode runs on a single core and
        // is the dominant per-frame cost (~15-20ms at 720p).
        encoder.SetThreadCount(Environment.ProcessorCount);

        var lastFrameAt = Stopwatch.GetTimestamp();
        var lastKeyFrameAt = Stopwatch.GetTimestamp();
        var frameCount = 0L;

        try
        {
            await foreach (var jpegBytes in reader.ReadAllAsync())
            {
                var t0 = Stopwatch.GetTimestamp();

                if (!decoder.TryDecode(jpegBytes, out var i420Ptr, out var frameWidth, out var frameHeight))
                {
                    logger.LogWarning("JPEG decode produced no frame for {Url}", targetUrl);
                    continue;
                }

                if (Stopwatch.GetElapsedTime(lastKeyFrameAt) >= KeyFrameInterval)
                {
                    encoder.ForceKeyFrame();
                    lastKeyFrameAt = Stopwatch.GetTimestamp();
                }

                var rawImage = new RawImage
                {
                    Width = frameWidth,
                    Height = frameHeight,
                    Stride = frameWidth,
                    Sample = i420Ptr,
                    PixelFormat = VideoPixelFormatsEnum.I420,
                };
                var encoded = encoder.EncodeVideoFaster(rawImage, VideoCodecsEnum.VP8);
                if (encoded is not { Length: > 0 })
                {
                    continue;
                }

                // RTP timestamps advance by wall-clock time between frames (90kHz units); the
                // screencast produces frames at an irregular, repaint-driven rate.
                var elapsed = Stopwatch.GetElapsedTime(lastFrameAt);
                lastFrameAt = Stopwatch.GetTimestamp();
                var durationRtpUnits = (uint)Math.Max(1, elapsed.TotalSeconds * RtpVideoClockRate);

                pc.SendVideo(durationRtpUnits, encoded);
                frameCount++;

                logger.LogDebug(
                    "Video frame {Count} for {Url}: jpeg {JpegBytes}B -> vp8 {Vp8Bytes}B in {Ms:F1}ms",
                    frameCount, targetUrl, jpegBytes.Length, encoded.Length, Stopwatch.GetElapsedTime(t0).TotalMilliseconds);
            }
        }
        catch (Exception ex)
        {
            // Send/encode failing means the peer connection or encoder is gone — the session is
            // over anyway, so log and let the loop end rather than crashing anything above it.
            logger.LogWarning(ex, "Video transcode loop stopped for {Url} after {Count} frames", targetUrl, frameCount);
        }

        logger.LogInformation("Video stream ended for {Url} after {Count} frames", targetUrl, frameCount);
    }
}
