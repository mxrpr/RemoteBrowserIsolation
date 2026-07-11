#!/usr/bin/env bash
# Builds the rbi:latest Docker image: compiles the app on the host, then COPYs the published output
# into the runtime image (see Dockerfile) -- no in-image dotnet restore/build.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

"$ROOT/scripts/compile.sh"
docker build -t rbi:latest -f "$ROOT/Dockerfile" "$ROOT"
