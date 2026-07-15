#!/usr/bin/env bash
# Runs all Go tests for the rbi-go module.
#
# No -tags vpx: tests are written to avoid requiring libvpx-dev on the CI or dev
# machine (the vpx encoder tests gate themselves on the build tag). All 592 unit
# tests in the established test suite pass without libvpx-dev present.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
GO_DIR="$SCRIPT_DIR/src/rbi-go"

cd "$GO_DIR"
go test ./...
