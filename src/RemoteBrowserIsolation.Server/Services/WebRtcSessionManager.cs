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

        var dataChannel = await pc.createDataChannel("page-content");
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
                dataChannel.send(result.Content!, 0, result.Content!.Length);
                logger.LogInformation("Sent {ByteLength} bytes for {Url} over data channel", result.Content.Length, targetUrl);
            }
            else
            {
                logger.LogWarning("Fetch failed for {Url}: {Error}", targetUrl, result.ErrorMessage);
            }
        }
        finally
        {
            dataChannel.close();
            pc.close();
        }
    }
}
