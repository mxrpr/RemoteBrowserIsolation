# Iteration 3 Plan: VP8 Video Track

Based on iteration 2 (`2_iteration_server_side_rendering.md`), which flagged this as the known
follow-up: *"if frame rate/latency turns out to be unacceptable, revisit using an actual WebRTC
video track."* Both predicted limits were hit in practice.

## Why

Two measured constraints of the JPEG-over-datachannel transport:

1. **SIPSorcery's SCTP sender drains at a fixed ~75KB/s** regardless of network capacity
   (measured: ~120KB frame → ~1.55s, ~51KB frame → ~0.72s — same rate). Every quality/latency
   trade so far (640×360, JPEG q40, latest-wins conflation) exists to live under this ceiling.
2. **Full-frame JPEGs waste most of those bytes** — no inter-frame compression, every frame pays
   for the whole image even when two pixels changed.

A real VP8 video track over RTP bypasses the SCTP path entirely (media flows over SRTP, which has
no such ceiling in SIPSorcery) and delta-frames give far better quality per byte. Target outcome:
1280×720 at visibly better quality *and* lower latency than today's 640×360 q40.

## Environment groundwork (done during planning)

- CDP screencast only emits JPEG/PNG, so the server must transcode: JPEG → raw → VP8.
- `SIPSorceryMedia.FFmpeg` 10.0.11 (matches our SIPSorcery 10.0.11) binds **FFmpeg 8** via
  FFmpeg.AutoGen 8.1; Ubuntu 24.04's system FFmpeg is 6.1 — incompatible. `SIPSorceryMedia.Encoders`
  ships Windows-only natives, dead end on Linux.
- Resolution: BtbN GPL **shared** build of FFmpeg n8.1 installed at `~/apps/ffmpeg-8.1`;
  `FFmpegInit.Initialise(logLevel, libPath, logger)` takes the lib directory explicitly, so no
  system-wide install or LD_LIBRARY_PATH needed. Verified working: VP8 encode of a raw I420 frame
  succeeds, and `VideoCodecsEnum.JPEG` is available for decoding the screencast frames.
- The FFmpeg lib path must be configurable (`appsettings.json`, e.g. `FFmpeg:LibPath`) since it's a
  per-machine location.

## Design decisions

- **Codec: VP8** — universally supported by browsers for WebRTC, already proven working with the
  installed FFmpeg build. H.264 (libx264/openh264 also present) kept as a fallback option only.
- **Pipeline**: keep CDP screencast as the frame source (it already self-throttles to page repaints
  and carries no extra CDP surface); per frame: base64 JPEG → `FFmpegVideoEncoder.DecodeVideoFaster`
  (JPEG → raw) → `EncodeVideo` (raw → VP8) → `RTCPeerConnection.SendVideo`. One
  encoder+decoder pair per session (encoders are stateful: delta frames reference prior frames).
- **Latest-wins conflation stays**: the encode step takes a few ms and RTP send is fast, but the
  mailbox pattern from iteration 2 still bounds staleness if a burst of repaints outpaces encoding;
  it also keeps encoding strictly serial, which the stateful VP8 encoder requires anyway.
- **Signaling**: client adds a recvonly video transceiver to its offer; server `addTrack`s a VP8
  `MediaStreamTrack` before answering, and starts pushing frames once
  `OnVideoFormatsNegotiated` fires and the connection is up. The input data channel (id 1)
  is unchanged; the frame data channel (id 0) is removed along with the client-side reassembler.
- **Client rendering**: `ontrack` → `MediaStream` → `<video autoplay muted>` element replacing the
  canvas. Input capture moves to the video element; coordinate scaling (CSS px → frame px) works the
  same way via `videoWidth`/`videoHeight`.
- **Viewport adaptation carries over**: client-requested size (clamped) still drives the headless
  viewport and screencast size; the 1280×720 cap can stay (now for encoder CPU reasons, not
  bandwidth) — revisit upward later.

## Steps

1. Add `SIPSorceryMedia.FFmpeg` package; wire `FFmpegInit.Initialise` at startup with the lib path
   from config. Fail fast with a clear error if the libs are missing.
2. Implement `VideoTrackStreamer` (replaces `FrameStreamer`): CDP screencast → JPEG decode → VP8
   encode → `SendVideo`, latest-wins mailbox, per-session encoder lifecycle (dispose on teardown).
3. Update `WebRtcSessionManager`: add VP8 video track to the peer connection, drop the id-0 frame
   data channel, start the streamer on connection/format negotiation instead of channel open.
4. Update `wwwroot/index.html`: recvonly video transceiver in the offer, `<video>` element instead
   of canvas, input capture + CSS→frame coordinate scaling against the video element.
5. End-to-end verification with a Playwright-driven client: video renders (element receives frames,
   `videoWidth` matches requested size), click accuracy preserved, and a quality/latency comparison
   against the JPEG path on an animated page.
6. Remove the now-dead JPEG frame path (`DataChannelTransport` stays — the input channel and any
   future data needs still use it — but the frame framing/reassembly code goes).

## Acceptance criteria

- Client receives the rendered page as a real VP8 video stream (`<video>` element playing).
- Perceived quality at 1280×720 clearly better than the current 640×360 JPEG q40.
- Click-to-visible-feedback latency on an animated page no worse than the current ~150ms.
- Multiple concurrent sessions still isolated, each with its own encoder instance.
- Session teardown disposes encoder + CDP + browser context without leaks; server runs without the
  FFmpeg libs present only if it fails fast at startup with an actionable message.

## Non-goals

- Audio track.
- Hardware encoding (VAAPI etc. — software libvpx is fine at 720p).
- Congestion-adaptive bitrate (fixed sensible bitrate first; revisit if WAN use appears).
- Fixing/tuning the SCTP data-channel throughput ceiling (input events are tiny; ceiling is
  irrelevant once frames leave that path).
