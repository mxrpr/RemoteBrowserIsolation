# Two-stage Go build. Unlike the C# Dockerfile (which relies on a host-side
# `dotnet publish` before `docker build` to avoid slow in-image restores), Go's
# module download + compile is fast enough to do entirely inside the build stage
# with good layer caching -- no separate compile.sh step is needed.

# ── Build stage ──────────────────────────────────────────────────────────────
FROM golang:1.26-bookworm AS build

# libvpx-dev: cgo headers needed at compile time for -tags vpx (encoder_vpx.go).
# libturbojpeg0-dev: cgo headers for the -tags vpx JPEG decoder
# (decoder_turbojpeg.go).
# libavcodec-dev + libavutil-dev: cgo headers for the -tags vpx VAAPI H.264
# encoder (encoder_vaapi.go), which links -lavcodec -lavutil.
# The runtime .so's are installed separately in the final stage; only headers
# are needed here, but these -dev packages also pull in the runtime libs which
# doesn't hurt.
RUN apt-get update && apt-get install -y --no-install-recommends \
        libvpx-dev libturbojpeg0-dev libavcodec-dev libavutil-dev \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /src

# Copy go.mod + go.sum first so module downloads are cached independently of
# source changes (standard Go Docker layer-caching pattern).
COPY src/rbi-go/go.mod src/rbi-go/go.sum ./
RUN go mod download

COPY src/rbi-go/ .

# -tags vpx: compile the real libvpx VP8 encoder (encoder_vpx.go) instead of
# the stub. Required for video mode to produce actual VP8 output.
RUN go build -tags vpx -o /out/server ./cmd/server/

# ── Runtime stage ─────────────────────────────────────────────────────────────
FROM debian:bookworm-slim

# libvpx7: runtime shared library for the VP8 encoder compiled with -tags vpx.
# libturbojpeg0: runtime shared library for the JPEG decoder compiled with
#   -tags vpx (decoder_turbojpeg.go).
# libavcodec59 + libavutil57: runtime shared libraries for the VAAPI H.264
#   encoder (encoder_vaapi.go).
# libva2: VAAPI client runtime. intel-media-va-driver-non-free: the Intel "iHD"
#   driver required for Iris Xe (Gen12) hardware H.264 encode — the older i965
#   driver does not cover it. It lives in Debian's non-free component, enabled
#   just below. vainfo: diagnostics (`vainfo` lists the driver's encode entrypoints).
# chromium: headless Chromium binary for video-mode CDP sessions via chromedp.
#   chromedp auto-detects "chromium" in PATH — no RBI_BROWSER_CHROMIUM_PATH override needed.
# ca-certificates: needed for TLS verification in outbound proxy connections.
# --no-install-recommends: avoids pulling in the full X11/desktop stack that
#   the chromium package recommends but that headless operation does not require.
#
# The sed enables the non-free + contrib components in Bookworm's deb822 sources
# so intel-media-va-driver-non-free is installable.
RUN sed -i 's/^Components: main$/Components: main contrib non-free non-free-firmware/' \
        /etc/apt/sources.list.d/debian.sources \
    && apt-get update && apt-get install -y --no-install-recommends \
        libvpx7 libturbojpeg0 libavcodec59 libavutil57 \
        libva2 intel-media-va-driver-non-free vainfo \
        chromium ca-certificates \
    && rm -rf /var/lib/apt/lists/*

# LIBVA_DRIVER_NAME=iHD selects the Intel media driver installed above (Iris Xe).
# The VAAPI render node itself must be passed into the container at run time:
#   docker run --device /dev/dri/renderD128 ...
# (added in run_docker_go.sh). Without the device, H264Available() returns false
# and the pipeline falls back to software VP8 in Auto mode.
ENV LIBVA_DRIVER_NAME=iHD

COPY --from=build /out/server /app/server

# wwwroot: rbi-go has no separate static-file tree — in dev it uses the C#
# project's wwwroot via a relative path. In Docker we copy that same directory
# into the image and point RBI_WWWROOT at its absolute location.
COPY src/RemoteBrowserIsolation.Server/wwwroot /app/wwwroot

# RBI_WWWROOT: absolute path so resolveWwwRoot skips the relative-path fallback.
ENV RBI_WWWROOT=/app/wwwroot

# RBI_DB_PATH: SQLite file inside the bind-mounted /app/data volume so the DB
# survives image rebuilds. Uses a separate filename (rbi-go.db) from the C#
# container's rbi.db — both can share the same ./data host directory.
ENV RBI_DB_PATH="Data Source=/app/data/rbi-go.db"

# RBI_PROXY_BIND: 0.0.0.0 so the forward proxy is reachable from outside the
# container (host browser's proxy setting points here). Config default is
# 127.0.0.1 which is unreachable from the host when running in a container.
ENV RBI_PROXY_BIND=0.0.0.0

# WebRTC defaults — explicit so they are visible in `docker inspect` and easy
# to override without needing to look up config.go.
ENV RBI_WEBRTC_ADVERTISED_IP=127.0.0.1
ENV RBI_WEBRTC_UDP_PORT_START=40000
ENV RBI_WEBRTC_UDP_PORT_END=40009

# --shm-size=1g must be passed at `docker run` time (not settable here); see
# run_docker_go.sh. Chromium's default /dev/shm of 64 MB is too small and
# forces disk-backed shared memory, which causes crashes on heavy pages.

WORKDIR /app

# HTTP API server (Go default: 5139 — differs from C#'s 5000).
EXPOSE 5139/tcp
# TLS-intercepting forward proxy (same as C#).
EXPOSE 8080/tcp
# WebRTC UDP media range (same as C#).
EXPOSE 40000-40009/udp

ENTRYPOINT ["/app/server"]
