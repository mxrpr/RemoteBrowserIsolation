#!/usr/bin/env bash
# Starts the RemoteBrowserIsolation.Server in dev mode.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_DIR="$SCRIPT_DIR/src/RemoteBrowserIsolation.Server"

cd "$PROJECT_DIR"
export ASPNETCORE_ENVIRONMENT="${ASPNETCORE_ENVIRONMENT:-Development}"
exec dotnet run
