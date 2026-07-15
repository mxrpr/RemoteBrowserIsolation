#!/usr/bin/env bash
# Starts the rbi-go server in dev mode.
#
# Runs from src/rbi-go/ so the default WwwRoot ("../RemoteBrowserIsolation.Server/wwwroot")
# resolves correctly via the working-directory fallback in resolveWwwRoot — no RBI_WWWROOT
# override is needed in this layout.
#
# -tags vpx compiles the real libvpx VP8 encoder (encoder_vpx.go); requires libvpx-dev
# installed on the dev machine. If libvpx-dev is not installed, drop the tag and video mode
# will use the stub encoder that returns an error only if a video-mode session is actually
# exercised — all other functionality continues to work normally.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
GO_DIR="$SCRIPT_DIR/src/rbi-go"

cd "$GO_DIR"
exec go run -tags vpx ./cmd/server/
