using System.Diagnostics;
using SIPSorcery.Net;

namespace RemoteBrowserIsolation.Server.Services;

// Shared low-level helpers for sending data over a pre-negotiated SIPSorcery data channel: chunking
// payloads to the SCTP association's negotiated max message size (a single oversized send() throws),
// and draining the send buffer before the caller does anything — like closing the channel or sending
// the next frame — that assumes the previous payload has actually gone out over the wire.
public static class DataChannelTransport
{
    // Splits content into pieces no larger than the channel's negotiated SCTP max message size and
    // sends each piece in order. The receiver reassembles by treating the channel as a byte stream.
    public static void SendChunked(RTCPeerConnection pc, RTCDataChannel dataChannel, byte[] content)
    {
        var maxMessageSize = (int)Math.Min(pc.sctp.maxMessageSize, int.MaxValue);
        if (maxMessageSize <= 0)
        {
            maxMessageSize = content.Length;
        }

        for (var offset = 0; offset < content.Length; offset += maxMessageSize)
        {
            var count = Math.Min(maxMessageSize, content.Length - offset);
            dataChannel.send(content, offset, count);
        }
    }

    // send() only queues data on the SCTP association; polls bufferedAmount down to zero (with a
    // safety timeout) so the caller can be sure the payload was actually transmitted. Returns how
    // long the drain actually took, so callers can log it as part of latency instrumentation.
    public static async Task<TimeSpan> WaitForSendBufferDrainAsync(RTCDataChannel dataChannel, int timeoutMs = 10_000, int pollIntervalMs = 20)
    {
        var start = Stopwatch.GetTimestamp();
        var elapsed = 0;
        while (dataChannel.bufferedAmount > 0 && elapsed < timeoutMs)
        {
            await Task.Delay(pollIntervalMs);
            elapsed += pollIntervalMs;
        }
        return Stopwatch.GetElapsedTime(start);
    }
}
