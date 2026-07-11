#!/usr/bin/env bash
# Publishes the ASP.NET Core server to ./publish. Standalone script, usable outside Docker too --
# scripts/build_docker.sh calls this before `docker build` so the image COPYs a prebuilt output
# instead of restoring/building inside the image.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

dotnet publish "$ROOT/src/RemoteBrowserIsolation.Server" -c Release -o "$ROOT/publish"
