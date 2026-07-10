# Iteration 1 Plan

Based on `1_iteration_framework.md`.

## Goal
A C# server application that:
- accepts many concurrent browser connections,
- given a URL, downloads the page over HTTP/HTTPS,
- sends the page content back to the requesting browser over WebRTC,
- logs every request at INFO level,
- reads log level from a configuration file.

## Design decisions

- **Signaling**: plain HTTP endpoint. Browser POSTs an SDP offer, server waits for ICE gathering to complete, then returns the SDP answer in the response body. Avoids building a WebSocket signaling channel for iteration 1.
- **Transport**: WebRTC DataChannel. SCTP handles message fragmentation internally, so the full page body can be sent as a single `send()` call — no custom chunking protocol needed for v1.
- **Session model**: one `RTCPeerConnection` per fetch request. Kestrel already handles concurrent HTTP requests, so no extra concurrency plumbing is needed beyond keeping each peer connection isolated.
- **Configuration**: use the standard ASP.NET Core `appsettings.json` `Logging:LogLevel` section. This satisfies "log level configurable via config file" without custom config code.
- **Logging**: a request-logging middleware/handler logs the requested URL and timestamp at INFO for every fetch request.
- **Scope excluded from v1**: no auth, no TLS certificate management beyond Kestrel defaults, no persistent storage, no page rendering/screenshot — just a raw byte relay of the downloaded page.

## Proposed project layout

```
src/RemoteBrowserIsolation.Server/
  Program.cs                     # minimal hosting, DI, logging config
  Endpoints/SessionEndpoints.cs  # POST /api/session (offer -> answer)
  Services/PeerConnectionFactory.cs  # builds RTCPeerConnection + data channel from an offer
  Services/PageDownloader.cs     # HttpClient wrapper, GET over http/https
  Models/OfferRequest.cs
  Models/AnswerResponse.cs
  appsettings.json
test-client/
  index.html                     # manual test page: enter URL, do offer/answer, show received content
```

## Steps

1. Scaffold ASP.NET Core minimal-API project, add SIPSorcery NuGet package. Confirm it builds and runs with a health-check endpoint.
2. Wire configuration + logging: log level read from `appsettings.json`, INFO log emitted per incoming fetch request (URL + timestamp).
3. Implement `PageDownloader` service: HttpClient GET, supports http/https, handles fetch errors (timeout, DNS failure, non-2xx).
4. Implement the signaling endpoint: accept SDP offer, create `RTCPeerConnection` + data channel, wait for ICE gathering to complete, return SDP answer.
5. Wire the data channel `open` event to: trigger the page download, send the bytes once ready, then close the channel/connection.
6. Build a minimal test-client (HTML + JS): user enters a URL, client performs the offer/answer exchange, opens the data channel, and displays the received content. This is the manual end-to-end verification tool for this iteration.
7. Manual end-to-end test: run the server, use the test client to fetch a real page, confirm received bytes match the original content.
8. Concurrency check: open multiple simultaneous sessions (e.g. several browser tabs) against the server, confirm each is handled independently and logged correctly.

## Acceptance criteria

- Server starts and listens on a configured port.
- Multiple concurrent browser sessions are handled independently, without cross-talk.
- Every fetch request is logged at INFO with at least the requested URL.
- Log level can be changed via the config file without recompiling.
- Test client can request a URL and receive the full page content back over a WebRTC data channel, byte-identical to the original.
