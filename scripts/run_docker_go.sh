#!/usr/bin/env bash
# Builds (via build_docker_go.sh) and runs the rbi-go image. The SQLite DB is
# bind-mounted from ./data on the host so rebuilding/re-running with a new image
# never loses existing settings/policies/CA. rbi-go.db and the C# container's
# rbi.db have different filenames, so both containers can share the same ./data
# directory without collision.
#
# RBI_WEBRTC_ADVERTISED_IP overrides the WebRTC answer SDP's host candidate IP
# for setups where the browser isn't on the same machine as the container
# (default: 127.0.0.1, i.e. browser + container on one machine via loopback).
#
# RBI_SELF_HOST adds an extra hostname/IP to the proxy's self-host list. Unlike
# the C# container (which appends via ASP.NET Core's array-index env var trick),
# rbi-go's RBI_PROXY_SELF_HOSTS *replaces* the whole list, so this script
# builds the full comma-separated value: "localhost,127.0.0.1,<extra>". Needed
# whenever the browser's proxy setting and its address bar for this app disagree
# (e.g. reaching the app via a LAN IP) so those requests bypass policy instead
# of being TLS-intercepted.
#
# --network host: skips Docker's NAT/userland-proxy overhead for the WebRTC UDP
# media range. Linux-only; Docker Desktop (Mac/Windows) falls back to -p mappings.
# --shm-size=1g: headless Chromium's default 64 MB /dev/shm is too small and
# forces disk-backed shared memory (slower, sometimes crashes on heavy pages).
#
# Container is named rbi-go (NOT rbi) to avoid colliding with the C# container.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

"$ROOT/scripts/build_docker_go.sh"

mkdir -p "$ROOT/data"

RUN_ARGS=(
    --rm -it
    --shm-size=1g
    -v "$ROOT/data:/app/data"
    -e "RBI_WEBRTC_ADVERTISED_IP=${RBI_WEBRTC_ADVERTISED_IP:-127.0.0.1}"
    --name rbi-go
)

if [[ "$(uname -s)" == "Linux" ]]; then
    RUN_ARGS+=(--network host)
else
    RUN_ARGS+=(
        -p 5139:5139
        -p 8080:8080
        -p 40000-40009:40000-40009/udp
    )
fi

if [[ -n "${RBI_SELF_HOST:-}" ]]; then
    # Provide the full self-host list including the defaults, since RBI_PROXY_SELF_HOSTS
    # replaces rather than appends (unlike the C# Proxy__SelfHosts__2 index trick).
    RUN_ARGS+=(-e "RBI_PROXY_SELF_HOSTS=localhost,127.0.0.1,${RBI_SELF_HOST}")
fi

docker run "${RUN_ARGS[@]}" rbi-go:latest
