#!/usr/bin/env bash
# One-shot setup for local (non-Docker) development: installs Playwright's headless Chromium and a
# matching FFmpeg 8.x shared build, then points the app at the FFmpeg lib directory. Replaces the
# manual multi-step dance in README.md's "Developer installation" section 4-6.
#
# Linux x86_64 only (matches the Dockerfile's own assumptions). Requires the .NET 9 SDK and `dotnet
# build` to already have been run once (this script builds if that hasn't happened yet).
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
PROJECT_DIR="$ROOT/src/RemoteBrowserIsolation.Server"
DEPS_DIR="$ROOT/deps"
FFMPEG_DIR="$DEPS_DIR/ffmpeg-8.1"

if [[ "$(uname -s)" != "Linux" || "$(uname -m)" != "x86_64" ]]; then
    echo "This script only automates Linux x86_64 setup (matching the Dockerfile)." >&2
    echo "On other platforms, follow README.md's manual 'Developer installation' steps instead." >&2
    exit 1
fi

if ! command -v dotnet &>/dev/null; then
    echo "dotnet SDK not found. Install the .NET 9 SDK first: https://dotnet.microsoft.com/download/dotnet/9.0" >&2
    exit 1
fi

# playwright.ps1 is emitted into bin/ by the Microsoft.Playwright NuGet package -- build once if
# it isn't there yet so this script can run standalone on a fresh checkout.
if [[ ! -f "$PROJECT_DIR/bin/Debug/net9.0/playwright.ps1" ]]; then
    echo "==> Building the project once to restore NuGet packages and emit playwright.ps1..."
    (cd "$PROJECT_DIR" && dotnet build)
fi

if ! command -v pwsh &>/dev/null; then
    echo "==> pwsh (PowerShell) not found -- installing via apt (Debian/Ubuntu)..."
    if command -v apt-get &>/dev/null; then
        curl -sL https://packages.microsoft.com/config/ubuntu/"$(lsb_release -rs)"/packages-microsoft-prod.deb -o /tmp/packages-microsoft-prod.deb
        sudo dpkg -i /tmp/packages-microsoft-prod.deb
        rm -f /tmp/packages-microsoft-prod.deb
        sudo apt-get update
        sudo apt-get install -y powershell
    else
        echo "No apt-get found. Install PowerShell manually: https://learn.microsoft.com/powershell/scripting/install/installing-powershell" >&2
        exit 1
    fi
fi

echo "==> Installing Playwright's headless Chromium (this downloads a browser binary, may take a minute)..."
(cd "$PROJECT_DIR" && pwsh bin/Debug/net9.0/playwright.ps1 install --with-deps chromium)

if [[ -d "$FFMPEG_DIR/lib" && -f "$FFMPEG_DIR/lib/libavcodec.so.62" ]]; then
    echo "==> FFmpeg 8.x already present at $FFMPEG_DIR/lib, skipping download."
else
    echo "==> Downloading FFmpeg 8.x shared build (BtbN linux64-gpl-shared)..."
    mkdir -p "$FFMPEG_DIR"
    curl -sL https://github.com/BtbN/FFmpeg-Builds/releases/download/latest/ffmpeg-n8.1-latest-linux64-gpl-shared-8.1.tar.xz \
        -o /tmp/ffmpeg.tar.xz
    tar -xf /tmp/ffmpeg.tar.xz -C "$FFMPEG_DIR" --strip-components=1
    rm -f /tmp/ffmpeg.tar.xz
fi

# Points the dev server at deps/ffmpeg-8.1/lib via appsettings.Development.json, so no shell env
# var needs to survive into whatever terminal eventually runs startRBI_dev.sh. python3 is used for
# the JSON patch since it's virtually always present alongside a .NET dev setup and handles this
# more safely than sed against arbitrary existing file content.
echo "==> Pointing FFmpeg:LibPath at $FFMPEG_DIR/lib in appsettings.Development.json..."
python3 - "$PROJECT_DIR/appsettings.Development.json" "$FFMPEG_DIR/lib" <<'PY'
import json
import sys

path, lib_path = sys.argv[1], sys.argv[2]
with open(path) as f:
    config = json.load(f)

config.setdefault("FFmpeg", {})["LibPath"] = lib_path

with open(path, "w") as f:
    json.dump(config, f, indent=2)
    f.write("\n")
PY

echo "==> Done. Run ./startRBI_dev.sh to start the dev server."
