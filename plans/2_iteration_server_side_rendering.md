# Iteration 2 Plan: Server-Side Rendering

Based on `1_iteration_framework.md` / `1_iteration_plan.md`, and the gap identified after testing iteration 1: relaying raw HTML/JS/CSS bytes to the client provides **zero isolation** — any script in the target page would execute in the client's own browser if rendered there. Real remote browser isolation requires the target page to be rendered and executed entirely on the server; the client must only ever receive pixels.

## Goal

Extend the server so that:
- the target URL is rendered by a real, JS-executing browser **on the server**, not just downloaded as bytes,
- the client receives a continuous stream of rendered frames (images) over WebRTC — never raw HTML/JS/CSS,
- the client can interact with the remote page (mouse, keyboard, scroll) by forwarding input events back to the server, which replays them on the server-side page,
- each session's server-side browser context is isolated from other sessions and torn down cleanly on disconnect.

## Why iteration 1's approach doesn't satisfy this

`PageDownloader` fetches raw bytes and `WebRtcSessionManager` relays them unmodified. That's correct for "iteration 1: raw byte relay" as scoped in `1_iteration_framework.md`, but it means the client is the one parsing/executing the target page — the opposite of isolation. Iteration 2 replaces this pipeline; `PageDownloader` and the single-shot "download → send → close" session lifecycle from iteration 1 are superseded, not extended.

## Design decisions

- **Rendering engine**: headless Chromium via [Microsoft.Playwright](https://playwright.dev/dotnet/) (.NET NuGet package). Playwright gives per-session isolated `BrowserContext`s from one shared `Browser` process, plus access to the underlying Chrome DevTools Protocol (CDP) session when needed.
- **Frame capture**: use CDP's `Page.startScreencast` (accessed via Playwright's raw CDP session) rather than polling `page.screenshot()` in a loop. Screencast pushes frames as the page actually changes instead of burning CPU on a fixed-interval poll, and lets frame rate self-throttle to page activity. Each received frame must be acknowledged (`Page.screencastFrameAck`) to keep frames flowing.
- **Frame transport — reuse the existing data channel, don't add a WebRTC video track (for now)**: iteration 1 already solved chunked send + buffered-amount draining over the pre-negotiated data channel (`WebRtcSessionManager.cs`). Sending JPEG stills as discrete messages over that same channel reuses proven plumbing and avoids the unknown of SIPSorcery's video-track/codec support (see `CLAUDE.md`'s warning about undocumented API surface). Because the channel now stays open for a long-lived streaming session instead of closing after one relay, each message needs explicit framing so the client can tell where one frame ends and the next begins — e.g. a fixed 4-byte big-endian length prefix followed by the JPEG bytes, chunked internally the same way large payloads already are.
- **Input channel**: a second pre-negotiated data channel (fixed id `1`, alongside the existing frame channel at id `0`) carries client → server input events as small JSON messages (`{ type: "mousemove"|"click"|"keydown"|"scroll"|..., ... }`). Server decodes and replays them via Playwright's `page.Mouse` / `page.Keyboard` APIs.
- **Session lifecycle**: no longer "fetch once, send once, close." A session now spans a `RTCPeerConnection` + a Playwright `BrowserContext`/`Page` that stay alive until the client disconnects (peer connection closes) or an idle/max-duration timeout fires. Teardown must close the Playwright page/context to free the headless browser resources — leaking these under concurrent sessions is the main new failure mode this iteration introduces.
- **Video track as a future optimization, not v2 scope**: JPEG-stills-over-datachannel is bandwidth-inefficient compared to real H.264/VP8 video. If frame rate/latency turns out to be unacceptable, revisit using an actual WebRTC video track — but that's a separate, riskier change (codec negotiation, SIPSorcery media API) and shouldn't block getting *some* pixel-only isolation working first.

## Important caveat — this iteration alone is not a complete security boundary

Rendering server-side and streaming only pixels stops malicious JS from ever reaching the client's browser — that's the core win. But the headless Chromium process itself now executes untrusted page content, so the security guarantee also depends on how well *that process* is sandboxed on the server (OS user isolation, containerization, Chromium sandbox flags, resource/network limits). This iteration does not include that hardening (see Non-goals) — don't represent it as a complete security solution until that follow-up lands.

## Proposed project layout

```
src/RemoteBrowserIsolation.Server/
  Services/
    HeadlessBrowserSessionManager.cs   # owns the shared Playwright Browser + per-session BrowserContext/Page lifecycle
    FrameStreamer.cs                   # starts CDP screencast, frames the JPEG bytes, sends over data channel id:0, acks each frame
    InputEventForwarder.cs             # decodes JSON input messages from data channel id:1, replays via Playwright page APIs
    WebRtcSessionManager.cs            # updated: two negotiated data channels (frames out, input in); session no longer closes after first send
    PageDownloader.cs                  # retained only for the /debug/fetch scaffolding endpoint, no longer in the main session flow
  Models/
    InputEvent.cs                      # mouse/keyboard/scroll event DTO for the input channel
appsettings.json                       # add config: max concurrent sessions, session idle timeout, target frame rate cap
```

## Steps

1. Add `Microsoft.Playwright` NuGet package; run `playwright install chromium` as part of dev setup. Write a throwaway check that headless Chromium launches and can screenshot a page, to confirm the environment (browser binaries, sandbox flags) works before wiring anything else.
2. Update `WebRtcSessionManager`: create a second pre-negotiated data channel (id `1`) for input, alongside the existing id `0`. Stop closing the peer connection/data channels after the first send — the session now stays open until disconnect.
3. Implement `HeadlessBrowserSessionManager`: one shared Playwright `Browser` instance (singleton, like today's `WebRtcSessionManager`); on session creation, open a new isolated `BrowserContext` + `Page`, navigate to the requested URL.
4. Implement `FrameStreamer`: open a CDP session on the page, call `Page.startScreencast`, on each `screencastFrame` event send `[4-byte length][JPEG bytes]` over data channel id `0` (reusing the existing `SendChunked`/`WaitForSendBufferDrainAsync` logic from iteration 1), then ack the frame via CDP.
5. Implement `InputEventForwarder`: data channel id `1`'s `onmessage` → deserialize `InputEvent` → replay through `page.Mouse`/`page.Keyboard`/wheel APIs.
6. Wire session teardown: on peer-connection close (or an idle timeout), dispose the `Page`/`BrowserContext` and stop the screencast, so headless resources don't leak across sessions.
7. Update `wwwroot/index.html`: replace the raw-text log/content panels with a `<canvas>` that decodes and draws each incoming frame (`length`-prefixed JPEG → `Blob` → `createImageBitmap` → `drawImage`); capture mouse/keyboard/scroll events on the canvas and send them as JSON over the input channel.
8. Manual end-to-end test: load a real page with active JS/video/images, confirm the client renders a live bitmap (not text), and confirm a click/scroll/keypress on the client canvas visibly affects the server-rendered page in the next frame.
9. Concurrency check: open multiple simultaneous sessions against different URLs, confirm each gets an isolated `BrowserContext`, frames don't cross sessions, and memory/process count stays bounded (add a max-concurrent-sessions config cap if needed).

## Acceptance criteria

- Client never receives raw HTML/JS/CSS bytes for the target page — only image frames.
- A page with active JavaScript, images, and video renders visibly in the client as a live bitmap stream, not as text.
- Mouse, keyboard, and scroll input performed in the client is reflected on the server-rendered page within the next few frames.
- Multiple concurrent sessions are isolated from each other (separate `BrowserContext`s, no frame/input cross-talk), consistent with iteration 1's concurrency requirement.
- Disconnecting a session (peer connection close) cleanly disposes its headless browser resources — no leaked `Page`/`BrowserContext` instances.
- Every session logs at INFO level (session start URL, session end + duration), consistent with iteration 1's logging requirement.

## Non-goals (this iteration)

- Real WebRTC video track / H.264 / VP8 encoding — JPEG-stills-over-datachannel is the v2 transport; revisit only if bandwidth/latency prove unacceptable.
- OS-level sandboxing/containerization of the headless Chromium process (Chromium sandbox flags, restricted OS user, container/network egress limits). Necessary before this system can be trusted as a complete security boundary, but scoped as separate hardening work.
- Audio streaming.
- File upload/download passthrough between client and target page.
- Clipboard sync between client and remote page.
