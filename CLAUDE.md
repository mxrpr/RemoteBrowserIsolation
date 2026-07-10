# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

A remote browser isolation server: given a URL, the server fetches the page over HTTP/HTTPS and streams the raw bytes back to a browser over a WebRTC data channel (not video/screenshot streaming — a raw byte relay). See `plans/1_iteration_framework.md` (requirements) and `plans/1_iteration_plan.md` (design decisions, step-by-step build plan, acceptance criteria) for the full iteration-1 scope.

## Commands

There is no solution file — one project at `src/RemoteBrowserIsolation.Server`.

```bash
# build
cd src/RemoteBrowserIsolation.Server && dotnet build

# run dev server (sets ASPNETCORE_ENVIRONMENT=Development)
./startRBI_dev.sh

# run tests — currently a no-op: no test project exists yet.
# Finds and runs any *Tests.csproj under the repo; prints a message and exits 0 if none found.
./startTests.sh
```

Manual verification (no automated test suite yet):
- Health check: `curl http://localhost:<port>/health`
- Full WebRTC flow: open `http://localhost:<port>/index.html` in a real browser (or headless Chromium via Playwright — this repo has been verified working that way), enter a URL, click Fetch.

## Architecture

Single ASP.NET Core minimal-API project (net9.0), top-level statements in `Program.cs`. Uses SIPSorcery for WebRTC.

**Request flow**: browser is always the WebRTC **offerer**; the server is the **answerer**. This shapes two non-obvious constraints:

1. **Data channel must be pre-negotiated with a fixed id.** Since the server only answers, it cannot introduce a new SDP media section on its own — so both `wwwroot/index.html` (client) and `Services/WebRtcSessionManager.cs` (server) create the data channel with `negotiated: true, id: 0`. If you change one side's id/label semantics, change the other to match.
2. **Signaling is a single HTTP round trip, no trickle ICE, no WebSocket.** The client waits for `iceGatheringState === 'complete'` before POSTing its offer; the server passes `RTCAnswerOptions { X_WaitForIceGatheringToComplete = true }` to `createAnswer` so the returned answer already has full ICE candidates baked in. `POST /api/session/offer` is the only signaling endpoint.

**Services** (`src/RemoteBrowserIsolation.Server/Services/`):
- `PageDownloader` — HttpClient-based fetch. Never throws on network-level failures (timeout, DNS, non-2xx); returns a `PageDownloadResult` record with `Success`/`ErrorMessage` instead. Callers must check `.Success`.
- `WebRtcSessionManager` — one `RTCPeerConnection` per session. Registered as a DI **singleton**, not scoped: the data channel's `onopen` callback (which triggers the actual fetch + `send`) fires asynchronously *after* the HTTP request/response that created the session has completed. A scoped `IPageDownloader` would already be disposed by the time `onopen` fires, so the typed `HttpClient` client is captured once at singleton construction instead.

**Models** (`src/RemoteBrowserIsolation.Server/Models/`): `OfferRequest`/`AnswerResponse` records — JSON keys are camelCase (`url`, `sdp`) via ASP.NET Core's default naming policy.

**Logging**: standard `appsettings.json` `Logging:LogLevel:Default` controls level — no custom config-loading code. A middleware in `Program.cs` logs every incoming request at INFO (method, URL, timestamp). Startup also logs the actual bound address/port.

**`GET /debug/fetch?url=`** is temporary scaffolding to exercise `PageDownloader` directly (not part of the product flow) — expect it to be removed once the real flow is fully trusted.

## Working with the SIPSorcery API

SIPSorcery's naming is inconsistent (`setLocalDescription` vs `SetRemoteDescription` are different overloads/methods) and its public API surface isn't fully covered by IntelliSense-friendly docs. Don't guess signatures from memory. To check exact members of an installed version:

```bash
# XML doc comments (only covers documented members):
grep -n "M:SIPSorcery.Net.RTCPeerConnection\." ~/.nuget/packages/sipsorcery/<version>/lib/net9.0/SIPSorcery.xml

# full reflection dump (covers everything, including undocumented properties/events) —
# build a throwaway console project referencing SIPSorcery and reflect over the type.
```

## MANDATORY Comments in code
Whenever a new class or function/method is created add comment about the function of the class/method/function.
