# Server-side rendering

How iteration 2 actually achieves isolation: the target page is rendered and executed entirely inside a headless Chromium process on the server; the client only ever receives JPEG frames and only ever sends back input events. Raw HTML/JS/CSS never reaches the client (contrast with iteration 1's raw byte relay — see `dev-doc/start-sequence.md` for that older flow, now superseded for the main session path).

## Why

Relaying raw page bytes to the client (iteration 1) means the client's own browser parses and executes the target page — any malicious JS runs on the user's machine. Rendering server-side and streaming only pixels means untrusted JS only ever touches the sandboxed server-side headless browser. See `plans/2_iteration_server_side_rendering.md` for the full design rationale and caveats (this alone doesn't sandbox the headless Chromium *process* itself — that's flagged as separate hardening work).

## Two data channels, not one

`WebRtcSessionManager` now negotiates two pre-negotiated data channels on the same peer connection instead of one:

| id | name | direction | carries |
|----|------|-----------|---------|
| `0` | `frame-stream` | server → client | JPEG frames, length-prefixed |
| `1` | `input-events` | client → server | JSON `InputEvent` messages |

Unlike iteration 1, the session no longer closes after a single send — it stays open for the life of the connection, continuously streaming frames and accepting input.

## Services involved

- **`WebRtcSessionManager`** (`Services/WebRtcSessionManager.cs`) — orchestrator. Builds the peer connection and both data channels, starts the rendering session once the frame channel opens, tears it down on disconnect. Tracks each `RTCPeerConnection` → `HeadlessSession` mapping in a `ConcurrentDictionary` so teardown can find the right browser resources to dispose.
- **`HeadlessBrowserSessionManager`** (`Services/HeadlessBrowserSessionManager.cs`) — owns one shared Playwright `IBrowser` (headless Chromium) for the whole process, launched lazily on first use. Hands out an isolated `IBrowserContext` + `IPage` per session (`CreateSessionAsync`), navigates it to the target URL, and disposes it on teardown (`CloseSessionAsync`). This is where untrusted target-page JS actually executes.
- **`FrameStreamer`** (`Services/FrameStreamer.cs`) — opens a Chrome DevTools Protocol (CDP) session on the page and starts `Page.startScreencast`. Each `Page.screencastFrame` CDP event is base64-decoded to JPEG bytes, framed with a 4-byte length prefix, sent over the frame channel, and acknowledged back to CDP (`Page.screencastFrameAck`) so Chromium keeps producing frames.
- **`InputEventForwarder`** (`Services/InputEventForwarder.cs`) — the input channel's `onmessage` handler. Deserializes each message as an `InputEvent` and replays it on the session's `IPage` via Playwright's `page.Mouse`/`page.Keyboard` APIs.
- **`DataChannelTransport`** (`Services/DataChannelTransport.cs`) — shared low-level helpers extracted from iteration 1: `SendChunked` (splits a payload to the SCTP association's `maxMessageSize`) and `WaitForSendBufferDrainAsync` (polls `bufferedAmount` to zero before the caller moves on). `FrameStreamer` uses both for every frame — draining after each send also throttles frame rate to what the client can actually absorb.
- **`PageDownloader`** — no longer part of the main session flow; retained only for the `/debug/fetch` scaffolding endpoint (see `dev-doc/REST_API_doc.md`).

## Frame wire format

Each frame sent over the `frame-stream` channel is:

```
[4 bytes: big-endian uint32 frame length][frame length bytes: JPEG data]
```

The client can't assume one `onmessage` event equals one frame — `SendChunked` may split a large frame across several data-channel messages, and small frames sent close together could arrive in separate messages that need combining. The client's `createFrameReassembler` (`wwwroot/index.html`) treats the whole channel as one continuous byte stream, appending every incoming chunk to a buffer and repeatedly trying to pull complete `[length][payload]` frames off the front — independent of how the underlying messages were chunked.

## Input event format

JSON sent as UTF-8 text over the `input-events` channel, matching `Models/InputEvent.cs`:

```json
{ "type": "mousemove" | "mousedown" | "mouseup" | "click" | "wheel" | "keydown" | "keyup", "x": 0, "y": 0, "deltaX": 0, "deltaY": 0, "key": "a" }
```

`type` selects which other fields matter (`x`/`y` for mouse position, `deltaX`/`deltaY` for wheel, `key` for keyboard). `wwwroot/index.html`'s `wireInputCapture` attaches listeners to the `<canvas id="screen">` element and sends one JSON message per DOM event.

## Call sequence

### 1. Session creation (`WebRtcSessionManager.CreateSessionAsync`, unchanged trigger — `POST /api/session/offer`)

1. `new RTCPeerConnection()` — server is the answerer, as in iteration 1.
2. `pc.createDataChannel("frame-stream", { negotiated: true, id: 0 })`.
3. `pc.createDataChannel("input-events", { negotiated: true, id: 1 })`.
4. Registers `frameChannel.onopen` → `StartRenderingSessionAsync` (fire-and-forget, runs once the channel actually opens).
5. Registers `pc.onconnectionstatechange` → `OnConnectionStateChanged`.
6. `pc.setRemoteDescription(offer)` → `pc.createAnswer(...)` → `pc.setLocalDescription(answer)` → returns answer SDP, exactly as iteration 1.

### 2. Rendering session start (`WebRtcSessionManager.StartRenderingSessionAsync`, fires on frame channel open)

1. `HeadlessBrowserSessionManager.CreateSessionAsync(targetUrl)`:
   1. Awaits the lazily-initialized shared `(IPlaywright, IBrowser)` — launches headless Chromium on the very first call for the process, reused after that.
   2. `browser.NewContextAsync()` — fresh, isolated context (no shared cookies/storage across sessions).
   3. `context.NewPageAsync()` → `page.GotoAsync(targetUrl)`.
   4. Returns `HeadlessSession(Context, Page)`.
2. Result stored in `activeSessions[pc]` for later teardown lookup.
3. `InputEventForwarder.Wire(inputChannel, session.Page, targetUrl)` — registers `inputChannel.onmessage`.
4. `FrameStreamer.StartAsync(pc, frameChannel, session, targetUrl)`:
   1. `context.NewCDPSessionAsync(page)`.
   2. Registers `cdp.Event("Page.screencastFrame").OnEvent` handler.
   3. `cdp.SendAsync("Page.startScreencast", { format: jpeg, quality: 80, maxWidth: 1280, maxHeight: 720 })`.
5. Logs `Started rendering session for {Url}`.

### 3. Per-frame loop (driven by CDP, not polled)

Repeats for as long as the session is open, triggered by Chromium itself whenever the page repaints:

1. CDP fires `Page.screencastFrame` with base64 JPEG data + a `sessionId`.
2. `FrameStreamer`'s handler decodes the base64, prefixes with a 4-byte big-endian length, calls `DataChannelTransport.SendChunked` then `WaitForSendBufferDrainAsync`.
3. `cdp.SendAsync("Page.screencastFrameAck", { sessionId })` — required or Chromium stops sending further frames.
4. Client's `frameChannel.onmessage` → `createFrameReassembler.append` → once a full `[length][payload]` frame is assembled, `renderFrame` decodes it via `createImageBitmap` and `ctx.drawImage`s it onto the canvas.

### 4. Per-input-event loop (driven by client DOM events)

1. Client canvas DOM event (mousemove/click/wheel/keydown/etc.) → `wireInputCapture`'s listener → `inputChannel.send(JSON.stringify(event))`.
2. Server's `inputChannel.onmessage` → `InputEventForwarder.HandleAsync` → `JsonSerializer.Deserialize<InputEvent>` → dispatches to the matching `page.Mouse`/`page.Keyboard` call.
3. Playwright replays the action on the real page → Chromium repaints → triggers step 3's per-frame loop again, so the client sees the effect of its own input in the next frame(s).

### 5. Teardown (`WebRtcSessionManager.OnConnectionStateChanged`)

1. Fires on `pc.onconnectionstatechange` whenever state becomes `closed`, `failed`, or `disconnected`.
2. `activeSessions.TryRemove(pc, out session)` — idempotent; only the first matching state change does anything.
3. `HeadlessBrowserSessionManager.CloseSessionAsync(session)` → `context.CloseAsync()` — tears down the isolated context/page (and, as a side effect, the CDP session and screencast). The shared `Browser` process stays up for other sessions.
4. Logs `Closed rendering session for {Url}`. Any in-flight frame send racing the teardown throws `TargetClosedException`, caught and logged as a warning (`Dropped a frame for {Url}`) rather than crashing anything — expected, not a bug.

## Verified behavior (manual + Playwright-driven test client)

- Client canvas shows actual rendered pixels of the target page (confirmed by sampling canvas pixel color against the real page's known background color).
- Scrolling on the client canvas visibly changes the rendered content (confirmed via pixel-hash diff before/after a forwarded wheel event).
- Concurrent sessions against different URLs render distinct, non-cross-talking content; one shared Chromium process backs all of them (isolated via separate `BrowserContext`s, not separate browser processes).
- Disconnecting a session logs a clean `Closed rendering session` and releases its `BrowserContext`/`Page`.
