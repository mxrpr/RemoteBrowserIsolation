#!/usr/bin/env bash
# Builds (via build_docker.sh) and runs the rbi image. The SQLite DB (and root CA stored in it) is
# bind-mounted from ./data on the host so rebuilding/re-running with a new image version never
# overrides existing settings/policies/CA.
#
# RBI_ADVERTISED_IP overrides the WebRTC answer SDP's host candidate IP for setups where the
# browser isn't on the same machine as the container (default: localhost, i.e. browser + container
# on one machine reaching the published UDP range via loopback).
#
# RBI_SELF_HOST adds one extra entry to Proxy:SelfHosts (which ships with just "localhost" and
# "127.0.0.1" baked into appsettings.json) -- ASP.NET Core's config binder merges env-var array
# entries with the appsettings.json ones by index, so this doesn't replace the defaults, it appends
# a 3rd. Needed whenever the browser's proxy setting and its address bar for this app disagree --
# e.g. reaching the app via a LAN IP or a real hostname instead of localhost -- since without it,
# the browser's own requests to the admin console / video viewer would get policy-checked and
# TLS-intercepted like any other site once the proxy is set globally, instead of bypassing straight
# to Kestrel (see TlsInterceptingProxyServer.BlindTunnelToSelfOriginAsync).
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

"$ROOT/scripts/build_docker.sh"

mkdir -p "$ROOT/data"

RUN_ARGS=(
    --rm -it
    -p 5000:5000
    -p 8080:8080
    -p 40000-40009:40000-40009/udp
    -v "$ROOT/data:/app/data"
    -e "WebRtc__AdvertisedIp=${RBI_ADVERTISED_IP:-127.0.0.1}"
    --name rbi
)

if [[ -n "${RBI_SELF_HOST:-}" ]]; then
    RUN_ARGS+=(-e "Proxy__SelfHosts__2=${RBI_SELF_HOST}")
fi

docker run "${RUN_ARGS[@]}" rbi:latest
