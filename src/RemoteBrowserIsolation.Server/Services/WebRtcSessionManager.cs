using System.Collections.Concurrent;
using SIPSorcery.Net;
using SIPSorceryMedia.Abstractions;

namespace RemoteBrowserIsolation.Server.Services;

public interface IWebRtcSessionManager
{
    Task<string> CreateSessionAsync(string offerSdp, Uri targetUrl, int? viewportWidth = null, int? viewportHeight = null, CancellationToken cancellationToken = default);
}

// Orchestrates one WebRTC session end to end: negotiates the peer connection with a send-only VP8
// video track (rendered page pixels out) and a pre-negotiated data channel (input events in), and —
// once the connection is established — starts a server-side rendering session, wiring video
// streaming and input forwarding to it. Tears the rendering session down once the peer connection
// disconnects, so headless-browser resources don't leak across sessions.
public sealed class WebRtcSessionManager(
    IHeadlessBrowserSessionManager browserSessionManager,
    IVideoTrackStreamer videoTrackStreamer,
    IInputEventForwarder inputEventForwarder,
    ILogger<WebRtcSessionManager> logger) : IWebRtcSessionManager
{
    // Bounds for the client-requested viewport. The lower bound guards against degenerate/abusive
    // values; the upper bound caps the VP8 encoder's per-frame CPU cost (~15-20ms at 720p —
    // bandwidth is no longer the constraint now that frames travel over RTP instead of the
    // throughput-limited SCTP data channel).
    private const int MinViewportWidth = 320;
    private const int MinViewportHeight = 180;
    private const int MaxViewportWidth = 1280;
    private const int MaxViewportHeight = 720;
    private const int DefaultViewportWidth = 1280;
    private const int DefaultViewportHeight = 720;

    // Dynamic RTP payload type for the offered VP8 format; anything in the dynamic range works,
    // the actual value is settled during SDP negotiation.
    private const int Vp8PayloadTypeId = 96;

    // Peer connections don't carry session state themselves, so track each one's rendering session
    // here to look it up again on teardown.
    private readonly ConcurrentDictionary<RTCPeerConnection, HeadlessSession> activeSessions = new();

    public async Task<string> CreateSessionAsync(string offerSdp, Uri targetUrl, int? viewportWidth = null, int? viewportHeight = null, CancellationToken cancellationToken = default)
    {
        var width = Math.Clamp(viewportWidth ?? DefaultViewportWidth, MinViewportWidth, MaxViewportWidth);
        var height = Math.Clamp(viewportHeight ?? DefaultViewportHeight, MinViewportHeight, MaxViewportHeight);

        var pc = new RTCPeerConnection();

        // The video track must be added before answering so the SDP answer accepts the client
        // offer's recvonly video section with VP8.
        var videoTrack = new MediaStreamTrack(new VideoFormat(VideoCodecsEnum.VP8, Vp8PayloadTypeId), MediaStreamStatusEnum.SendOnly);
        pc.addTrack(videoTrack);

        // negotiated + fixed id: the client must create the channel with the same id out-of-band,
        // since the server is the answerer and can't add new data-channel sections via SDP alone.
        var inputChannel = await pc.createDataChannel("input-events", new RTCDataChannelInit { negotiated = true, id = 1 });

        // Start rendering once the connection (DTLS/SRTP) is actually up — video can't flow any
        // earlier, and the input channel opens over the same established transport. Ensures the
        // heavyweight work (Chromium context, FFmpeg encoder) only spins up for connections that
        // actually complete.
        var started = false;
        pc.onconnectionstatechange += state =>
        {
            if (state == RTCPeerConnectionState.connected && !started)
            {
                started = true;
                _ = StartRenderingSessionAsync(pc, inputChannel, targetUrl, width, height);
            }

            OnConnectionStateChanged(pc, state, targetUrl);
        };

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

    // Fired once the peer connection is established: launches the headless-browser session, wires
    // input replay to it, and starts the video stream. Runs fire-and-forget from the state-change
    // callback, after the HTTP request that created the session has already completed.
    private async Task StartRenderingSessionAsync(RTCPeerConnection pc, RTCDataChannel inputChannel, Uri targetUrl, int viewportWidth, int viewportHeight)
    {
        try
        {
            var session = await browserSessionManager.CreateSessionAsync(targetUrl, viewportWidth, viewportHeight);
            activeSessions[pc] = session;

            // The connection can die during the (multi-second) browser-context startup above; if
            // teardown already ran, close what we just created instead of leaking it.
            if (pc.connectionState is RTCPeerConnectionState.closed or RTCPeerConnectionState.failed or RTCPeerConnectionState.disconnected)
            {
                OnConnectionStateChanged(pc, pc.connectionState, targetUrl);
                return;
            }

            inputEventForwarder.Wire(inputChannel, session.Page, targetUrl);
            await videoTrackStreamer.StartAsync(pc, session, targetUrl);

            logger.LogInformation("Started rendering session for {Url} at {Width}x{Height}", targetUrl, viewportWidth, viewportHeight);
        }
        catch (Exception ex)
        {
            logger.LogError(ex, "Failed to start rendering session for {Url}", targetUrl);
            pc.close();
        }
    }

    // Closes the headless-browser session once the peer connection is no longer usable (closed,
    // failed, or disconnected), so BrowserContext/Page resources don't leak past client disconnect.
    private void OnConnectionStateChanged(RTCPeerConnection pc, RTCPeerConnectionState state, Uri targetUrl)
    {
        if (state is not (RTCPeerConnectionState.closed or RTCPeerConnectionState.failed or RTCPeerConnectionState.disconnected))
        {
            return;
        }

        if (activeSessions.TryRemove(pc, out var session))
        {
            _ = TeardownAsync(session, targetUrl);
        }
    }

    // Disposes one session's headless-browser resources; swallows/logs failures since teardown
    // happens after the client is already gone and there's no one left to report an error to.
    private async Task TeardownAsync(HeadlessSession session, Uri targetUrl)
    {
        try
        {
            await browserSessionManager.CloseSessionAsync(session);
            logger.LogInformation("Closed rendering session for {Url}", targetUrl);
        }
        catch (Exception ex)
        {
            logger.LogWarning(ex, "Error while closing rendering session for {Url}", targetUrl);
        }
    }
}
