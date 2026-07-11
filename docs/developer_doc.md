# Remote Browser Isolation — Developer Documentation

This document is a deep-dive for developers new to this codebase. It assumes no prior WebRTC
or forward-proxy knowledge and explains both *what* the app does and *why* it's built this way.
For a quick product overview, see `README.md`; for terse "how do I build/run this" instructions,
see `CLAUDE.md`. This document is the one to read end-to-end before making your first change.

## Table of contents

1. [What problem this solves](#1-what-problem-this-solves)
2. [High-level architecture](#2-high-level-architecture)
3. [The policy engine](#3-the-policy-engine)
4. [HTML mode: the TLS-intercepting forward proxy](#4-html-mode-the-tls-intercepting-forward-proxy)
5. [Video mode: the WebRTC pipeline](#5-video-mode-the-webrtc-pipeline)
6. [WebRTC concepts explained](#6-webrtc-concepts-explained)
7. [SIPSorcery: the WebRTC library](#7-sipsorcery-the-webrtc-library)
8. [Third-party dependencies](#8-third-party-dependencies)
9. [Data model & admin console](#9-data-model--admin-console)
10. [Configuration reference](#10-configuration-reference)
11. [Docker deployment](#11-docker-deployment)
12. [Project layout reference](#12-project-layout-reference)
13. [Debugging playbook](#13-debugging-playbook)

---

## 1. What problem this solves

"Remote browser isolation" (RBI) is a security pattern: instead of letting a user's real browser
load an untrusted or unknown website directly (and thus run that site's JavaScript, parse its
HTML/CSS, and be exposed to any browser 0-day it might exploit), you interpose a server that does
the risky work on the user's behalf, and hand the user back something safe — either the real,
lightly-sanitized content, or a lossy visual representation with no executable code at all.

This app implements RBI with **two different strength levels**, selected per-hostname by an
admin-managed policy:

- **HTML mode** — a normal forward proxy that intercepts TLS, optionally does small sanitizing
  edits to the HTML, and hands the real page straight to the user's own browser. Fast, full
  fidelity, but the site's JS still executes in the user's browser — this is "relay with a policy
  checkpoint," not real isolation.
- **Video mode** — the page is rendered server-side in a disposable headless Chromium instance
  (via Playwright), and the user only ever receives an encoded video stream (VP8 over WebRTC) of
  the rendered pixels, plus a channel to send mouse/keyboard back. **No page code — HTML, CSS, or
  JS — ever reaches the user's machine.** This is the "actually isolate it" path.

The admin decides, per hostname, which mode applies (or blocks the host outright). See
[§3](#3-the-policy-engine).

## 2. High-level architecture

```
                      ┌─────────────────────────────────────────────┐
                      │              Browser (the user)              │
                      │  proxy setting → this server's Proxy:Port    │
                      └───────────────┬───────────────────┬─────────┘
                                       │                   │
                         HTTP/HTTPS via forward proxy      │ WebRTC (only for
                         (every site's traffic transits    │ video-mode sites,
                         this once configured)              │ via /index.html)
                                       │                   │
                      ┌────────────────▼──────┐  ┌─────────▼──────────────┐
                      │ TlsInterceptingProxy   │  │  Kestrel (ASP.NET Core)│
                      │ Server (hand-rolled    │  │  - REST APIs           │
                      │ TcpListener, its own   │  │  - static wwwroot/     │
                      │ IHostedService)        │  │  - WebRTC signaling    │
                      └───────────┬────────────┘  └──────────┬────────────┘
                                  │                            │
                    resolves policy via                resolves policy via
                    IPolicyEngine (deny-by-default)     IPolicyEngine
                                  │                            │
                     ┌────────────┴───────────┐    ┌───────────┴────────────┐
                     │  HTML mode:             │    │  Video mode:           │
                     │  OriginForwarder fetches│    │  WebRtcSessionManager  │
                     │  the real site, relays  │    │  spins up a headless   │
                     │  bytes back (optionally │    │  Chromium page via     │
                     │  through                │    │  HeadlessBrowserSession│
                     │  HtmlNoInputInjector)    │    │  Manager, streams it   │
                     │                          │    │  as VP8 (VideoTrack    │
                     │                          │    │  Streamer), forwards   │
                     │                          │    │  input (InputEvent     │
                     │                          │    │  Forwarder)            │
                     └──────────────────────────┘    └────────────────────────┘
```

Both paths share one SQLite database (`AppDbContext`) holding `SitePolicy` rows (the policy
table), `RequestLog` rows (audit trail), `AdminUser` (admin login), and
`RootCertificateAuthority` (the TLS-intercepting proxy's signing CA, private key included).

Everything lives in a single ASP.NET Core project,
`src/RemoteBrowserIsolation.Server`, `net9.0`, top-level statements in `Program.cs`. There is no
microservice split — the proxy, the WebRTC signaling endpoint, the admin REST API, and the static
file server (`wwwroot/`, including the WebRTC test client `index.html`) are all one process.

## 3. The policy engine

`Services/PolicyEngine.cs` (`IPolicyEngine.ResolveAsync(Uri url)`) is the single choke point both
the proxy and the WebRTC signaling endpoint call before doing anything with a URL. It:

1. Loads every `SitePolicy` row from the DB (the table is admin-curated and expected to stay
   small, so this is a full in-memory scan, not an indexed query).
2. Matches the URL's host against each policy's `HostPattern` — exact match, or being a subdomain
   of it (`app.example.com` matches a policy for `example.com`).
3. If multiple patterns match, the **longest pattern wins** (a specific rule beats a broad one).
4. Returns the matched policy's `ViewMode`, or `null` if nothing matched.

**`null` means deny.** There is deliberately no "default allow" — an unlisted host is blocked
outright. This is enforced identically by both the proxy (`TlsInterceptingProxyServer`, which
returns `502 Bad Gateway` and logs a `"deny"` decision) and the WebRTC endpoint
(`SessionEndpoints`, which returns `403 Forbidden`).

`ViewMode` (`Models/ViewMode.cs`) has four values:

| Mode | Rendering | Input | Enforcement |
|---|---|---|---|
| `HtmlAllowInput` | Real page relayed via the proxy, executes in the user's own browser | Full input | — |
| `HtmlNoInput` | Same relay | Text entry/editable controls visually disabled via injected CSS; links/scroll/navigation still work | **Client-side only** — cosmetic, a hostile client can strip the CSS |
| `VideoAllowInput` | Server-side headless Chromium, streamed as VP8 | Mouse + keyboard both forwarded | — |
| `VideoNoInput` | Same server-side render | Mouse forwarded (click/scroll/navigate); keyboard dropped before replay | **Server-side** — the forwarder drops keydown/keyup before they reach the page, so a malicious client can't forge keystrokes |

Every request — allowed or denied — is written to `RequestLog` (`IRequestLogService`) for audit.

## 4. HTML mode: the TLS-intercepting forward proxy

Implemented in `Services/Proxy/TlsInterceptingProxyServer.cs`, this is the most unusual piece of
the codebase, so it's worth understanding from first principles.

### Why not just use Kestrel?

Kestrel (ASP.NET Core's built-in web server) understands "HTTP request in, HTTP response out." It
has no concept of the **forward-proxy protocol**: a browser configured with a proxy setting sends
either

- `GET http://example.com/path HTTP/1.1` (absolute-URI form, for plain HTTP), or
- `CONNECT example.com:443 HTTP/1.1` followed by (after a `200 Connection Established` reply) a
  raw TLS handshake tunneled through the same TCP connection, for HTTPS.

Kestrel can't be told "accept a plain-text CONNECT line, then start terminating TLS on the same
socket." So this project hand-rolls a `TcpListener`-based server (`TlsInterceptingProxyServer`,
registered as an `IHostedService` so it starts/stops with the rest of the app) that does exactly
that, alongside Kestrel (which still serves the admin UI, the WebRTC test client, and the REST
APIs on its own port).

### The CONNECT / TLS-interception flow

1. Browser sends `CONNECT example.com:443 HTTP/1.1`.
2. Server checks `Proxy:SelfHosts` — if the CONNECT target *is this server itself* (e.g. the
   browser, with its proxy setting active, tries to reach `localhost:5000` to load the admin UI or
   the WebRTC test client), the connection is blind-tunneled straight to Kestrel's real port with
   **no** policy check and **no** TLS interception, so the browser negotiates TLS directly with
   Kestrel and gets Kestrel's real certificate. Without this, the admin UI would be policy-checked
   and TLS-intercepted like any other site once the proxy is globally configured.
3. If the target port isn't in `Proxy:InterceptPorts` (default: 443 only), it's blind-tunneled to
   the real origin with no interception at all — raw bytes pumped both directions
   (`SpliceAsync`).
4. Otherwise, the policy engine resolves the host. `null` → `502`. Otherwise the server replies
   `200 Connection Established` and **starts terminating TLS itself**, using
   `SslStream.AuthenticateAsServerAsync` with a `ServerOptionsSelectionCallback` that mints a leaf
   certificate on the fly for whatever hostname the browser's TLS ClientHello names via SNI
   (`ILeafCertificateMinter.GetOrMintAsync`).
5. Once the TLS handshake completes, the server reads the *real* HTTP request (now decrypted) off
   that `SslStream` and dispatches it per the resolved `ViewMode` (see below).

This only works if the **user has installed this server's root CA into their OS/browser trust
store** — otherwise step 4's minted leaf certificate is untrusted and the browser shows a TLS
warning. The admin uploads a root CA (private key included) via `/admin/`; it's stored in the
`RootCertificateAuthority` DB table (see `RootCaStore`, a singleton in-memory cache over that
table) and used by `LeafCertificateMinter` to sign short-lived (7-day) leaf certs per hostname,
cached in-process.

### What happens per mode, once decrypted

- **`HtmlAllowInput`**: `OriginForwarder` fetches the real request from the real origin
  (`AllowAutoRedirect=false`, `UseCookies=false` — 3xx and `Set-Cookie` relay through as real
  headers instead of being swallowed by `HttpClient`) and relays the response back byte-for-byte.
- **`HtmlNoInput`**: same fetch, but if the response is HTML, it's run through
  `HtmlNoInputInjector`, which parses it with AngleSharp and injects a `<style>` rule disabling
  `pointer-events`/`user-select` on `input`/`textarea`/`select`/`[contenteditable]` elements. This
  is a **cosmetic nudge only** — a modified client, or a user with devtools, can strip the
  injected CSS and type anyway. It's documented as such everywhere in the codebase; don't rely on
  it for anything security-sensitive.
- **`VideoAllowInput` / `VideoNoInput`**: the proxy does **not** relay any real content. It serves
  a small static interstitial HTML page linking to `/index.html?url=<target>` — clicking that
  link opens this server's own WebRTC video viewer (§5) for the same URL. No real page bytes ever
  reach the browser through the proxy for these hosts.

## 5. Video mode: the WebRTC pipeline

This is the "actually isolate it" path. If you hit a connectivity issue here, see
[§13](#13-debugging-playbook) for known failure modes and how to diagnose them.

### Actors and their roles

- **Client** (`wwwroot/index.html`) — plain JS, no framework. Always the WebRTC **offerer**.
- **Server** (`WebRtcSessionManager.cs`) — always the WebRTC **answerer**. This isn't a free
  choice: since browsers can't easily be told to answer instead of offer, and this project only
  ever has one server-initiated flow, offerer/answerer roles are fixed this way.
- **Headless Chromium** (`HeadlessBrowserSessionManager.cs`, via Playwright) — where the actual
  target page's JS/HTML/CSS executes. One shared Chromium *process* per server; one isolated
  `BrowserContext` + `Page` per WebRTC session, so different users' sessions never share
  cookies/storage.

### The signaling handshake

WebRTC needs an out-of-band "signaling" step to exchange SDP (Session Description Protocol —
codecs, network candidates, crypto fingerprints) before a peer-to-peer(-ish) connection can form.
This project uses **the simplest possible signaling**: a single HTTP POST, no WebSocket, no
trickle ICE.

1. Client builds an `RTCPeerConnection`, adds a receive-only video transceiver, creates a
   **pre-negotiated data channel** with a fixed ID (`negotiated: true, id: 1`; see
   [§6](#datachannels-and-why-pre-negotiated)), creates an SDP offer, and waits for
   `iceGatheringState === 'complete'` (i.e. it finishes discovering all its own network
   candidates) before sending anything.
2. Client `POST`s the offer SDP + target URL + desired viewport to `/api/session/offer`
   (`Rest/SessionEndpoints.cs`).
3. Server resolves policy (403 if denied, 409 if the policy says HTML mode instead), then calls
   `WebRtcSessionManager.CreateSessionAsync`, which builds its own `RTCPeerConnection` (pinned to
   a fixed UDP port range — see [§11](#11-docker-deployment)), adds a matching send-only video
   track and the matching pre-negotiated data channel, sets the client's offer as its remote
   description, and calls `createAnswer` with `X_WaitForIceGatheringToComplete = true` — so the
   *answer* SDP the server returns already has all of the server's own ICE candidates baked in.
   No back-and-forth after this; the single HTTP response carries everything.
4. Server rewrites the answer SDP's host-candidate addresses to a configured "advertised IP"
   before returning it (again, [§11](#11-docker-deployment) explains why).
5. Client sets the returned SDP as its remote description. ICE connectivity checks
   (§6) begin; once they succeed, DTLS handshakes, then the data channel and video track become
   usable.

### Once connected: rendering and streaming

`WebRtcSessionManager` waits for `pc.onconnectionstatechange` to report `connected`, then (only
then — connecting the heavyweight infra only once a client actually completed the handshake):

1. `HeadlessBrowserSessionManager.CreateSessionAsync` opens a fresh `BrowserContext`+`Page` sized
   to the negotiated viewport and navigates it to the target URL.
2. `InputEventForwarder.Wire` is always called — the data channel is always wired up so mouse
   input works in *both* video modes (see the `ViewMode` table in §3). It decodes each incoming
   JSON `InputEvent` (`Models/InputEvent.cs`) and replays it against the Playwright `Page` via its
   virtual mouse/keyboard. `keydown`/`keyup` are silently dropped here when the session's
   `allowKeyboard` flag is false (`VideoNoInput`) — this is the actual server-side enforcement
   point, independent of whatever the client chooses to send.
3. `VideoTrackStreamer.StartAsync` opens a Chrome DevTools Protocol (CDP) screencast
   (`Page.startScreencast`) on the page. Each screencast frame arrives as a JPEG; a single-slot
   "latest wins" mailbox (`Channel.CreateBounded(1, DropOldest)`) decouples frame production from
   encoding so a burst of repaints doesn't queue up staleness. A consumer loop decodes each JPEG
   (`FFmpegVideoEncoder.DecodeFaster`, MJPEG), encodes it as VP8 (`libvpx`, tuned for realtime:
   `deadline=realtime`, `cpu-used=8`, `lag-in-frames=0`), and pushes it onto the peer connection's
   video track via `pc.SendVideo`. A forced keyframe every 5s lets a client that joins mid-stream
   (or loses a packet) resync.

This is a full transcode pipeline running per active session:
`CDP JPEG → FFmpeg decode → FFmpeg VP8 encode → RTP`. Frames don't travel over the data channel —
RTP has no application-imposed throughput ceiling, unlike the
data channel's SCTP sender, so both quality and resolution went up while latency went down.

## 6. WebRTC concepts explained

WebRTC bundles several IETF protocols. This section explains each one at the level needed to
debug this codebase — not a full spec walkthrough.

### SDP (Session Description Protocol)

A text format describing a proposed (or agreed) media session: what codecs, what network
candidates, what security fingerprints. An "offer" and an "answer" are both SDP documents. You'll
see lines like:

```
m=video 9 UDP/TLS/RTP/SAVP 120        <- a video media section, payload type 120
a=candidate:... typ host              <- an ICE candidate (see below)
a=fingerprint:sha-256 ...             <- the DTLS certificate fingerprint for this side
a=setup:active                        <- DTLS role (see "DTLS" below)
a=ice-ufrag:... a=ice-pwd:...         <- ICE credentials, used to authenticate connectivity checks
```

### ICE (Interactive Connectivity Establishment)

The core problem ICE solves: two peers, each potentially behind NAT/firewalls, need to find *some*
IP:port pair over which they can actually exchange UDP packets. Each side gathers a list of
**candidates** (its own possible address:port pairs) and exchanges them via SDP. Then both sides
try every candidate pair, sending STUN binding-request packets, until one pair succeeds
("nominated"). Candidate types, in this project's logs:

- **`host`** — a real local interface address (e.g. `192.168.0.128:58554`, or, for a browser, a
  synthetic mDNS name — see below).
- **`srflx`** (server-reflexive) — "what does the outside world see as my address," learned by
  asking a STUN server. Useful when the true local address isn't directly reachable/nameable by
  the peer.
- **`relay`** — a TURN server relays traffic between both peers (not used in this project — no
  TURN server is configured; see the debugging note in §13 about the "add a TURN server" message).

**This project's server pins its ICE/RTP UDP socket to a fixed port range** (`WebRtc:UdpPortStart`
/ `UdpPortEnd`, default `40000`–`40009`) instead of letting the OS pick an ephemeral port, purely
so a Docker container can publish exactly those ports (`-p 40000-40009:40000-40009/udp`) — see
§11.

### mDNS candidate obfuscation

Chrome and Firefox, by default, **hide a host candidate's real local IP** behind a randomly
generated `<uuid>.local` hostname (a privacy feature — otherwise any website you visit over
WebRTC could fingerprint your local network topology). The *other* peer is expected to resolve
that `.local` name via multicast DNS (mDNS) — a UDP broadcast on the local network segment asking
"who is `<uuid>.local`?", which only the browser that minted it will ever answer.

**This breaks when the answering peer is in a Docker bridge network**: Docker's default bridge
does not forward multicast traffic between a container and the host, so the server-side mDNS
query for that random `.local` name times out, and no destination address is ever known for that
candidate — the whole candidate pair is simply unusable. In this project's SIPSorcery debug logs
this shows up as:

```
RTP ICE channel MDNS resolver failed to resolve <uuid>.local.
RTP ICE Channel could not create a check list entry for a remote candidate with no destination end point, ...
ICE RTP channel failed to connect as no checklist entries became available within 16.0249471s.
```

and the browser reports **"ICE failed, add a STUN server."**

**The fix used here**: configure a STUN server on the *client* (`wwwroot/index.html`'s
`RTCPeerConnection({ iceServers: [...] })`). mDNS obfuscation only hides `host`-type candidates —
it doesn't touch `srflx` candidates, since a server-reflexive candidate is, by definition, already
an address as seen from outside (there's no local-network information to leak). Adding a STUN
server makes the browser *also* gather a `srflx` candidate with its real (non-obfuscated) address,
giving the server something it can actually complete connectivity checks against. This is
literally what the browser's own error message says to do — it's not really about NAT traversal
in this same-host scenario, it's a workaround for mDNS being unresolvable from inside the
container.

Two other things NAT/network topology can change here, both explicitly rejected for this project
(see `plans/10_docker_image.md`'s locked decisions and `WebRtcSessionManager`'s SDP-rewrite doc
comment):

- `--network host` would give the container access to the host's multicast domain (fixing mDNS
  resolution directly) but was rejected because Docker Desktop (Mac/Windows) has no usable host
  networking, and this image must run cross-platform.
- The server also rewrites its **own** answer SDP's host-candidate address (`c=IN IP4 ...` and
  `a=candidate ... typ host`) from the container's internal bridge IP (e.g. `172.17.0.2`,
  unreachable from the host browser) to a configured `WebRtc:AdvertisedIp` (default `127.0.0.1`,
  the common case of browser and container sharing a host and reaching the published port range
  via loopback) — see `WebRtcSessionManager.RewriteHostCandidateAddresses`.

### DTLS (Datagram Transport Layer Security)

TLS, but over UDP instead of TCP (UDP doesn't guarantee ordered/reliable delivery, so DTLS adds
retransmission/reordering handling TLS doesn't need). WebRTC uses a **single DTLS handshake** to
both (a) derive the SRTP keys that encrypt the video/audio RTP streams, and (b) directly
encapsulate the SCTP association carrying the data channel. One SDP attribute negotiates which
side acts as the DTLS client (the one that sends the first handshake message) versus server: the
offerer proposes `a=setup:actpass` (either is fine with me), and the answerer picks a concrete
`a=setup:active` (I'll be the client) or `a=setup:passive` (I'll be the server).

### SCTP and data channels

RTCDataChannel is not a WebRTC-native transport — it's **SCTP** (Stream Control Transmission
Protocol, an IETF transport protocol with TCP-like reliability but message-oriented, not
byte-stream-oriented) tunneled inside the same DTLS association used for media. SCTP has its own
three-way-ish handshake (INIT / INIT-ACK-with-cookie / COOKIE-ECHO / COOKIE-ACK) before any data
can flow, and its own `ABORT` chunk a peer can send to unilaterally terminate the association. An
`ABORT` received right as a session ends (tab closed, navigated away) is normal teardown. An
`ABORT` received instead of the data channel ever reaching `open` means the association never
completed — check whether the SCTP `INIT` was ever received (SIPSorcery debug logs, `Logging:
LogLevel:SIPSorcery=Debug`) and correlate with the client's `inputChannel.onopen`/`onclose`
console logs to see which side gave up.

#### DataChannels and why pre-negotiated

WebRTC data channels normally use in-band signaling (DCEP — Data Channel Establishment Protocol)
to announce a new channel to the other peer without needing a new SDP round-trip. This project
doesn't use that: because the server is *always* the answerer, it can never unilaterally introduce
a new SDP media section of its own (only the offerer proposes media sections; the answerer only
accepts or rejects). So both sides instead create the data channel **pre-negotiated, with a fixed
ID** (`negotiated: true, id: 1`) — both client and server just agree the channel exists and skip
the DCEP open/ack handshake entirely, since the ID match is agreed out-of-band (i.e., hardcoded in
both `index.html` and `WebRtcSessionManager.cs` — if you ever change one side's ID, you must change
the other to match, or the two sides will each think they own a different channel).

### STUN vs TURN

- **STUN** (Session Traversal Utilities for NAT) — a lightweight, stateless protocol: "tell me
  what IP:port you see this packet coming from." Used to discover `srflx` candidates (see ICE
  above). This project uses Google's public STUN server (`stun.l.google.com:19302`) client-side.
- **TURN** (Traversal Using Relays around NAT) — a full relay server: if no direct path exists
  between peers at all, TURN relays every packet through itself. Slower (adds a hop, costs
  bandwidth on the TURN server), used only as a last resort. **This project does not configure a
  TURN server** — if you ever see the browser's ICE-failure message change from "add a STUN
  server" to "add a TURN server," that specifically means STUN-derived `srflx` candidates were
  gathered but still couldn't form a working pair (a much harder network topology than this
  project is designed for — same-host or same-LAN browser/server is the supported case).

## 7. SIPSorcery: the WebRTC library

[SIPSorcery](https://github.com/sipsorcery-org/sipsorcery) is a C#/.NET library implementing SIP,
VoIP, and WebRTC (`RTCPeerConnection`, ICE, DTLS, SCTP, SRTP — essentially everything in §6,
in managed code, no native WebRTC library dependency). This project uses it for every
`RTCPeerConnection` (`WebRtcSessionManager.cs`), the video track's RTP sending
(`VideoTrackStreamer.SendVideo`), and the input data channel (`InputEventForwarder`).

Two things worth knowing if you touch this code:

- **Its public API is inconsistently named** (`setLocalDescription` vs `SetRemoteDescription` are
  different methods/overloads with different casing conventions) and not fully covered
  by XML doc comments. `CLAUDE.md`'s "Working with the SIPSorcery API" section has the exact
  commands to inspect the installed version's real member list — don't guess signatures from
  memory or from an older tutorial.
- **It logs through its own static `LogFactory`, not the ASP.NET Core DI logging pipeline.**
  Unless something explicitly calls `SIPSorcery.LogFactory.Set(loggerFactory)`, every internal
  SIPSorcery log line (ICE candidate checks, DTLS handshake steps, SCTP association events —
  everything referenced throughout §6) is silently dropped, *regardless* of what your
  `appsettings.json` `Logging:LogLevel` says. `Program.cs` wires this up right after
  `builder.Build()`. If you're debugging a WebRTC connectivity issue and seeing suspiciously
  little SIPSorcery output, check this is still wired — it's an easy thing to lose in a refactor,
  and its absence is *silent*, not an error.

`SIPSorceryMedia.FFmpeg` is a companion package that bridges SIPSorcery's video track abstraction
to native FFmpeg encode/decode (`FFmpegVideoEncoder` in `VideoTrackStreamer.cs`) — SIPSorcery
itself has no video codec implementation, just the RTP transport.

## 8. Third-party dependencies

From `RemoteBrowserIsolation.Server.csproj`:

| Package | Purpose | Why this one |
|---|---|---|
| `SIPSorcery` | WebRTC (`RTCPeerConnection`, ICE, DTLS, SCTP, SRTP) | The only mature managed-code (no native lib) WebRTC implementation for .NET |
| `SIPSorceryMedia.FFmpeg` | VP8 video encode/decode for the video track | Companion package bridging SIPSorcery's video abstraction to native FFmpeg codecs |
| `Microsoft.Playwright` | Headless Chromium automation for video-mode rendering, plus CDP screencast for frame capture | Cross-platform, actively maintained, first-class CDP access needed for `Page.startScreencast` |
| `AngleSharp` | HTML parsing/serialization for `HtmlNoInputInjector`'s charset normalization + `<style>` injection | A real HTML5-spec-compliant parser (regex-based HTML mangling is unreliable); no network I/O configured, so it's safe to use on untrusted response bodies |
| `Microsoft.EntityFrameworkCore.Sqlite` (+ `.Design`) | ORM + migrations for the SQLite-backed policy/log/CA/admin-user store | Zero-ops embedded DB fits this project's single-process, no-external-services deployment model; `.Design` is dev-only (migration generation), not shipped at runtime |
| `Microsoft.AspNetCore.Authentication.JwtBearer` | Admin console auth | Standard bearer-JWT flow for the `/api/admin/*` REST surface |
| `Microsoft.AspNetCore.OpenApi` | API schema/docs generation | Standard ASP.NET Core minimal-API tooling |

Runtime (non-NuGet) dependencies pulled in by the Docker image (`Dockerfile`), each independently
versioned and installed:

| Dependency | Why |
|---|---|
| Headless Chromium (via `playwright.ps1 install --with-deps chromium`) | The actual browser instance video mode renders pages in |
| FFmpeg 8.x shared libs (`libavcodec.so.62` etc., from a pinned BtbN release build) | `SIPSorceryMedia.FFmpeg`'s native codec dependency; Ubuntu's own apt-packaged FFmpeg is 6.x, ABI-incompatible |
| PowerShell (`pwsh`) | Playwright's .NET port only ships a `.ps1` browser-installer script, no shell-script equivalent |

## 9. Data model & admin console

`Data/AppDbContext.cs` (SQLite, EF Core migrations under `Data/Migrations/`), four entities:

- **`SitePolicy`** (`Data/Entities/SitePolicy.cs`) — `HostPattern` + `ViewMode`. The policy table
  §3 reads.
- **`RequestLog`** (`Data/Entities/RequestLog.cs`) — one row per request (allowed or denied),
  written by both the proxy and the WebRTC endpoint via `IRequestLogService`. Audit trail, viewed
  in the admin console.
- **`RootCertificateAuthority`** (`Data/Entities/RootCertificateAuthority.cs`) — the admin-uploaded
  CA's PFX bytes + password, private key included. Read by `RootCaStore` (§4).
- **`AdminUser`** (`Data/Entities/AdminUser.cs`) — admin login credentials for the console.

The admin console itself is server-rendered static content under `wwwroot/admin/` (not covered in
depth here — see `Rest/Admin/*.cs` for the REST surface: `AdminAuthEndpoints`,
`AdminSiteEndpoints` (policy CRUD), `AdminLogEndpoints`, `AdminRootCaEndpoints`). All admin
endpoints except login/status require a bearer JWT (`Program.cs`'s `AddJwtBearer` config).

## 10. Configuration reference

All config is standard ASP.NET Core layered config (`appsettings.json`, overridable by environment
variables using `__` as the section separator — e.g. `WebRtc__AdvertisedIp` overrides
`WebRtc:AdvertisedIp`). This section documents every setting, since several only make sense with
the context from earlier sections.

### `Proxy` (`Models/Proxy/ProxyOptions.cs`, §4)

| Key | Default | Meaning |
|---|---|---|
| `Proxy:Port` | `8080` | Port the hand-rolled TLS-intercepting proxy listener binds. Point the browser/OS proxy setting here. |
| `Proxy:Bind` | `127.0.0.1` | Interface the proxy listener binds. The Docker image overrides this to `0.0.0.0` (`Proxy__Bind=0.0.0.0` in the `Dockerfile`) so it's reachable from outside the container — a host browser's proxy setting can't reach a proxy bound only to the container's own loopback. |
| `Proxy:InterceptPorts` | `[443]` | CONNECT to one of these ports gets policy-checked and TLS-intercepted; any other port is blind-tunneled straight to origin with no policy check, no cert minting, no MITM. |
| `Proxy:SelfHosts` | `["localhost", "127.0.0.1"]` | Hostnames that mean "this server's own origin" — see §4's self-origin bypass. **`run_docker.sh` can append a third entry via `RBI_SELF_HOST`**, which maps to the env var `Proxy__SelfHosts__2` (array index `2`, i.e. the slot *after* the two appsettings.json-baked entries at indices 0 and 1). ASP.NET Core's config binder merges environment-variable array entries with the appsettings.json ones **by index**, not by replacing the whole array — so setting only `Proxy__SelfHosts__2` appends a third self-host rather than overwriting the built-in two. You need this whenever the browser's proxy setting and its address bar for this app *disagree* — e.g. you reach the app via a LAN IP or a real hostname instead of `localhost`/`127.0.0.1`. Without it, the browser's own requests to the admin console or the WebRTC viewer would get policy-checked and TLS-intercepted like any other site once the proxy is globally configured, instead of bypassing straight to Kestrel. |

### `WebRtc` (`Models/WebRtcOptions.cs`, §5, §6, §11)

| Key | Default | Meaning |
|---|---|---|
| `WebRtc:AdvertisedIp` | `127.0.0.1` | The IP address substituted into the WebRTC answer SDP's session-level connection line (`c=IN IP4 ...`) and host-candidate lines (`a=candidate ... typ host`) in place of whatever address the server's `RTCPeerConnection` actually bound to. This matters specifically because inside Docker, that real bound address is the container's internal bridge IP (e.g. `172.17.0.2`) — completely unreachable from a browser running on the host. The default assumes the common case (browser and container share a machine, reaching the container's published UDP port range via loopback). `run_docker.sh` exposes this as `RBI_ADVERTISED_IP` (mapped to the env var `WebRtc__AdvertisedIp`); set it to the host's real LAN IP if the browser is on a *different* machine than the container. See §5's SDP-rewrite walkthrough and §6's ICE section for the full mechanism — this rewrite is orthogonal to, and doesn't replace, the client-side STUN workaround for mDNS candidates. |
| `WebRtc:UdpPortStart` / `WebRtc:UdpPortEnd` | `40000` / `40009` | Inclusive UDP port range the server's ICE/RTP socket is bound within (via SIPSorcery's `PortRange`), instead of an OS-chosen ephemeral port — required so this exact range can be published deterministically (`-p 40000-40009:40000-40009/udp`). See §11. |

### `Jwt`

| Key | Default | Meaning |
|---|---|---|
| `Jwt:Key` | a dev-only placeholder string | HMAC signing key for admin-console bearer JWTs. **Must be overridden in any real deployment** — the shipped default is intentionally labeled `CHANGE_ME_dev_only_...` and is not a secret worth keeping. |
| `Jwt:Issuer` / `Jwt:Audience` | `RemoteBrowserIsolation.Server` / `RemoteBrowserIsolation.Admin` | Standard JWT validation claims. |
| `Jwt:TtlMinutes` | `60` | Admin session token lifetime. |

### `FFmpeg`

| Key | Default | Meaning |
|---|---|---|
| `FFmpeg:LibPath` | a hardcoded host dev path | Directory containing the FFmpeg 8.x shared libs (`libavcodec.so.62` etc.) `SIPSorceryMedia.FFmpeg` needs natively. The Docker image overrides this to `/opt/ffmpeg/lib` (`FFmpeg__LibPath` in the `Dockerfile`, matching where the image's FFmpeg download is extracted). If this path is wrong, `Program.cs` fails fast at startup with an actionable error rather than letting the first video session die with an obscure `DllNotFoundException`. |

### `ConnectionStrings:Sqlite`

Default `Data Source=rbi.db` (relative to the working directory). The Docker image overrides this
to `Data Source=/app/data/rbi.db` (`ConnectionStrings__Sqlite`), pointed at the bind-mounted
`./data` volume so the DB — and the CA/policies/admin users it holds — survives image rebuilds.

### `Logging:LogLevel`

Standard ASP.NET Core hierarchical log-level config. Notably, **`Logging:LogLevel:SIPSorcery`
does nothing unless `SIPSorcery.LogFactory.Set(...)` has also been wired in `Program.cs`** — see
§7's explanation of SIPSorcery's separate static logging pipeline. Set it to `Debug` (e.g.
`-e "Logging__LogLevel__SIPSorcery=Debug"` on `docker run`) when chasing an ICE/DTLS/SCTP issue;
it's very verbose, so don't leave it on by default.

## 11. Docker deployment

`Dockerfile` + `scripts/{compile,build_docker,run_docker}.sh` produce a self-contained runtime
image so end users don't need a local .NET/Playwright/FFmpeg toolchain. `scripts/compile.sh`
publishes the app on the *host* first (`dotnet publish -c Release -o publish/`); `Dockerfile` then
just assembles the runtime image around that prebuilt output — no in-image `dotnet restore`/build.

Two WebRTC-specific config knobs exist purely because of containerization (`WebRtcOptions.cs`,
bound from the `WebRtc` appsettings section):

- **`WebRtc:UdpPortStart`/`UdpPortEnd`** (default `40000`–`40009`) — pins the ICE/RTP socket to a
  fixed range so it can be published deterministically (`-p 40000-40009:40000-40009/udp`) instead
  of relying on an OS-chosen ephemeral port, which Docker can't publish ahead of time.
- **`WebRtc:AdvertisedIp`** (default `127.0.0.1`) — the address substituted into the answer SDP's
  host candidates in place of the container's internal (unreachable-from-outside) bridge address.
  See §6's ICE section for the full mechanism.

The Playwright `.ps1` installer needs its *full* dependency closure present (deps.json,
runtimeconfig.json, the bundled Node-based driver under `.playwright/`) to run via reflection.
Copying only `playwright.ps1` plus `Microsoft.Playwright.dll` is not enough: the PowerShell script
throws internally, but `pwsh` still exits `0`, so `docker build` reports success while
`PLAYWRIGHT_BROWSERS_PATH` is left completely empty. Every subsequent video-mode session then
fails at runtime with `Executable doesn't exist at /ms-playwright/.../chrome-headless-shell` — a
build-time problem that only surfaces at request time. The `Dockerfile` copies the *entire*
`publish/` output before running the install step to avoid this. To verify the install actually
populated the browsers path: `docker exec <container> ls /ms-playwright` should list
`chromium-<rev>`, `chromium_headless_shell-<rev>`, and `ffmpeg-<rev>` directories; an empty or
missing `/ms-playwright` means the install step silently no-op'd and the image needs a real
rebuild (`docker build --no-cache`), not just a container restart.

**Testing convention**: when building/running this image for your own debugging (not the
project's actual `run_docker.sh` flow), always use a different name — `rbi-testing` — for both the
image tag and the container name. Reusing `rbi` collides with a user's already-running long-lived
container and forces a full Chromium/FFmpeg re-download on their next `run_docker.sh`.

## 12. Project layout reference

```
src/RemoteBrowserIsolation.Server/
  Program.cs                     — composition root, top-level statements
  Data/
    AppDbContext.cs               — EF Core context
    Entities/                     — SitePolicy, RequestLog, RootCertificateAuthority, AdminUser
    Migrations/
  Models/                         — DTOs and small value types (ViewMode, InputEvent, WebRtcOptions, ...)
    Admin/                        — admin REST request/response DTOs
    Proxy/                        — ProxyOptions
  Rest/                           — minimal-API endpoint mapping extension methods
    Admin/                        — admin-only endpoints (auth, site policy CRUD, logs, root CA)
    PolicyEndpoints.cs            — GET /api/policy/resolve (client-side mode hint)
    SessionEndpoints.cs           — POST /api/session/offer (WebRTC signaling)
  Services/
    PolicyEngine.cs                — §3
    WebRtcSessionManager.cs        — §5, peer connection lifecycle
    HeadlessBrowserSessionManager.cs — §5, Playwright/Chromium ownership
    VideoTrackStreamer.cs          — §5, CDP screencast -> FFmpeg -> RTP
    InputEventForwarder.cs         — §5, data channel -> Playwright virtual input
    PageDownloader.cs              — legacy/diagnostic-only, see its own doc comment
    RequestLogService.cs, AdminAuthService.cs
    Proxy/
      TlsInterceptingProxyServer.cs — §4, the hand-rolled forward proxy
      OriginForwarder.cs            — HTML-mode origin fetch
      LeafCertificateMinter.cs      — §4, per-hostname leaf cert minting
      RootCaStore.cs                — §4, in-memory CA cache
      HtmlNoInputInjector.cs        — §4, cosmetic no-input CSS injection
      HttpMessageIO.cs, ProxyStreamReader.cs, ProxyMessages.cs, ProxyHeaders.cs — raw HTTP/1.1 parsing helpers for the proxy's hand-rolled socket handling
  wwwroot/
    index.html                     — the WebRTC test client / video viewer (plain JS, no framework)
    admin/                         — admin console static assets
Dockerfile, scripts/                — §11
docs/developer_doc.md               — this file
README.md                           — product-level overview, mode enforcement table
CLAUDE.md                           — terse build/run commands + house style rules
plans/                              — per-iteration design docs (historical + current; see CLAUDE.md's note on which are current)
```

## 13. Debugging playbook

Known failure modes for the video-mode WebRTC path, keyed by observable symptom.

**Symptom: browser console says `WebRTC: ICE failed, add a STUN server`.**
Root cause: Chrome/Firefox mDNS-obfuscate host candidates by default; a Docker bridge network
can't resolve them (no multicast reachability to the host). Fix: configure a STUN server
client-side so the browser also gathers a real (non-obfuscated) `srflx` candidate. Full
explanation in §6.

**Symptom: same message, but says "add a TURN server" instead of "add a STUN server."**
Progress, not a new problem — it means STUN-derived candidates were successfully gathered and
exchanged, but the connectivity checks against them still failed. Check SIPSorcery debug logs
(`Logging:LogLevel:SIPSorcery=Debug`, and confirm `SIPSorcery.LogFactory.Set` is wired — see §7)
for the actual candidate pairs being tried and why they're failing; this is a harder network
topology than "browser and server share a host or LAN," which is the only case this project's
current STUN-only setup (no TURN) supports.

**Symptom: video mode renders, but every session eventually fails with
`Executable doesn't exist at /ms-playwright/.../chrome-headless-shell`.**
Root cause: the Docker image's Playwright browser install step silently no-op'd at build time
(see §11's Dockerfile lesson). Confirm with `docker exec <container> ls /ms-playwright` — if the
directory is missing or empty, a rebuild is required, not just a container restart.

**Symptom: `SCTP packet ABORT chunk received from remote party, reason (null)` in the server
logs.**
Seen both as routine teardown (a session ending/navigating away legitimately tears down its SCTP
association this way) and, if it happens *instead of* the data channel ever reaching `open`, as a
real connectivity problem. Correlate with the client's `inputChannel.onopen`/`onclose` console
logs (already wired in `index.html`) to tell which case you're in before chasing further.

**Symptom: mouse works but keyboard doesn't, or neither works, in one specific `ViewMode`.**
Check both ends of the enforcement split described in §3's `ViewMode` table:
`InputEventForwarder.HandleAsync`'s `allowKeyboard` filter (server-side, authoritative) and
`wwwroot/index.html`'s `inputChannel.onopen` handler (client-side — must call
`wireInputCapture` unconditionally; it should never skip capture based on mode, since the server
is what enforces the restriction, not the client choosing not to send).
