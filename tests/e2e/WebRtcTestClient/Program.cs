using System.Diagnostics;
using System.Net.Http.Json;
using System.Text.Json;
using System.Text.Json.Serialization;
using SIPSorcery.Net;
using SIPSorceryMedia.Abstractions;

// Standalone WebRTC OFFERER test client for the RemoteBrowserIsolation server's video-mode
// signaling endpoint (POST /api/session/offer). The production server is always the WebRTC
// ANSWERER (see WebRtcSessionManager.cs) and can never introduce new SDP media sections on its
// own, so this client mirrors the server's expectations exactly: it offers a recvonly VP8 video
// m-section and a pre-negotiated data channel with the same fixed id (1) the server uses. Used by
// the e2e test suite to prove a session can be fully negotiated end to end, that video RTP packets
// actually arrive, and that input events can be sent over the data channel.
//
// Usage:
//   dotnet run --project WebRtcTestClient.csproj -- \
//     --server-url http://127.0.0.1:15139 \
//     --target-url "http://127.0.0.1:18081/keytest.html?token=allow" \
//     --timeout-seconds 15
//
// Prints a single "RESULT {...}" JSON line to stdout summarizing what was achieved, and exits 0
// only if the connection was fully established, at least one video RTP packet arrived, the data
// channel opened, and the keydown/keyup pair was sent. Any other outcome exits 1.

// Minimum number of video RTP packets to observe before considering the video stream "proven
// flowing" rather than a single stray/malformed packet.
const int VideoPacketTarget = 5;

// Dynamic RTP payload type advertised for VP8 in the offer. The actual value in use is settled
// during SDP negotiation, so this is just the initial hint (matches the server's own constant).
const int Vp8PayloadTypeId = 96;

var options = ParseArgs(args);
var serverUrl = RequireArg(options, "server-url").TrimEnd('/');
var targetUrl = RequireArg(options, "target-url");
var timeoutSeconds = double.Parse(RequireArg(options, "timeout-seconds"));

var deadline = Stopwatch.StartNew();
var deadlineBudget = TimeSpan.FromSeconds(timeoutSeconds);

var connected = false;
var dataChannelOpened = false;
var keydownSent = false;
var videoPacketsReceived = 0;

RTCPeerConnection? pc = null;
try
{
    // Plain no-arg constructor: this is a client-side test peer on localhost, it doesn't need the
    // fixed-UDP-port-range constructor the server uses for Docker port publishing (see
    // WebRtcSessionManager.cs) -- an OS-chosen ephemeral port is fine here.
    pc = new RTCPeerConnection();

    // Advertise a recvonly VP8 video m-section so the offer matches what the server expects to
    // answer with its send-only VP8 track.
    var videoFormat = new VideoFormat(VideoCodecsEnum.VP8, Vp8PayloadTypeId);
    var videoTrack = new MediaStreamTrack(videoFormat, MediaStreamStatusEnum.RecvOnly);
    pc.addTrack(videoTrack);

    // Count every video RTP packet that arrives, regardless of connection/data-channel state, so
    // we have an accurate count by the time the final summary is printed.
    pc.OnRtpPacketReceived += (_, mediaType, _) =>
    {
        if (mediaType == SDPMediaTypesEnum.video)
        {
            Interlocked.Increment(ref videoPacketsReceived);
        }
    };

    // Pre-negotiated data channel with the SAME id (1) the server creates on its side -- required
    // because the server (the answerer) can't add a new SDP section for it.
    var dataChannel = await pc.createDataChannel("input-events", new RTCDataChannelInit { negotiated = true, id = 1 });

    var offer = pc.createOffer(null);
    await pc.setLocalDescription(offer);

    // No trickle ICE support server-side: wait for gathering to fully complete (mirrors
    // wwwroot/index.html's browser client, which waits for iceGatheringState === "complete" before
    // POSTing) so the offer already carries all host candidates.
    while (pc.iceGatheringState != RTCIceGatheringState.complete && deadline.Elapsed < deadlineBudget)
    {
        await Task.Delay(50);
    }

    using var httpClient = new HttpClient();
    var jsonOptions = new JsonSerializerOptions { PropertyNamingPolicy = JsonNamingPolicy.CamelCase };
    var requestBody = new OfferRequestBody(targetUrl, pc.localDescription.sdp.ToString(), 1280, 720);

    var response = await httpClient.PostAsJsonAsync($"{serverUrl}/api/session/offer", requestBody, jsonOptions);
    if (!response.IsSuccessStatusCode)
    {
        var body = await response.Content.ReadAsStringAsync();
        Console.Error.WriteLine($"Offer POST failed with status {(int)response.StatusCode}: {body}");
        PrintResultAndExit(connected, videoPacketsReceived, dataChannelOpened, keydownSent);
        return;
    }

    var answerBody = await response.Content.ReadFromJsonAsync<AnswerResponseBody>(jsonOptions);
    if (answerBody is null || string.IsNullOrEmpty(answerBody.Sdp))
    {
        Console.Error.WriteLine("Answer response missing sdp field.");
        PrintResultAndExit(connected, videoPacketsReceived, dataChannelOpened, keydownSent);
        return;
    }

    var setRemoteResult = pc.setRemoteDescription(new RTCSessionDescriptionInit { type = RTCSdpType.answer, sdp = answerBody.Sdp });
    if (setRemoteResult != SetDescriptionResultEnum.OK)
    {
        Console.Error.WriteLine($"setRemoteDescription failed: {setRemoteResult}");
        PrintResultAndExit(connected, videoPacketsReceived, dataChannelOpened, keydownSent);
        return;
    }

    // Poll connection/data-channel state and video packet count until the deadline. The
    // keydown/keyup pair is RESENT periodically (not just once) while the channel is open: the
    // data channel's own "open" event fires purely from SCTP association state and can race
    // ahead of the server's InputEventForwarder.Wire(), which only attaches its onmessage
    // handler after the headless page finishes navigating (WebRtcSessionManager starts
    // rendering only once the peer connection reaches "connected", then awaits page.GotoAsync
    // before wiring input) -- a message sent before that attach is silently dropped. Resending
    // guarantees eventual delivery once the server-side handler is actually listening.
    TimeSpan? lastKeySendAt = null;
    var keySendInterval = TimeSpan.FromMilliseconds(500);
    TimeSpan? dataChannelOpenedAt = null;
    // Require at least a few resend cycles after the channel opened before allowing an early
    // exit, so a lucky first video-packet burst can't end the test before a resend has had a
    // real chance to land after the server finishes wiring the input handler.
    var minResendWindow = TimeSpan.FromSeconds(3);
    while (deadline.Elapsed < deadlineBudget)
    {
        connected = pc.connectionState == RTCPeerConnectionState.connected;
        dataChannelOpened = dataChannel.readyState == RTCDataChannelState.open;
        if (dataChannelOpened && dataChannelOpenedAt is null)
        {
            dataChannelOpenedAt = deadline.Elapsed;
        }

        if (dataChannelOpened && (lastKeySendAt is null || deadline.Elapsed - lastKeySendAt >= keySendInterval))
        {
            SendKeyEvent(dataChannel, "keydown");
            SendKeyEvent(dataChannel, "keyup");
            keydownSent = true;
            lastKeySendAt = deadline.Elapsed;
        }

        if (connected && keydownSent && videoPacketsReceived >= VideoPacketTarget
            && dataChannelOpenedAt is not null && deadline.Elapsed - dataChannelOpenedAt >= minResendWindow)
        {
            break;
        }

        await Task.Delay(100);
    }

    // Final state snapshot after the loop exits (either success or timeout).
    connected = pc.connectionState == RTCPeerConnectionState.connected;
    dataChannelOpened = dataChannel.readyState == RTCDataChannelState.open;
}
catch (Exception ex)
{
    Console.Error.WriteLine($"Test client failed: {ex}");
}
finally
{
    pc?.close();
}

PrintResultAndExit(connected, videoPacketsReceived, dataChannelOpened, keydownSent);

// Sends one input-event JSON message on the data channel, matching the server's
// Models/InputEvent.cs record shape exactly (camelCase keys, null for unused fields).
static void SendKeyEvent(RTCDataChannel dataChannel, string type)
{
    var json = JsonSerializer.Serialize(new InputEventBody(type, null, null, null, null, "a"));
    dataChannel.send(json);
}

// Prints the final "RESULT {...}" summary line the orchestrating test script parses, then exits
// with 0 only if every success criterion was met, 1 otherwise.
static void PrintResultAndExit(bool connected, int videoPacketsReceived, bool dataChannelOpened, bool keydownSent)
{
    var result = new ResultSummary(connected, videoPacketsReceived, dataChannelOpened, keydownSent);
    var json = JsonSerializer.Serialize(result, new JsonSerializerOptions { PropertyNamingPolicy = JsonNamingPolicy.CamelCase });
    Console.WriteLine($"RESULT {json}");

    var success = connected && videoPacketsReceived >= 1 && dataChannelOpened && keydownSent;
    Environment.Exit(success ? 0 : 1);
}

// Parses "--key value" pairs from argv into a lookup dictionary. Minimal manual parsing -- no CLI
// library needed for this throwaway harness's small, fixed set of flags.
static Dictionary<string, string> ParseArgs(string[] args)
{
    var result = new Dictionary<string, string>();
    for (var i = 0; i < args.Length - 1; i++)
    {
        if (args[i].StartsWith("--", StringComparison.Ordinal))
        {
            result[args[i][2..]] = args[i + 1];
        }
    }

    return result;
}

// Looks up a required CLI flag, throwing with a clear message if it's missing.
static string RequireArg(Dictionary<string, string> options, string name)
{
    if (!options.TryGetValue(name, out var value))
    {
        throw new ArgumentException($"Missing required argument --{name}");
    }

    return value;
}

// POST body for /api/session/offer -- mirrors the server's Models/OfferRequest.cs shape
// (camelCase: url, sdp, width, height).
internal sealed record OfferRequestBody(string Url, string Sdp, int Width, int Height);

// Response body from /api/session/offer -- mirrors the server's Models/AnswerResponse.cs shape
// (camelCase: sdp).
internal sealed record AnswerResponseBody([property: JsonPropertyName("sdp")] string Sdp);

// One input-event message sent over the data channel -- mirrors the server's
// Models/InputEvent.cs record shape exactly (camelCase: type, x, y, deltaX, deltaY, key).
internal sealed record InputEventBody(string Type, float? X, float? Y, float? DeltaX, float? DeltaY, string? Key);

// Final machine-parseable summary printed as "RESULT {...}" for the orchestrating test script.
internal sealed record ResultSummary(bool Connected, int VideoPacketsReceived, bool DataChannelOpened, bool KeydownSent);
