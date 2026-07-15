#!/usr/bin/env bash
# Builds the rbi-go:latest Docker image. The Go build runs entirely inside the
# Dockerfile build stage (see Dockerfile.go) -- no host-side compile step is
# needed, unlike the C# build_docker.sh which calls compile.sh first. Go's
# in-stage build is fast with module-download layer caching and avoids requiring
# a Go toolchain on the host machine.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

docker build -t rbi-go:latest -f "$ROOT/Dockerfile.go" "$ROOT"
