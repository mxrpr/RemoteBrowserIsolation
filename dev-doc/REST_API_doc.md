# REST API

All endpoints defined in `src/RemoteBrowserIsolation.Server/Program.cs`. None of them carry page content — that only ever flows over the WebRTC data channel (see `dev-doc/start-sequence.md`). REST here exists purely for signaling and diagnostics.

---

## `GET /health`

**Role**: liveness check.

```
Results.Ok(new { status = "ok" })
```

**Why**: cheap way to confirm the server process is up and answering HTTP before bothering with WebRTC at all. Used for manual smoke-testing (`curl http://localhost:5139/health`); not consumed by the browser client.

---

## `POST /api/session/offer`

**Role**: the WebRTC signaling exchange — the only endpoint the browser client actually calls.

**Request body** (`OfferRequest`):
```json
{ "url": "https://example.com", "sdp": "<client's offer SDP>" }
```

**Response body** (`AnswerResponse`):
```json
{ "sdp": "<server's answer SDP>" }
```

**Why we need it**: WebRTC peers can't discover each other or negotiate a connection on their own — they need an out-of-band ("signaling") channel to exchange SDP offer/answer before ICE/DTLS/SCTP can establish. Browser is always the offerer, server is always the answerer (see `CLAUDE.md`), so a single POST-in/answer-out endpoint is sufficient — no WebSocket or trickle ICE needed, since both sides wait for full ICE gathering before exchanging SDP (`X_WaitForIceGatheringToComplete` server-side, `waitForIceGatheringState === 'complete'` client-side).

**What it does** (delegates to `WebRtcSessionManager.CreateSessionAsync`):
1. Validates `url` is an absolute URI — `400 Bad Request` with `{ error: "Invalid URL" }` if not.
2. Creates a server-side `RTCPeerConnection`, pre-negotiated data channel (fixed `id: 0`), sets remote description from the client's offer, creates + sets local answer description.
3. Registers the channel's `onopen` handler to later fetch `url` and stream it back over the data channel once the connection is live — but that happens *after* this HTTP call returns, not during it.
4. Returns the answer SDP so the client can call `setRemoteDescription` and complete the handshake.

**Failure mode**: `400 Bad Request` with `{ error: <message> }` if `setRemoteDescription` fails (malformed/incompatible offer SDP).

Once this call returns, REST's job is done for that session — everything else (fetch + byte streaming) happens over the data channel.

---

## `GET /debug/fetch?url=`

**Role**: scaffolding to exercise `PageDownloader` directly, bypassing WebRTC entirely.

**Response body**:
```json
{ "success": true, "contentType": "text/html; charset=UTF-8", "byteLength": 630298 }
```
or
```json
{ "success": false, "error": "<message>" }
```

**Why**: lets you check whether a fetch failure is in the HTTP-download layer (`PageDownloader`) or the WebRTC layer (`WebRtcSessionManager`/data channel) without spinning up a browser client. Used during debugging (e.g. confirming index.hu's 630KB page downloads fine before tracking a data-channel truncation bug to `WebRtcSessionManager`).

**Not part of the product flow** — per `CLAUDE.md`, expect this endpoint to be removed once the real WebRTC flow is fully trusted. Don't build features that depend on it.

---

## Static files (`UseDefaultFiles` / `UseStaticFiles`)

Not a REST API endpoint per se, but the only other thing served over plain HTTP: `wwwroot/index.html` (the test client) and any other static assets. `GET /` or `GET /index.html` serves the page that drives everything above.
