using SIPSorcery.Net;

namespace RemoteBrowserIsolation.Server.Services;

public interface IWebRtcSessionManager
{
    Task<string> CreateSessionAsync(string offerSdp, Uri targetUrl, CancellationToken cancellationToken = default);
}

public sealed class WebRtcSessionManager(IPageDownloader downloader, ILogger<WebRtcSessionManager> logger) : IWebRtcSessionManager
{
    public async Task<string> CreateSessionAsync(string offerSdp, Uri targetUrl, CancellationToken cancellationToken = default)
    {
        var pc = new RTCPeerConnection();

        // negotiated + fixed id: the client must create its channel with the same id (0) out-of-band,
        // since the server is the answerer and can't add a new data-channel section via SDP alone.
        var dataChannel = await pc.createDataChannel("page-content", new RTCDataChannelInit { negotiated = true, id = 0 });
        dataChannel.onopen += () => _ = SendPageAsync(pc, dataChannel, targetUrl);
        dataChannel.onerror += error => logger.LogWarning("Data channel error for {Url}: {Error}", targetUrl, error);

        var setRemoteResult = pc.setRemoteDescription(new RTCSessionDescriptionInit { type = RTCSdpType.offer, sdp = offerSdp });
        if (setRemoteResult != SetDescriptionResultEnum.OK)
        {
            pc.close();
            throw new InvalidOperationException($"Failed to set remote description: {setRemoteResult}");
        }

        var answer = pc.createAnswer(new RTCAnswerOptions { X_WaitForIceGatheringToComplete = true });
        await pc.setLocalDescription(answer);

        return pc.localDescription.sdp.ToString();
    }

    private async Task SendPageAsync(RTCPeerConnection pc, RTCDataChannel dataChannel, Uri targetUrl)
    {
        try
        {
            var result = await downloader.DownloadAsync(targetUrl);
            if (result.Success)
            {
                SendChunked(pc, dataChannel, result.Content!);
                await WaitForSendBufferDrainAsync(dataChannel);
                logger.LogInformation("Sent {ByteLength} bytes for {Url} over data channel", result.Content!.Length, targetUrl);
            }
            else
            {
                logger.LogWarning("Fetch failed for {Url}: {Error}", targetUrl, result.ErrorMessage);
            }
        }
        catch (Exception ex)
        {
            logger.LogError(ex, "Failed to send page content for {Url}", targetUrl);
        }
        finally
        {
            dataChannel.close();
            pc.close();
        }
    }

    // RTCDataChannel.send() does not chunk internally — a single call exceeding the negotiated
    // SCTP maxMessageSize throws, so pages larger than that limit must be split into multiple sends.
    private static void SendChunked(RTCPeerConnection pc, RTCDataChannel dataChannel, byte[] content)
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

    // send() only queues data on the SCTP association; closing the channel right after send()
    // returns can tear it down before a multi-chunk payload has actually gone out over the wire.
    // Poll bufferedAmount down to zero (with a safety timeout) before closing.
    private static async Task WaitForSendBufferDrainAsync(RTCDataChannel dataChannel, int timeoutMs = 10_000, int pollIntervalMs = 20)
    {
        var elapsed = 0;
        while (dataChannel.bufferedAmount > 0 && elapsed < timeoutMs)
        {
            await Task.Delay(pollIntervalMs);
            elapsed += pollIntervalMs;
        }
    }
}
