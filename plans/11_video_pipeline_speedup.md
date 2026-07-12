# 11 — Video pipeline speedup — IMPLEMENTATION PLAN

Goal: reduce per-frame transcode time in video mode. Hard constraint from the user:
**JPEG quality (`JpegQuality = 80`) must NOT be lowered.** Viewport bounds also stay unchanged.

This plan is written to be executed step by step by another model without further research.
All signatures below were verified against the installed packages
(`SIPSorceryMedia.FFmpeg` 10.0.11, `SIPSorceryMedia.Abstractions` 10.0.11, `FFmpeg.AutoGen` 8.1.0).

---

## Background — current pipeline and the two confirmed inefficiencies

File: `src/RemoteBrowserIsolation.Server/Services/VideoTrackStreamer.cs`

Current per-frame path at 720p (see `TranscodeLoopAsync`, lines ~110–177):

```
CDP screencast JPEG (quality 80)
  -> decoder.DecodeFaster(AV_CODEC_ID_MJPEG, jpegBytes, ...)   // MJPEG decode + sws_scale to RGB24
  -> encoder.EncodeVideoFaster(rawFrames[0], VP8)              // sws_scale RGB24 -> YUV420P + libvpx encode
  -> pc.SendVideo(durationRtpUnits, encoded)
```

Confirmed by reading `SIPSorceryMedia.FFmpeg` source (FFmpegVideoEncoder.cs, upstream master,
matches 10.0.11 API surface):

1. **Pointless RGB round-trip.** MJPEG decode natively outputs YUVJ420P (full-range YUV420P).
   `DecodeFaster` always converts the decoded frame to RGB24 via `VideoFrameConverter`
   (sws_scale pass #1); `EncodeVideoFaster` then converts RGB24 back to YUV420P (sws_scale
   pass #2) because libvpx wants YUV420P. Two full-frame conversions per frame doing net nothing.
2. **Encoder is single-threaded.** `FFmpegVideoEncoder.SetThreadCount(int?)` exists but is never
   called. `_thread_count` is applied to `_encoderContext->thread_count` inside
   `InitialiseEncoder()`, which runs on the FIRST `EncodeVideoFaster` call — so calling
   `SetThreadCount` right after constructing the encoder (before any frame) is correct and safe.

## Verified API signatures (SIPSorceryMedia.FFmpeg 10.0.11)

```csharp
public FFmpegVideoEncoder(Dictionary<string, string>? encoderOptions = null,
    AVHWDeviceType HWDeviceType = AVHWDeviceType.AV_HWDEVICE_TYPE_NONE);

public void SetThreadCount(int? threadCount);
public byte[]? EncodeVideoFaster(RawImage rawImage, VideoCodecsEnum codec);
public List<RawImage>? DecodeFaster(AVCodecID codecID, byte[] buffer, out int width, out int height);
public void ForceKeyFrame();
public void SetBitrate(long? avgBitrate, int? toleranceBitrate, long? minBitrate, long? maxBitrate);
```

`RawImage` (SIPSorceryMedia.Abstractions) members: `Width`, `Height`, `Stride`, `Sample` (IntPtr
to pixel data), `PixelFormat` (`VideoPixelFormatsEnum`), `GetBuffer()`.

`EncodeVideoFaster` pixel-format support (via internal `GetAVPixelFormat` mapping):
`Bgr -> AV_PIX_FMT_BGR24`, `Bgra -> AV_PIX_FMT_BGRA`, **`I420 -> AV_PIX_FMT_YUV420P`**,
`NV12 -> AV_PIX_FMT_NV12`, `Rgb -> AV_PIX_FMT_RGB24`. I420 input therefore makes the encoder's
input conversion a same-format pass (cheap) — this is what Step 3 exploits.

## Decisions (locked)

| Topic | Decision | Why |
|-------|----------|-----|
| JPEG quality | Stays at 80 | User constraint. |
| Viewport bounds | Unchanged (320x180–1280x720) | Not part of this iteration. |
| Codec | Stay on software VP8 (libvpx) for Steps 1–3 | Hardware encode is Step 4, deferred. |
| Measurement | Existing debug log line `"Video frame {Count} for {Url}: jpeg {JpegBytes}B -> vp8 {Vp8Bytes}B in {Ms:F1}ms"` | Already measures the full decode+encode per frame; no new instrumentation. |
| Commit granularity | One commit per step, independently revertable | Each step is separately measurable. |

## Repo conventions the implementer MUST follow

- CLAUDE.md: every new class/method gets a comment describing its function.
- C# style (`csharp-style` skill): explicit types (no `var` where the project avoids it — note:
  this file's existing code DOES use `var`; match the surrounding file's existing style), braces
  always, doc comments on new public members.
- No test project exists; verification is manual (see "Verification" per step).

---

## Step 1 — Multithread the VP8 encoder

**File:** `src/RemoteBrowserIsolation.Server/Services/VideoTrackStreamer.cs`
**Location:** `TranscodeLoopAsync`, immediately after `encoder.SetBitrate(...)` (currently line ~124).

Change:

```csharp
using var encoder = new FFmpegVideoEncoder(realtimeOptions);
encoder.SetBitrate(TargetBitrate, null, MinBitrate, MaxBitrate);
// libvpx multithreading: thread_count is applied to the codec context when the encoder is
// initialised on the first frame. Without this the encode runs on a single core and is the
// dominant per-frame cost (~15-20ms at 720p).
encoder.SetThreadCount(Environment.ProcessorCount);
```

Notes for the implementer:

- Do NOT cap the value; libvpx internally clamps threads to what the frame size can use once
  token partitions (Step 2) are set.
- `SetThreadCount` must be called before the first `EncodeVideoFaster` call (encoder context is
  created lazily on first frame). Placing it next to `SetBitrate` guarantees that.
- The decoder (`decoder` variable) does NOT need thread count — MJPEG decode is cheap relative
  to encode, and Step 3 replaces it anyway.

**Verification:** build (`cd src/RemoteBrowserIsolation.Server && dotnet build`), run
`./startRBI_dev.sh`, open a `VideoAllowInput` site with animation (e.g. a video-playing page),
set `Logging:LogLevel:Default` to `Debug`, collect ≥100 `Video frame` log lines, compare median
`in {Ms}ms` against baseline (collect baseline BEFORE the change). Expect a clear drop on a
multi-core machine (encode stage typically 2–4x faster).

---

## Step 2 — libvpx screen-content encoder options

**File:** same, `TranscodeLoopAsync`, the `realtimeOptions` dictionary (currently lines ~117–122).

Change the dictionary to:

```csharp
// libvpx realtime tuning: "deadline=realtime" caps how long the encoder may spend per
// frame, "cpu-used=8" trades a little compression efficiency for speed, "lag-in-frames=0"
// forbids lookahead buffering (which would add whole frames of latency).
// "static-thresh=100" lets the encoder skip macroblocks whose residual is below the
// threshold — screencast content is mostly static between frames, so this both speeds up
// encoding and saves bits. "token_partitions=3" splits the bitstream into 8 independent
// partitions so the encoder threads (SetThreadCount) can parallelise within one frame.
var realtimeOptions = new Dictionary<string, string>
{
    ["deadline"] = "realtime",
    ["cpu-used"] = "8",
    ["lag-in-frames"] = "0",
    ["static-thresh"] = "100",
    ["token_partitions"] = "3",
};
```

Notes for the implementer:

- These are FFmpeg libvpx-encoder AVOptions, passed through `FFmpegVideoEncoder`'s
  `encoderOptions` ctor parameter via `av_opt_set` on the codec's private options. Both option
  names are exactly as FFmpeg's libvpx wrapper spells them (`ffmpeg -h encoder=libvpx`):
  `static-thresh` (with hyphen), `token_partitions` (with underscore). Do not "fix" the
  inconsistent spelling.
- Do NOT add `row-mt` — that option is VP9-only; libvpx VP8 will reject/ignore it.
- If an option name is rejected at runtime, `FFmpegVideoEncoder` logs a warning (does not
  throw); check server logs for AVOption warnings after first frame.
- `static-thresh` tuning: 100 is the starting point. If slow fades/gradients visibly smear,
  try 50 then 0 (off); if quality is fine, 500 may be tried for more speed. Record the chosen
  value in this file when done.

**Verification:** same measurement procedure as Step 1. Additionally eyeball quality on:
(a) a page with a slow CSS fade animation, (b) static text page while moving the mouse cursor,
(c) a playing video. No visible smearing/ghosting allowed at threshold 100.

---

## Step 3 — Kill the RGB round-trip (custom MJPEG→I420 decode)

**Goal:** replace `decoder.DecodeFaster(...)` (JPEG→RGB24) + implicit RGB24→YUV420P in the
encoder with a direct MJPEG→I420 decode, so the encoder receives `VideoPixelFormatsEnum.I420`
and its input conversion becomes a same-format no-op-class pass.

**New file:** `src/RemoteBrowserIsolation.Server/Services/MjpegToI420Decoder.cs`
**Changed file:** `VideoTrackStreamer.cs` (swap the decode call; remove `FFmpegVideoEncoder decoder`).

### 3a. The decoder class — contract

```csharp
// Decodes MJPEG (JPEG) frames straight to a contiguous I420 buffer using FFmpeg.AutoGen,
// bypassing SIPSorceryMedia.FFmpeg's DecodeFaster (which force-converts to RGB24 and forces
// the VP8 encoder to convert back to YUV420P — two wasted full-frame sws_scale passes).
// Not thread-safe: owned and used by exactly one TranscodeLoopAsync, same lifetime discipline
// as the FFmpegVideoEncoder it feeds.
internal sealed unsafe class MjpegToI420Decoder : IDisposable
{
    // Decodes one JPEG image. Returns false if the packet produced no frame.
    // On success 'i420' points at an internal reusable buffer laid out as contiguous
    // I420 (Y plane, then U, then V, no row padding) valid until the next Decode call.
    public bool TryDecode(byte[] jpegBytes, out IntPtr i420, out int width, out int height);
    public void Dispose();
}
```

### 3b. The decoder class — implementation detail (FFmpeg.AutoGen 8.1.0, `using FFmpeg.AutoGen;`)

Project already references FFmpeg.AutoGen (`VideoTrackStreamer.cs` line 3) and FFmpeg natives
are initialised at startup (`Program.cs:130`, `FFmpegInit.Initialise(...)` binds
`ffmpeg.RootPath`). The csproj must have `<AllowUnsafeBlocks>true</AllowUnsafeBlocks>` — check
`RemoteBrowserIsolation.Server.csproj`; add it if missing.

Construction (once per transcode loop):

1. `AVCodec* codec = ffmpeg.avcodec_find_decoder(AVCodecID.AV_CODEC_ID_MJPEG);` — throw
   `InvalidOperationException` if null.
2. `_ctx = ffmpeg.avcodec_alloc_context3(codec);` then `ffmpeg.avcodec_open2(_ctx, codec, null)`
   — throw on negative return.
3. `_packet = ffmpeg.av_packet_alloc(); _frame = ffmpeg.av_frame_alloc();`

`TryDecode` per frame:

1. Pin `jpegBytes` (`fixed (byte* p = jpegBytes)`), point the packet at it:
   `_packet->data = p; _packet->size = jpegBytes.Length;` then
   `ffmpeg.avcodec_send_packet(_ctx, _packet)` and `ffmpeg.avcodec_receive_frame(_ctx, _frame)`
   — both inside the `fixed` block (MJPEG is intra-only: one packet in, one frame out,
   synchronously; no draining loop needed). Return false if receive_frame returns
   `EAGAIN`/negative.
2. The decoded `_frame->format` will be `AV_PIX_FMT_YUVJ420P` (full-range) for standard JPEGs.
   Defensive: if it is anything other than `AV_PIX_FMT_YUVJ420P` or `AV_PIX_FMT_YUV420P`
   (e.g. 4:2:2 JPEG — Chromium emits 4:2:0, but do not crash if not), fall back through ONE
   `sws_getCachedContext`/`sws_scale` to `AV_PIX_FMT_YUV420P`. Keep the cached context as a
   field; free with `ffmpeg.sws_freeContext` in Dispose.
3. **Range note (accepted tradeoff):** YUVJ420P is full-range, YUV420P is limited-range. We
   deliberately relabel rather than range-convert — one-time slight contrast shift, standard
   practice in real-time pipelines, invisible after VP8 quantisation. Do NOT add an extra
   sws_scale pass just for range; that would re-create the cost this step removes.
4. Copy the three planes into the reusable contiguous I420 buffer:
   - Buffer size: `width * height * 3 / 2` bytes, allocated once via
     `Marshal.AllocHGlobal` when dimensions first seen (realloc if dimensions change —
     they shouldn't mid-session, viewport is fixed).
   - Y: `height` rows of `width` bytes from `_frame->data[0]` with source stride
     `_frame->linesize[0]`; U then V: `height/2` rows of `width/2` bytes from
     `data[1]`/`data[2]` with `linesize[1]`/`linesize[2]`.
   - The row-by-row copy is REQUIRED because FFmpeg pads linesize (e.g. 1280-wide plane may
     have linesize 1312); `RawImage` has a single `Stride`/contiguous-sample model, so the
     destination must be tightly packed (`Stride = width` for the Y plane; the encoder derives
     U/V offsets from width/height for I420).
   - Use `Buffer.MemoryCopy` per row.
5. `ffmpeg.av_frame_unref(_frame);` before returning.

`Dispose`: `av_frame_free(&_frame)`, `av_packet_free(&_packet)`,
`avcodec_free_context(&_ctx)`, `sws_freeContext` if created, `Marshal.FreeHGlobal` the buffer.

### 3c. Wiring into `TranscodeLoopAsync`

Replace:

```csharp
using var decoder = new FFmpegVideoEncoder();
...
var rawFrames = decoder.DecodeFaster(AVCodecID.AV_CODEC_ID_MJPEG, jpegBytes, out _, out _);
if (rawFrames is not { Count: > 0 }) { ...warn+continue... }
...
var encoded = encoder.EncodeVideoFaster(rawFrames[0], VideoCodecsEnum.VP8);
```

with:

```csharp
using var decoder = new MjpegToI420Decoder();
...
if (!decoder.TryDecode(jpegBytes, out var i420Ptr, out var width, out var height))
{
    logger.LogWarning("JPEG decode produced no frame for {Url}", targetUrl);
    continue;
}

var rawImage = new RawImage
{
    Width = width,
    Height = height,
    Stride = width,                         // tightly packed Y plane
    Sample = i420Ptr,
    PixelFormat = VideoPixelFormatsEnum.I420,
};
var encoded = encoder.EncodeVideoFaster(rawImage, VideoCodecsEnum.VP8);
```

(Check whether `RawImage` is a class or struct with settable members in 10.0.11 — if members
are get-only, use whatever constructor it exposes. `GetBuffer()` is not needed; `Sample` +
`Stride` is the zero-copy path `EncodeVideoFaster` consumes.)

### 3d. Fallback / abort criteria

If `EncodeVideoFaster` with an I420 `RawImage` produces null/empty frames or a distorted image
(plane-order or stride mismatch — symptoms: green/pink tint, diagonal shear), first re-check
the row-copy stride math. If it genuinely cannot be made to work, REVERT Step 3 entirely
(keep Steps 1–2) and record the failure in this file; do not ship a half-working decode path.

**Verification:** same log-based measurement; expect a further ~3–8 ms/frame drop at 720p.
Visual check mandatory (correct colors, no shear) on a photo-heavy page, plus the Step 2
quality checks again. Also run one full session teardown (close client tab) and confirm no
crash and the `"Video stream ended"` log line appears — validates Dispose ordering.

---

## Step 4 (deferred) — Hardware encode + "Video encoder" admin setting

VAAPI/NVENC offload. 10x-class improvement but changes codec negotiation, the Docker image
(GPU passthrough, drivers) and client assumptions. Only start if Steps 1–3 measured
insufficient. Not specified to implementation depth here — requires its own plan when picked
up. Key design points already agreed:

### Admin setting: "Video encoder" (ships WITH Step 4, not before)

A settings-UI property to choose the encode path. Not a plain CPU/GPU toggle — a dropdown:

| Value | Behavior |
|-------|----------|
| `Auto` (default) | Probe hardware encoder availability at startup; use GPU if usable, else fall back to CPU and log which path was chosen. |
| `CPU` | Force software VP8 (libvpx). For debugging quality/compat issues ("is the GPU encoder producing artifacts?"). |
| `GPU` | Force hardware encode; if unavailable, fail loudly (log ERROR + surface in admin UI) instead of silently falling back — so a broken Docker GPU passthrough is visible, not hidden. |

Design points:

- **Why not a toggle:** GPU is often absent (no `--gpus`/`--device` passthrough, no drivers,
  no NVENC/VAAPI on the host). A bare "GPU: on" would break silently on such machines;
  `Auto` + forced modes covers both convenience and debuggability.
- **Applies per session, no restart:** the encoder pair is created per session in
  `TranscodeLoopAsync`, so the setting is read at session start. Store it in SQLite alongside
  the other admin-managed settings, same pattern as policies.
- **UI shows detected reality** next to the dropdown: "GPU: NVENC available" /
  "GPU: not detected (using CPU)" — admin sees the actual state, not just the wish.
- **Note:** `FFmpegVideoEncoder`'s ctor already accepts an `AVHWDeviceType` parameter — the
  probe/selection can likely be built on that rather than a new encoder class.

---

## Execution order & verification protocol

1. **Baseline first:** before touching code, run video mode on an animated page with Debug
   logging, collect ≥100 `Video frame` lines, compute median/p95 of the `in {Ms}ms` value.
   Save the numbers at the bottom of this file.
2. Step 1 → build → measure → commit (`perf(video): multithread VP8 encoder`).
3. Step 2 → build → measure + quality eyeball → commit
   (`perf(video): libvpx static-thresh + token partitions`).
4. Step 3 → build → measure + color/teardown checks → commit
   (`perf(video): decode MJPEG straight to I420, drop RGB round-trip`).
5. Docker: rebuild the image and re-run the measurement inside the container
   (**use container name `rbi-testing`, NEVER `rbi`** — see CLAUDE.md), since FFmpeg build
   and core count differ from the dev host.

Success criterion: median per-frame transcode time at 720p drops well under one frame period
at the screencast's natural rate (transcode stops being the bottleneck), with no visible
quality regression at JPEG quality 80.

## Measured results (2026-07-12, live server, 3 runs each, 720p)

Measured with `tests/e2e/measure_video_perf.sh` against a worst-case full-frame-animating local
test page (`tests/e2e/fixtures/animated.html` -- background hue changes every repaint, so
literally every pixel changes every frame). Baseline = master (`9919f71`) via a temporary
detached worktree; current = this branch with Steps 1-3 all applied together (not measured
incrementally per-step -- see caveat below).

| Run | Frames | Median ms/frame | p95 ms/frame |
|-----|--------|------------------|--------------|
| baseline 1 | 116 | 23.30 | 37.30 |
| baseline 2 | 110 | 22.90 | 40.70 |
| baseline 3 | 114 | 22.95 | 39.90 |
| current 1 | 110 | 23.05 | 40.00 |
| current 2 | 119 | 21.70 | 35.50 |
| current 3 | 119 | 21.10 | 36.50 |

**Result: ~5-10% median improvement (≈23ms → ≈21-23ms), not the multi-x win hoped for.**
16 cores available (`nproc`) on the measurement machine.

**Root cause of the modest gain, found during this measurement:**
- The `token_partitions` libvpx AVOption used in Step 2 **does not exist** in this FFmpeg
  build's libvpx wrapper -- confirmed both by `ffmpeg -h encoder=libvpx` (no such option listed)
  and by the server's own log: `Failed to set encoder option "token_partitions"="3", Skipping
  this option. Option not found`. It was silently ignored the whole time. Removed from the code
  (see commit after this measurement) since FFmpeg's libvpx encoder derives the token-partition
  count automatically from `thread_count` (clamped to `log2(thread_count)`, max 8 partitions) --
  so `SetThreadCount` alone was already doing the useful part; the option was dead weight.
- libvpx clamps automatic token partitions to a max of 8 (`log2` clamped 0-3), so encode
  parallelism within one frame tops out around 8 threads regardless of the 16 cores available --
  partly explains why the gain isn't larger even with threading working.
- **The test page is an adversarial worst case for Step 2.** `static-thresh` (skip
  unchanged macroblocks) gets essentially zero benefit here because the full-screen hue fill
  invalidates every macroblock every frame -- real screencast content (a web page with mostly
  static text/layout and small animated regions) should benefit far more from `static-thresh`
  than this synthetic test shows. This measurement has NOT been repeated against a
  mostly-static/typical page, so the true real-world improvement from Step 2 specifically is
  still unknown -- likely better than 5-10%, not confirmed.

**Caveat:** measured Steps 1-3 together, not incrementally (Step 1 alone / +Step 2 / +Step 3
rows were not filled in separately) -- if isolating each step's contribution is wanted later,
rerun `measure_video_perf.sh` against worktrees checked out at each intermediate commit.

**Follow-up measurement against a realistic mostly-static page** (`tests/e2e/fixtures/realistic_static.html`
-- static text/table/layout like a real web page, one small ~200x40px ticking-clock region,
closer to actual screencast content than the adversarial full-repaint test above):

| Run | Frames | Median ms/frame | p95 ms/frame |
|-----|--------|------------------|--------------|
| baseline 1 | 115 | 23.50 | 34.50 |
| baseline 2 | 110 | 22.30 | 37.70 |
| baseline 3 | 107 | 23.70 | 40.50 |
| current 1 | 122 | 19.80 | 37.00 |
| current 2 | 130 | 19.20 | 33.30 |
| current 3 | 122 | 19.40 | 33.80 |

**Result: ~16% median improvement (≈23.2ms → ≈19.5ms) on realistic content** -- better than the
worst-case full-motion test's ~5-10% (as expected, since `static-thresh` gets real macroblocks
to skip here), but still not a multi-x win. Baseline itself barely changes between the two test
pages (~23ms either way) since it has no content-adaptive optimization and always pays the full
RGB round-trip regardless of what's on screen.

**Conclusion re: Step 4 gate ("only start if Steps 1-3 measured insufficient"):** measured
result is a genuine but modest ~16% per-frame speedup on realistic content, ~5-10% on worst-case
full-motion content. This is real, not noise (consistent across 3 runs each), but it is not a
dramatic win. Whether that's "sufficient" is a product call, not a technical one -- flagging
back to the user rather than deciding unilaterally whether to proceed to Step 4 (GPU hardware
encode).

## Step 5 -- everyNthFrame (2026-07-12, done)

User asked about faster headless browsers (Firefox/WebKit) -- ruled out: CDP screencast
(`Page.startScreencast`/`Page.screencastFrame`) is Chromium/CDP-specific, Playwright doesn't
expose an equivalent for Firefox/WebKit, so switching engines would lose the entire frame
capture mechanism, not just be a speed tweak.

User then brought a set of CDP-tuning suggestions found online. Checked each against the actual
code:
- "Ack immediately" -- already done (`VideoTrackStreamer.cs`'s screencastFrame handler writes to
  the mailbox synchronously and acks right after, not gated on the transcode loop).
- "Lower maxWidth/maxHeight" -- doesn't apply; those are just an upper bound (4096) already above
  the real cap (viewport, max 1280x720), so there's no waste there to cut.
- "Lower JPEG quality" -- off the table per earlier constraint (quality 80 must stay).
- **`everyNthFrame`** -- real, unused knob. Implemented: `EveryNthFrame = 2` passed to
  `Page.startScreencast`, skipping every other repaint at the source (Chromium's own JPEG
  encode), instead of us receiving and discarding frames after the fact.
- `puppeteer-stream` (Node/Puppeteer library using Chromium's native tabCapture/MediaStream APIs
  instead of CDP JPEG screencast) -- not usable from this C#/Playwright/SIPSorcery stack, no
  .NET equivalent. Same underlying idea as bypassing CDP screencast entirely (see "smarter
  protocol" discussion below) -- confirms that's the real lever, just not available as a ready
  library here.

**Measured (3 runs each, 720p, vs master baseline):**

| Page | Baseline median | Steps 1-3 only | + everyNthFrame=2 |
|------|------------------|----------------|---------------------|
| Realistic mostly-static | ~23.2ms | ~19.5ms (16%) | ~16.5ms (**~29%**) |
| Full-motion animated | ~23.0ms | ~21.3ms (~7%) | ~17.2ms (**~25%**) |

Frame count also dropped (~115 -> ~82 frames per 20s window, as expected from skipping every
other frame). Notably, per-frame transcode ms *also* dropped, not just frame count -- likely
less CPU contention with Chromium's own JPEG-encode work competing for the same cores/cache,
rather than a change in per-frame algorithmic cost.

## Open question -- "smarter protocol" (partial/dirty-rect updates)

User asked whether a more advanced protocol (refresh only the changed screen region) would beat
the current full-frame JPEG approach. Analysis, not yet implemented:

- VP8 itself already does this at the *encode* level (inter-frame delta + `static-thresh`
  macroblock skip from Step 2) -- the bitstream is already proportional to how much changed.
- The real constraint is upstream: `Page.startScreencast` is Chromium's only public CDP API for
  this and it is full-frame-JPEG-only -- no dirty-rect/partial-region parameter exists in it.
  Chromium therefore still full-frame JPEG-encodes every repaint, and we still full-frame
  JPEG-decode every frame, regardless of how much of the page actually changed.
- A genuine "send only the changed region" architecture would mean bypassing CDP screencast
  entirely for some lower-level Chromium capture (native video encode via tabCapture-style APIs,
  akin to what `puppeteer-stream` does in Node) -- a real architecture change, not a tuning
  knob, and it's unconfirmed whether an equivalent is reachable from headless Chromium via
  Playwright/.NET at all.
- **Not started.** Recommended next step if pursued: a short, time-boxed research spike to
  confirm whether such a capture path is actually reachable before committing to a rewrite --
  explicitly flagged as a decision for the user, not decided here.

## Implementation notes (2026-07-12)

Steps 1-3 implemented on branch/worktree `video_codeing_enhancement`. `dotnet build` clean,
0 warnings.

- Live per-frame ms measurement via the actual video-mode WebRTC flow was not exercised in this
  session (needs a real WebRTC signaling client; no Playwright/node runtime available in the
  sandbox). Instead, Step 3's correctness (the highest-risk step -- plane-copy/stride math) was
  verified with a standalone decode->encode->decode round trip:
  1. Generated a synthetic 320x180 4:2:0 JPEG with `ffmpeg -f lavfi -i testsrc=... -pix_fmt yuvj420p`
     (matches Chromium's actual screencast subsampling -- an earlier attempt without forcing
     `-pix_fmt yuvj420p` produced 4:4:4 JPEG and correctly triggered the decoder's defensive
     format check, confirming that check works).
  2. Fed it through `MjpegToI420Decoder.TryDecode` -> `FFmpegVideoEncoder.EncodeVideoFaster`
     (same encoder options as Steps 1-2) in a throwaway console project referencing the real
     `MjpegToI420Decoder.cs`.
  3. Wrapped the resulting VP8 bytes in a minimal IVF container and decoded back to PNG via
     `ffmpeg -i out.ivf out.png`.
  4. Result: correct SMPTE color bars, no shear, no plane misalignment, no green/pink tint --
     confirms the row-by-row plane copy (Y/U/V, linesize-vs-tightly-packed) and the I420
     `RawImage` wiring are correct.
- Still needed before merge: run the actual dev server (`./startRBI_dev.sh`) with
  `Logging:LogLevel:Default=Debug`, drive a real video-mode session end to end (browser or
  Playwright-based client) to populate the measured-results table above and confirm no
  regression under the real CDP screencast frame cadence, then do the Docker re-measure
  (`rbi-testing`, per CLAUDE.md).
