# Start sequence

Steps/methods that run after `./startRBI_dev.sh` starts the server, through one full fetch cycle.

## 1. Server boot (`Program.cs`)

1. `WebApplication.CreateBuilder(args)` — build host.
2. `builder.Services.AddHttpClient<IPageDownloader, PageDownloader>(...)` — registers typed `HttpClient` (30s timeout) for `PageDownloader`.
3. `builder.Services.AddSingleton<IWebRtcSessionManager, WebRtcSessionManager>()` — singleton, not scoped (see `CLAUDE.md` for why).
4. `builder.Build()`.
5. Request-logging middleware registered (`app.Use(...)`) — logs method/URL/timestamp for every request.
6. `app.UseDefaultFiles()` / `app.UseStaticFiles()` — serves `wwwroot/` (including `index.html`).
7. Route handlers mapped: `GET /health`, `POST /api/session/offer`, `GET /debug/fetch`.
8. `app.Lifetime.ApplicationStarted.Register(...)` — logs the bound address(es) once Kestrel is listening.
9. `app.Run()` — blocks, serving requests.

## 2. Browser loads the client

1. Browser requests `GET /index.html` (served by `UseStaticFiles`).
2. User enters a URL, clicks **Fetch** → `fetchBtn.onclick` handler runs.

## 3. Client builds and sends the offer (`wwwroot/index.html`)

1. `new RTCPeerConnection()` — client is always the WebRTC **offerer**.
2. `pc.createDataChannel('page-content', { negotiated: true, id: 0 })` — pre-negotiated channel, fixed id `0` (must match server-side id).
3. `pc.createOffer()` → `pc.setLocalDescription(offer)`.
4. `waitForIceGatheringComplete(pc)` — blocks until `iceGatheringState === 'complete'` (no trickle ICE).
5. `fetch('/api/session/offer', { method: 'POST', body: { url, sdp } })` — the single signaling round trip.

## 4. Server handles the offer (`Program.cs` → `WebRtcSessionManager.cs`)

1. `POST /api/session/offer` handler: validates `url` via `Uri.TryCreate`.
2. `WebRtcSessionManager.CreateSessionAsync(offerSdp, targetUrl)`:
   1. `new RTCPeerConnection()` — server is the **answerer**.
   2. `pc.createDataChannel('page-content', { negotiated: true, id: 0 })` — same fixed id as the client.
   3. Registers `dataChannel.onopen` → fires `SendPageAsync(pc, dataChannel, targetUrl)` (fire-and-forget) once the channel opens.
   4. Registers `dataChannel.onerror` → logs a warning.
   5. `pc.setRemoteDescription(offer)` — consumes the client's offer.
   6. `pc.createAnswer(new RTCAnswerOptions { X_WaitForIceGatheringToComplete = true })` — answer isn't returned until ICE gathering completes, so it already carries full candidates.
   7. `pc.setLocalDescription(answer)`.
   8. Returns `pc.localDescription.sdp` as the answer SDP.
3. Handler returns `200 OK` with `{ sdp: answerSdp }`.

## 5. Client completes the handshake

1. `pc.setRemoteDescription({ type: 'answer', sdp: answerSdp })`.
2. ICE/DTLS/SCTP establishes; data channel transitions to open on both sides.

## 6. Data flows over the channel (`WebRtcSessionManager.SendPageAsync`)

Triggered by the server's `dataChannel.onopen`:

1. `downloader.DownloadAsync(targetUrl)` (`PageDownloader.cs`) — `HttpClient.GetAsync` with `ResponseHeadersRead`; never throws on network failure, returns `PageDownloadResult { Success, Content, ErrorMessage }`.
2. On success: `SendChunked(pc, dataChannel, content)` — splits `content` into pieces no larger than `pc.sctp.maxMessageSize` and calls `dataChannel.send(...)` per chunk (a single oversized `send()` throws).
3. `WaitForSendBufferDrainAsync(dataChannel)` — polls `dataChannel.bufferedAmount` down to `0` (10s timeout) so `close()` doesn't cut off data still queued for transmission.
4. On failure: logs a warning with `result.ErrorMessage`, sends nothing.
5. Any exception during send is caught and logged (`logger.LogError`).
6. `finally`: `dataChannel.close()` then `pc.close()`.

## 7. Client receives data (`wwwroot/index.html`)

1. `channel.onmessage` fires per chunk — appends to `chunks[]`, accumulates `receivedBytes`, logs progress.
2. `channel.onclose` fires once the server closes the channel — logs total bytes received, builds a `Blob` from `chunks`, renders it as text into the page.
