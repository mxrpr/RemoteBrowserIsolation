# 12 — VAAPI hardware video encode (Go backend) — IMPLEMENTATION PLAN

Goal: move the per-session video encode off the CPU onto the Intel iGPU (VAAPI / QuickSync),
raising concurrent-session capacity on the current host. Scope is **the Go backend only**
(`src/rbi-go`); the legacy C# project is untouched.

This plan is written to be executed step by step by another model without further research.
It targets the pipeline as it exists after the framerate-cap change (plan `11_video_pipeline_speedup`
covers the *C#* history; this is the Go continuation of its deferred "Step 4 — hardware encode").

---

## Why

Per video session, the Go pipeline runs three CPU stages (see
`src/rbi-go/internal/video/pipeline.go` `transcodeLoop`):

```
CDP screencast JPEG (quality 80, everyNthFrame=2)
  -> libjpeg-turbo decode to packed I420   (internal/video/decoder_turbojpeg.go)
  -> libvpx VP8 encode, cpu-used=8, 1 thread (internal/video/encoder_vpx.go)
  -> pion TrackLocalStaticSample.WriteSample (VP8 over RTP)
```

Measured reality on this host: **16 cores, ~7 concurrent sessions before CPU saturation** ≈ 2.3
cores/session. The software libvpx VP8 encode (`g_threads=1`, one core) is ~1 of those cores and
is pure CPU work the iGPU can do for free.

Host capability (probed 2026-07-19):
- CPU: 16 cores, 31 GB RAM.
- GPU: **Intel Raptor Lake-P Iris Xe**, VAAPI render node present at `/dev/dri/renderD128`.
- No NVIDIA.

Iris Xe (Gen12) fixed-function media engine supports **hardware encode of H.264, HEVC, VP9, AV1**
and **hardware decode of MJPEG**. It does **NOT** support VP8 *encode* (VP8 encode was dropped from
QuickSync after older gens). Therefore hardware encode requires **changing the WebRTC codec off
VP8**. That is the central consequence this plan is built around.

Expected win: offloading the encode frees ~1 core/session → target **~2× session density**
(rough, to be measured). The GPU is ~idle today, so headroom is real.

---

## Decision: codec, and why H.264

| Option | Verdict |
|--------|---------|
| **H.264 (`h264_vaapi`)** | **Chosen.** Most mature VAAPI encoder on Intel, lowest realtime latency, universal WebRTC browser support. Use Constrained Baseline profile (WebRTC-friendly, no B-frames → no reorder latency). pion has a built-in H.264 RTP payloader (`MimeTypeH264`). |
| VP9 (`vp9_vaapi`) | Works on Iris Xe, pion supports `MimeTypeVP9`, but VAAPI VP9 realtime latency/quality is less proven than H.264 and buys nothing over H.264 here. Reject for phase 1. |
| AV1 (`av1_vaapi`) | Too new; realtime tuning immature; browser AV1-RTP negotiation inconsistent. Reject. |
| Keep VP8 | Impossible on this GPU (no HW VP8 encode). |

**Locked: hardware path uses H.264 Constrained Baseline.** Software fallback stays VP8 (existing
libvpx path, unchanged).

---

## Decision: how to reach VAAPI from Go

| Option | Verdict |
|--------|---------|
| **cgo → libavcodec (`h264_vaapi`) directly** | **Chosen.** Consistent with the existing cgo backends (`encoder_vpx.go`, `decoder_turbojpeg.go`) — same build-tag discipline, same "one C-heap encoder per session owned by one goroutine" contract. No process/pipe overhead. Feed I420, get Annex-B NAL units back. |
| ffmpeg subprocess per session (raw I420 in stdin, H.264 out stdout) | Lower cgo effort, but adds a process + two pipes per session, an extra copy each way, and a second process-lifecycle to babysit alongside Chrome. Rejected as the primary path; keep in back pocket only if the cgo VAAPI wiring proves unstable. |
| Pure-Go / other bindings | No maintained pure-Go VAAPI H.264 encoder exists. Reject. |

**Locked: cgo + libavcodec VAAPI**, new file `internal/video/encoder_vaapi.go` behind a build tag,
implementing the **existing `VP8Encoder` interface's sibling** (see Step 2 for the interface
refactor). Keep the C surface tiny (create / encode-one-frame / destroy), mirroring
`encoder_vpx.go`'s `vpxEncCreate` / `vpxEncodeFrame` / `vpxEncDestroy`.

---

## Decision: pixel format & the input to the GPU

VAAPI encoders consume **NV12** surfaces, not I420. Two sub-decisions:

1. **I420 → NV12 conversion is cheap and stays on CPU for phase 1.** NV12 = same Y plane as I420,
   with U and V interleaved into one plane. It is a byte-interleave of the two chroma planes —
   trivial cost vs. the encode it replaces. Do it in C inside the encode wrapper (or let
   libavcodec/`vaapi` upload filter do it). Do **not** add an sws_scale RGB detour.
2. **Upload to a VAAPI hwframe** happens inside libavcodec via an `AV_HWDEVICE_TYPE_VAAPI` device +
   an NV12 hwframe context. The encoder's `pix_fmt` is `AV_PIX_FMT_VAAPI`; frames are uploaded with
   `av_hwframe_transfer_data` from a software NV12 `AVFrame`. Standard libavcodec VAAPI encode
   boilerplate — replicate it in C, not Go.

**Phase 2 (optional, deferred): keep pixels on the GPU end-to-end.** Iris Xe also HW-decodes MJPEG.
A full `mjpeg` VAAPI-decode → (surface stays in VRAM) → `h264_vaapi` encode path removes the
libjpeg-turbo CPU decode *and* the CPU NV12 pack *and* the upload copy — zero CPU pixel work per
frame. Larger effort (VAAPI decode context, surface sharing between decode/encode). Not specified to
implementation depth here; gate on phase-1 measurement.

---

## Decision: encoder selection (Auto / CPU / GPU)

Mirror the model already agreed for the C# backend (plan 11 Step 4): a runtime-selectable mode, not
a bare toggle, because the GPU is absent on many hosts/containers.

| Mode | Behavior |
|------|----------|
| `Auto` (default) | Probe VAAPI at startup. If a usable `h264_vaapi` encoder initialises, use GPU; else fall back to software VP8 and log which path was chosen. |
| `Cpu` | Force software VP8 (existing path). For debugging GPU quality/compat. |
| `Gpu` | Force VAAPI H.264. If unavailable, **fail the session loudly** (log ERROR, close the peer connection) rather than silently degrading — so broken container GPU passthrough is visible. |

Storage: the Go backend already persists admin settings in SQLite (`internal/db`, `internal/policy`
patterns). Add one settings row. Read the mode at **session start** in `startRender`
(`cmd/server/session.go`), so no restart is needed to change it.

Critical wrinkle the C# plan hit and this must respect: **the codec is chosen per session, but the
pion video track's codec is fixed when the WebRTC answer is negotiated** (`internal/webrtc/manager.go`,
`AddTransceiverFromTrack`, well before `startRender` runs). So the mode decision must be available at
`CreateSession` (answer) time, not deferred to `startRender`. See Step 3.

---

## Repo conventions the implementer MUST follow

- `CLAUDE.md`: every new type/func/method gets a comment describing its function.
- cgo files use the **same build tags as the existing backends**: `//go:build vpx && cgo`. Decide
  whether VAAPI shares the `vpx` tag or gets its own (`vaapi`); recommendation: reuse a single
  production tag so one Docker build produces one binary with both software and hardware paths
  compiled in, selection at runtime. If a separate tag is chosen, the Dockerfile and
  `scripts/run_docker_go.sh` must pass it.
- Stub builds must still compile: provide a `encoder_vaapi_stub.go` (no cgo) that reports
  "VAAPI unavailable" so `go build ./...` without tags keeps working (matches
  `encoder_stub.go` / `decoder_stub.go`).
- No lowering of `screencastQuality` (80) — same constraint carried from plan 11.
- Delegate builds/tests to the `test-runner` agent; git to `git-runner` (see global CLAUDE.md).

---

## Step 0 — Baseline measurement (before any code)

There is no per-frame perf harness in `rbi-go` yet equivalent to the C#
`measure_video_perf.sh`. Capacity, not per-frame ms, is the metric that matters here.

1. Use `scripts/loadtest/` (present in the tree) or extend it to open N concurrent video sessions
   against a fixed animated test URL.
2. Record: max stable concurrent sessions before frames stall / CPU pins (baseline ≈ 7 per the
   user's report), plus `nproc`, and GPU utilisation (`intel_gpu_top`, package `intel-gpu-tools`)
   showing the render/video engine ~idle at baseline.
3. Save numbers at the bottom of this file. Everything after is measured against this.

---

## Step 1 — Add the VAAPI H.264 encoder (cgo)

**New file:** `src/rbi-go/internal/video/encoder_vaapi.go` (build-tagged, cgo).
**New file:** `src/rbi-go/internal/video/encoder_vaapi_stub.go` (no-cgo stub).

C surface (keep minimal, mirror `encoder_vpx.go`):

```c
// Creates a VAAPI H.264 encoder: opens AV_HWDEVICE_TYPE_VAAPI on /dev/dri/renderD128,
// builds an NV12 hwframe pool sized width x height, finds/opens the h264_vaapi encoder
// with Constrained Baseline, realtime/low-latency options. Returns NULL + errBuf on failure.
void* vaapiEncCreate(int width, int height, int bitrate_kbps, const char* renderNode,
                     char* errBuf, int errBufLen);

// Encodes one packed-I420 frame: pack I420->NV12 into a software AVFrame, av_hwframe_transfer_data
// upload to a VAAPI surface, send/receive one packet, copy Annex-B bytes to outBuf.
// forceKeyFrame => set AV_PKT_FLAG-relevant pict_type = AV_PICTURE_TYPE_I. Returns byte count or -1.
int vaapiEncodeFrame(void* enc, const uint8_t* i420, int width, int height,
                     int forceKeyFrame, uint8_t* outBuf, int outCap, char* errBuf, int errBufLen);

void vaapiEncDestroy(void* enc);
```

Encoder options (set via `av_opt_set` / `AVCodecContext`):
- `profile = constrained_baseline`, `level` left default.
- `bf = 0` (no B-frames — zero reorder latency, required for realtime WebRTC).
- `g` (GOP) large; keyframes driven explicitly by `forceKeyFrame` like the VP8 path
  (`keyframeInterval` in `pipeline.go` already forces one every 5 s).
- `rc_mode = CBR`, `b = bitrate` (start at the same 3000 kbps as `vpxTargetBitrateKbps`).
- Low-latency: `async_depth = 1`, no lookahead.
- Output **Annex-B** byte-stream NAL units (pion's H.264 payloader expects Annex-B start codes).

Notes:
- Reuse the C-heap out buffer + err buffer pattern from `vpxEncoder` (no malloc/free per frame).
- One encoder instance per session, owned by one `transcodeLoop` goroutine — same non-thread-safe
  contract as `vpxEncoder`. Document it in the type comment.
- I420→NV12: Y plane copies verbatim (`w*h` bytes); interleave U and V (`(w/2)*(h/2)` each) into the
  NV12 chroma plane. Trivial C loop; no sws needed.

**Verification:** unit-test round-trips a synthetic I420 frame through `vaapiEncCreate` /
`vaapiEncodeFrame` / `vaapiEncDestroy`, asserts non-empty Annex-B output with a leading start code
and an IDR NAL on the forced-keyframe frame. Run only under the cgo/GPU build (skip on stub).

---

## Step 2 — Generalise the encoder interface

**File:** `src/rbi-go/internal/video/encoder.go` (the `VP8Encoder` interface + factory).

Currently `NewVP8Encoder(width, height)` returns a `VP8Encoder`. Refactor to a codec-agnostic
encoder so `transcodeLoop` doesn't care which backend it holds:

- Rename the interface to `VideoEncoder` with the same `Encode(i420, w, h, forceKey) ([]byte, error)`
  and `Close()` methods (the byte slice is opaque encoded output — VP8 or H.264 Annex-B; the loop
  never inspects it, it just hands it to `WriteSample`).
- Add a factory `NewVideoEncoder(codec Codec, width, height int) (VideoEncoder, error)` where
  `Codec` is a small enum (`CodecVP8`, `CodecH264`). Keep `NewVP8Encoder` as a thin wrapper or
  inline its callers.

**File:** `internal/video/pipeline.go` — `StartPipeline` / `transcodeLoop` take the chosen `Codec`
(threaded down from `startRender`) and call `NewVideoEncoder`. No other loop logic changes: the
mailbox, framerate cap, keyframe cadence, dimension guard, and `WriteSample` are codec-independent.

**Verification:** `go build ./...` (stub) + `go vet` + existing `internal/video` tests pass. VP8 path
behaviour unchanged (regression check: existing encoder tests still green).

---

## Step 3 — Select the codec at WebRTC-answer time

**File:** `src/rbi-go/internal/webrtc/manager.go` (`CreateSession`, the track creation at
lines ~149-166) and its caller `cmd/server/session.go`.

The pion track codec is fixed at answer time, so the encode-mode decision must be resolved **before**
`AddTransceiverFromTrack`:

1. Add a startup **VAAPI probe** (`internal/video/probe.go`): attempt `vaapiEncCreate` once with a
   tiny frame; cache the boolean for process lifetime. Under the stub build it always returns false.
2. Read the admin encoder-mode setting (Step 4) + probe result to pick the effective codec:
   - `Cpu` → VP8. `Gpu` → H.264 (or hard-fail if probe false). `Auto` → H.264 if probe true else VP8.
3. `CreateSession` builds the track with `MimeTypeVP8` **or** `MimeTypeH264`
   (`RTPCodecCapability{MimeType: pion.MimeTypeH264, SDPFmtpLine: "level-asymmetry-allowed=1;packetization-mode=1;profile-level-id=42e01f"}`
   for Constrained Baseline). `RegisterDefaultCodecs()` already registers H.264 in the MediaEngine,
   so negotiation with the browser's H.264 offer works with no extra codec registration.
4. Thread the chosen `Codec` from `CreateSession` into the returned `Session`, and from there into
   `StartPipeline` (Step 2) so encoder and track codec always match.

Failure mode for `Gpu`-with-no-GPU: reject the offer (or close the PC in `manageRenderingSession`)
with a clear logged error, matching the C# "fail loudly" decision. Do not silently answer VP8.

**Verification:** with mode `Auto` on the GPU host, the negotiated answer SDP's video m-line shows
H.264 (`a=rtpmap:... H264/90000`); with `Cpu`, it shows VP8. Confirm a real browser session renders
in each mode. `Gpu` on a non-GPU build/container fails the session with the expected log line.

---

## Step 4 — Admin "Video encoder" setting (Auto/Cpu/Gpu)

Follow the existing Go admin-settings pattern (look at how `internal/policy` / the admin endpoints
persist and expose rows; reuse that shape — do not invent a new storage mechanism):

- One settings row: `video_encoder_mode` ∈ {`auto`,`cpu`,`gpu`}, default `auto`.
- `GET`/`PUT /api/admin/settings/video-encoder` (admin-authenticated, same middleware as other admin
  endpoints).
- Admin UI (`src/RemoteBrowserIsolation.Server/wwwroot/admin/…` if the Go server serves the same
  assets, or the Go server's own admin page — check which the Go backend actually serves): a
  `<select>` for the mode + a live status line showing the probe result
  ("GPU: VAAPI H.264 available" / "GPU: not detected — using CPU"), mirroring the Root CA section's
  "show detected reality" convention.
- Read the setting at session start (Step 3), cached with invalidation on `PUT`.

**Verification:** `GET`/`PUT` round-trip via curl; UI status line reflects the live probe; changing
the mode changes the negotiated codec on the next session with no server restart.

---

## Step 5 — Docker & runtime plumbing

**Files:** `scripts/run_docker_go.sh`, the Go Dockerfile.

- Install VAAPI runtime + Intel media driver in the image:
  `libva2`, `va-driver-all` (or the newer `intel-media-va-driver-non-free` for Iris Xe /
  `iHD` driver), `libavcodec`/`libavutil`/`libavformat` dev headers to build against, and
  `intel-gpu-tools` (for `intel_gpu_top` diagnostics). Confirm exact package names for the base
  image's distro.
- Pass the render node into the container: `--device /dev/dri/renderD128` (and ensure the container
  user is in the `render`/`video` group, or run with the right `--group-add`).
- Set `LIBVA_DRIVER_NAME=iHD` for Iris Xe.
- Keep the cgo build tag consistent so the shipped binary has the VAAPI path compiled in.
- **Test container name `rbi-testing`, NEVER `rbi`** (CLAUDE.md), and different host ports if the
  user's `rbi` is running.

**Verification:** inside `rbi-testing`, startup log shows the probe found VAAPI; `intel_gpu_top`
shows the Video/Render engine busy during a session; a real video-mode session renders correctly in
H.264.

---

## Step 6 — Capacity re-measure (the actual success gate)

Repeat Step 0's load test with mode `Auto` (GPU active):

- Record new max stable concurrent sessions and GPU engine utilisation at that point.
- Success criterion: **materially more concurrent sessions than the ~7 baseline** with acceptable
  latency and no visible H.264 artifacts at 3000 kbps / 720p. If the GPU's fixed-function encoder
  becomes the new ceiling (single media engine shared across sessions), record the saturation point
  — that is the real hardware limit and informs whether phase 2 (GPU JPEG decode) or a second
  encode tier is worth pursuing.

Save results at the bottom of this file.

---

## Execution order & commit granularity

1. Step 0 baseline (no code) → record numbers.
2. Step 1 encoder + stub → build (stub + cgo) → unit test → commit
   (`feat(video): VAAPI H.264 encoder backend`).
3. Step 2 interface generalisation → build/vet/test → commit
   (`refactor(video): codec-agnostic VideoEncoder interface`).
4. Step 3 codec selection at answer time → commit
   (`feat(video): negotiate H.264 track when GPU encode selected`).
5. Step 4 admin setting → commit (`feat(admin): video-encoder Auto/Cpu/Gpu setting`).
6. Step 5 Docker → commit (`build(docker): VAAPI runtime + render-node passthrough`).
7. Step 6 measure → record → decide on phase 2.

Each step independently revertable. VP8 software path remains the guaranteed fallback throughout, so
no step can leave video mode non-functional on a host without a GPU.

---

## Deferred — phase 2 (full GPU pipeline, MJPEG HW-decode)

Only if phase-1 measurement shows CPU JPEG decode / NV12 pack is now the dominant remaining
per-session cost. Adds a VAAPI `mjpeg` decode context feeding surfaces straight into the H.264
encoder, eliminating libjpeg-turbo and the CPU→GPU upload. Its own plan when picked up.

## Open risks / things to confirm during Step 1

- Iris Xe `h264_vaapi` realtime latency under N concurrent encode contexts sharing one media engine —
  unknown until measured; the single fixed-function encoder may serialise and cap density lower than
  "1 core freed × N" predicts.
- Annex-B vs AVCC output from `h264_vaapi` — verify pion's H.264 payloader gets start-code framing;
  add a bitstream-filter (`h264_mp4toannexb`) only if the encoder emits length-prefixed NALs.
- Constrained Baseline profile-level-id string must match what the browser offers, or negotiation
  drops H.264 and (with `RegisterDefaultCodecs`) may silently fall to VP8 with no VP8 encoder wired —
  Step 3's "codec and encoder must match" invariant guards this; test the mismatch path.

## Implementation status (2026-07-19)

Implemented on the Go backend; committed alongside the latent-bug fix below.

- **Latent bug fixed first (independent of VAAPI):** `pipeline.go` had
  `screencastEveryNthFrame = 2`, which — exactly as the C# plan 11 found — makes CDP emit
  **zero** frames for a page that paints once and never repaints (its only paint can land on a
  dropped count), giving a permanently blank/frozen client stream. Removed the source-side throttle
  entirely; frame-rate limiting is now done only downstream by `transcodeLoop`'s `minFrameInterval`
  gate, which guarantees the first frame always passes (`lastEncodeAt` starts at the zero time).
- **Step 1 (VAAPI H.264 encoder):** `internal/video/encoder_vaapi.go` (cgo, `//go:build vpx && cgo`)
  + stub additions in `encoder_stub.go`. **Compiles against real libavcodec 5.1 / VAAPI headers** —
  verified by building the Dockerfile.go `build` stage. Uses `FF_PROFILE_H264_CONSTRAINED_BASELINE`
  (ffmpeg 5.1 spelling; `AV_PROFILE_*` is 6.0+). **The actual GPU encode + browser playback is NOT
  yet verified** — needs a real session on the Iris Xe host (Step 6).
- **Step 2 (codec-agnostic interface):** `VP8Encoder`→`VideoEncoder`, added `Codec` enum +
  `NewVideoEncoder` factory. VP8 path behaviour unchanged; stub build + all Go tests green.
- **Step 3 (codec selection at answer time):** `resolveVideoCodec` in `cmd/server/session.go`
  (Cpu→VP8, Gpu→H264-or-loud-fail, Auto→H264-if-`video.H264Available()`-else-VP8), threaded through
  `CreateSession` (sets pion track `MimeTypeVP8`/`MimeTypeH264`+fmtp) and `StartPipeline`. Client
  needs no change: its `addTransceiver('video',{recvonly})` already offers H264.
- **Step 4 (admin mode + probe + UI):** already existed in the Go backend (`internal/settings`,
  `cmd/server/settings.go`, admin `index.html`); it was previously **not consumed** by the render
  path — Step 3 now consumes it. Note: the admin UI's "detected GPU" line uses the shell-based
  `settings.ProbeGpu` (render-node presence); actual codec selection uses the stronger
  `video.H264Available` (really constructs a probe encoder), so the two can disagree if the node
  exists but the encoder can't init — acceptable (UI is advisory).
- **Step 5 (Docker):** `Dockerfile.go` adds `libavcodec-dev`/`libavutil-dev` (build) and
  `libavcodec59 libavutil57 libva2 intel-media-va-driver-non-free vainfo` + `LIBVA_DRIVER_NAME=iHD`
  (runtime, non-free component enabled); `run_docker_go.sh` passes `--device /dev/dri/renderD128`
  when present. **Full runtime image + GPU run NOT executed here.**

### Still to do (needs the GPU host / running container — could not be done in this environment)

- Build the full `Dockerfile.go` image and run it as `rbi-go-testing` (NOT `rbi-go`) with the render
  node, confirm startup + a real video-mode session renders in H.264 (`vainfo` shows an H264 encode
  entrypoint; server log line `codec=H264`).
- Verify the h264_vaapi output framing is Annex-B as pion's payloader expects; if the encoder emits
  length-prefixed NALs instead, insert an `h264_mp4toannexb` bitstream filter in `encoder_vaapi.go`.
- Steps 0 + 6 capacity measurement (baseline ~7 vs GPU), watching whether the single fixed-function
  media engine serialises under N concurrent encode contexts.

## Measured results (2026-07-19, real GPU host, `rbi-go-testing` container)

**End-to-end functional verification: PASSED.** Built the full `Dockerfile.go` image fresh,
ran as `rbi-go-testing` (not `rbi-go`) with `--device /dev/dri/renderD128`, `--network host`,
separate `./data-testing` DB. GPU probe: `AV_HWDEVICE_TYPE_VAAPI available (/dev/dri/renderD128)`.
Single real session against `https://index.hu` in `Auto` mode: server log confirms `codec=H264`,
client received a real playable video track (1280×672, TTFF 3.15s, 8.9fps sustained over 8s hold) —
the VAAPI cgo encoder genuinely encodes and the browser genuinely decodes/plays H.264 from pion.

**6-session clean comparison** (`https://index.hu`, 15s hold, 500ms ramp, forced `Cpu` vs forced
`Gpu` mode, both runs 6/6 succeeded):

| Mode | Sustained fps (min/avg/max) | Container CPU% (avg/max) | Container Mem (avg/max) |
|------|------------------------------|---------------------------|--------------------------|
| Cpu (VP8, software) | 2.4 / 3.0 / 4.1 | 630.9% / 1330.5% | 2959 / 3727 MiB |
| Gpu (H.264, VAAPI) | 4.9 / 5.3 / 5.7 | 650.9% / 1293.4% | 3137 / 3955 MiB |

**~77% higher average sustained fps (5.3 vs 3.0) at essentially identical CPU usage** (avg/max
both ~650%/~1300% — within noise of each other). This is real evidence the encode moved off the
CPU: at the same total CPU budget, GPU mode delivers meaningfully smoother video instead of a
smaller session count freeing up cores, because at 6 concurrent sessions Chrome's own rendering
cost (not encode) is already the CPU-bound resource — freed encode cycles get reabsorbed by
rendering/compositing rather than showing up as idle CPU.

### The -32000 "Not attached to an active page" failures — ROOT-CAUSED AND FIXED (2026-07-19)

8-session runs (both codecs) initially hit 1-2/8 failures logged as
`session: start render failed ... err="... Not attached to an active page (-32000)"`. Investigated
with CDP-level diagnostics added to `internal/browser/manager.go` (Inspector.targetCrashed,
Inspector.detached, Target.targetDestroyed, Target.targetCreated listeners + `chromedp.WithErrorf`
routed to slog):

- **NOT a Chrome-launch race, NOT a renderer crash, NOT codec/VAAPI-related.** Across repeated
  failing runs, **zero** crash/detach/destroy/new-target events fired for the failing session — the
  attached page target is alive and never swapped.
- **Actual cause: a transient "page not active" window during navigation commit.** Under concurrent
  startup, `Page.startScreencast` is occasionally dispatched in the brief instant the target is
  swapping to the freshly-navigated document — after `DOMContentLoaded` fired but before commit
  settles — and Chrome momentarily reports no active page. Self-recovering, hence intermittent.
- **Fix:** bounded retry of `Page.startScreencast` on exactly that error string (4 retries ×150ms)
  in `internal/video/pipeline.go` `startScreencastWithRetry`; any other error stays terminal.
- **Validated:** three 10-session runs post-fix → `screencast started after retry` fired and
  recovered those sessions; **0 unrecovered -32000** across all runs. 10 concurrent sessions now
  run 9-10/10 success at ~9fps avg — past the original ~7-session ceiling.

**Remaining occasional failures are external, not a code defect:** `net::ERR_CONNECTION_RESET`
navigating to `https://index.hu` — the real site resets some connections when 10 fresh headless
browsers hit it simultaneously (a load-test artifact; real users hit different sites, not 10 bots
on one host). `CreateSession` correctly reports it as a navigation failure. An optional
navigation-level retry could paper over it but risks masking genuine navigation errors — left as a
deliberate non-fix.

**Conclusion vs the "Open risks" question (single fixed-function media engine serializing
concurrent encode contexts):** at 10 concurrent H.264 sessions the iGPU media engine kept up
(no encode stalls, ~9fps sustained), so no evidence of encoder serialization at this scale on
Iris Xe. Higher counts blocked mainly by the external site connection-resets, not by the encoder.

### Static-page blank (`frames=0`) — ROOT-CAUSED AND FIXED (2026-07-19)

Testing `http://example.com` (a page that paints once and is then completely idle) surfaced a
separate, pre-existing bug: the session connected and `render started`, but `frames=0` for the
whole session — permanently blank client. This is NOT the `everyNthFrame` bug (already removed) and
reproduces even with every frame requested.

- **Cause:** CDP `Page.startScreencast` only emits a frame on a **compositor commit (repaint)**. A
  page that finished its initial paint *before* screencast was enabled, and then never repaints,
  produces no commit → no screencast frame ever. Dynamic pages (index.hu, anything animating)
  repaint continuously so never hit this; static pages stay blank.
- **Fix:** `forceInitialFrame` in `pipeline.go` — after screencast starts, briefly resize the
  emulated viewport (`h → h+2 → h`) to force compositor commits. Re-applying the *same* viewport is
  a Chrome no-op (verified — it produced 0 frames), so a real size change is required; the transient
  `h+2` frame is dropped by transcodeLoop's frame-size guard, the restored-size frame passes. Gated
  by an `atomic.Bool` (`firstFrame`) set by transcodeLoop on the first delivered frame, so it stops
  nudging the instant frames flow — dynamic pages get at most one harmless nudge, usually none.
- **Validated:** `http://example.com` single session now succeeds — `frames=1`, TTFF 2926ms, client
  renders the page (the first frame is inherently an IDR keyframe, so it decodes). fps then 0, which
  is correct: a static page has nothing new to send; the client holds the fully-rendered frame, and
  any later real change or user interaction repaints and streams normally.

**Environment note:** Iris Xe VAAPI encoder correctly resolves both `Auto` (probes and picks H264)
and `Gpu` (forces H264) modes; `Cpu` mode correctly forces VP8. `resolveVideoCodec`'s DB-mode →
codec → pion-track-codec → pipeline-encoder wiring all confirmed correct end-to-end by the server
log's `codec=` field matching what was requested via the admin API.
