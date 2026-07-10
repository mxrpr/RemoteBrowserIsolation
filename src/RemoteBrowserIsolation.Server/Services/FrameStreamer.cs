using System.Buffers.Binary;
using System.Diagnostics;
using System.Threading.Channels;
using Microsoft.Playwright;
using SIPSorcery.Net;

namespace RemoteBrowserIsolation.Server.Services;

public interface IFrameStreamer
{
    Task StartAsync(RTCPeerConnection pc, RTCDataChannel frameChannel, HeadlessSession session, Uri targetUrl, int frameWidth, int frameHeight, CancellationToken cancellationToken = default);
}

// Captures the server-side rendered page as a live sequence of JPEG frames via Chrome DevTools
// Protocol screencasting, and streams each frame to the client over the WebRTC data channel as
// [4-byte big-endian length][JPEG bytes] so the client can reassemble frames from a continuous byte
// stream regardless of how send() chunking split them across individual data-channel messages.
public sealed class FrameStreamer(ILogger<FrameStreamer> logger) : IFrameStreamer
{
    // SIPSorcery's managed SCTP sender drains bufferedAmount at a fixed rate of roughly 75KB/s
    // regardless of actual network capacity (measured directly: a ~120KB frame took ~1.55s to
    // drain, a ~51KB frame took ~0.72s — both land on the same ~75KB/s rate, so this is a fixed
    // throughput ceiling in the sender, not a congestion-window/bandwidth effect). Frame payload
    // size is therefore the only lever available to control per-frame send latency without
    // switching transports; these values target frames under ~15KB so a send drains in well
    // under 200ms.
    private const string JpegFormat = "jpeg";
    private const int JpegQuality = 40;

    // Opens a CDP session on the page and starts screencasting. Frames flow through a latest-wins
    // mailbox: the CDP event handler acks immediately and only stores the newest frame, while a
    // single consumer loop sends whatever is newest when the channel is free. On a page with
    // constant animation the send pipe (~75KB/s, see above) can't keep up with frame production —
    // sending every frame would build an ever-growing queue, so a click's visual feedback would sit
    // behind seconds of stale animation frames. Conflating to the newest frame bounds that latency
    // to roughly one frame's send time, at the cost of dropping intermediate animation frames. The
    // single consumer also guarantees sends never interleave chunks of two different frames on the
    // wire (which would corrupt the client's byte-stream reassembly).
    // frameWidth/frameHeight MUST equal the page's viewport size (1:1): if the screencast scaled
    // the frame down from a larger viewport, client canvas coordinates would no longer equal page
    // coordinates and every replayed click would land at the wrong position. Both values come from
    // the same clamped client-requested size (see WebRtcSessionManager).
    public async Task StartAsync(RTCPeerConnection pc, RTCDataChannel frameChannel, HeadlessSession session, Uri targetUrl, int frameWidth, int frameHeight, CancellationToken cancellationToken = default)
    {
        var cdp = await session.Context.NewCDPSessionAsync(session.Page);

        var mailbox = Channel.CreateBounded<byte[]>(new BoundedChannelOptions(1)
        {
            FullMode = BoundedChannelFullMode.DropOldest,
            SingleReader = true,
        });
        frameChannel.onclose += () => mailbox.Writer.TryComplete();

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

                // DropOldest capacity-1 mailbox: replaces any still-unsent older frame.
                mailbox.Writer.TryWrite(frameBytes);

                // Ack right away, independent of send progress, so Chromium keeps producing fresh
                // frames for the mailbox to conflate instead of stalling the screencast.
                await cdp.SendAsync("Page.screencastFrameAck", new Dictionary<string, object> { ["sessionId"] = sessionId });
            }
            catch (PlaywrightException ex) when (ex.Message.Contains("closed", StringComparison.OrdinalIgnoreCase))
            {
                // Session tearing down while an ack was in flight (TargetClosedException is internal
                // to Playwright, hence the message match) — routine, not worth a stack trace.
            }
            catch (Exception ex)
            {
                logger.LogWarning(ex, "Failed to receive/ack a screencast frame for {Url}", targetUrl);
            }
        };

        _ = SendLoopAsync(pc, frameChannel, mailbox.Reader, targetUrl);

        await cdp.SendAsync("Page.startScreencast", new Dictionary<string, object>
        {
            ["format"] = JpegFormat,
            ["quality"] = JpegQuality,
            ["maxWidth"] = frameWidth,
            ["maxHeight"] = frameHeight,
        });
    }

    // Single consumer: sends the newest available frame, one at a time, until the mailbox closes.
    private async Task SendLoopAsync(RTCPeerConnection pc, RTCDataChannel frameChannel, ChannelReader<byte[]> reader, Uri targetUrl)
    {
        try
        {
            await foreach (var frameBytes in reader.ReadAllAsync())
            {
                var frameStart = Stopwatch.GetTimestamp();
                var (sendElapsed, drainElapsed) = await SendFrameAsync(pc, frameChannel, frameBytes);

                logger.LogInformation(
                    "Frame for {Url}: {Bytes} bytes, send {SendMs:F1}ms, drain {DrainMs:F1}ms, total {TotalMs:F1}ms",
                    targetUrl, frameBytes.Length, sendElapsed.TotalMilliseconds,
                    drainElapsed.TotalMilliseconds, Stopwatch.GetElapsedTime(frameStart).TotalMilliseconds);
            }
        }
        catch (Exception ex)
        {
            // Send failing means the data channel/peer connection is gone — the session is over
            // anyway, so log and let the loop end rather than crashing anything above it.
            logger.LogWarning(ex, "Frame send loop stopped for {Url}", targetUrl);
        }
    }

    // Prefixes the frame with its byte length, chunks it to the SCTP max message size, and waits for
    // the send buffer to drain — this naturally throttles frame rate to what the client can absorb.
    // Returns (chunking time, drain time) so the caller can log where the time actually went.
    private static async Task<(TimeSpan SendElapsed, TimeSpan DrainElapsed)> SendFrameAsync(RTCPeerConnection pc, RTCDataChannel frameChannel, byte[] frameBytes)
    {
        var framed = new byte[4 + frameBytes.Length];
        BinaryPrimitives.WriteUInt32BigEndian(framed, (uint)frameBytes.Length);
        frameBytes.CopyTo(framed, 4);

        var sendStart = Stopwatch.GetTimestamp();
        DataChannelTransport.SendChunked(pc, frameChannel, framed);
        var sendElapsed = Stopwatch.GetElapsedTime(sendStart);

        var drainElapsed = await DataChannelTransport.WaitForSendBufferDrainAsync(frameChannel);
        return (sendElapsed, drainElapsed);
    }
}
