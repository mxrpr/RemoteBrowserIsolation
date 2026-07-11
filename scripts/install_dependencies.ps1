# One-shot setup for local (non-Docker) development on Windows: installs Playwright's headless
# Chromium and a matching FFmpeg 8.x shared build, then points the app at FFmpeg's DLL directory.
# Windows x64 equivalent of scripts/install_dependencies.sh -- see that script's header comment for
# why each step exists (Microsoft.Playwright's NuGet package only restores bindings, not the
# browser binary; SIPSorceryMedia.FFmpeg needs FFmpeg 8.x, no package manager ships that).
#
# Usage (from repo root, in PowerShell -- Windows PowerShell 5.1 or pwsh 7+ both work):
#   .\scripts\install_dependencies.ps1

$ErrorActionPreference = "Stop"

$Root = Split-Path -Parent $PSScriptRoot
$ProjectDir = Join-Path $Root "src\RemoteBrowserIsolation.Server"
$DepsDir = Join-Path $Root "deps"
$FFmpegDir = Join-Path $DepsDir "ffmpeg-8.1"
$FFmpegBinDir = Join-Path $FFmpegDir "bin"

if (-not (Get-Command dotnet -ErrorAction SilentlyContinue)) {
    Write-Error "dotnet SDK not found. Install the .NET 9 SDK first: https://dotnet.microsoft.com/download/dotnet/9.0"
    exit 1
}

# playwright.ps1 is emitted into bin/ by the Microsoft.Playwright NuGet package -- build once if
# it isn't there yet so this script can run standalone on a fresh checkout.
$PlaywrightPs1 = Join-Path $ProjectDir "bin\Debug\net9.0\playwright.ps1"
if (-not (Test-Path $PlaywrightPs1)) {
    Write-Host "==> Building the project once to restore NuGet packages and emit playwright.ps1..."
    Push-Location $ProjectDir
    dotnet build
    Pop-Location
}

Write-Host "==> Installing Playwright's headless Chromium (this downloads a browser binary, may take a minute)..."
Push-Location $ProjectDir
& $PlaywrightPs1 install --with-deps chromium
Pop-Location

$AvcodecDll = Get-ChildItem -Path $FFmpegBinDir -Filter "avcodec-*.dll" -ErrorAction SilentlyContinue
if ($AvcodecDll) {
    Write-Host "==> FFmpeg 8.x already present at $FFmpegBinDir, skipping download."
} else {
    Write-Host "==> Downloading FFmpeg 8.x shared build (BtbN win64-gpl-shared)..."
    New-Item -ItemType Directory -Force -Path $FFmpegDir | Out-Null
    $ZipPath = Join-Path $env:TEMP "ffmpeg.zip"
    Invoke-WebRequest -Uri "https://github.com/BtbN/FFmpeg-Builds/releases/download/latest/ffmpeg-n8.1-latest-win64-gpl-shared-8.1.zip" -OutFile $ZipPath

    # BtbN's zip extracts into a single top-level folder (e.g. ffmpeg-n8.1-...-win64-gpl-shared-8.1);
    # expand to a temp dir and move that folder's contents up into FFmpegDir so the resulting layout
    # matches the Linux script's flat $FFmpegDir/{bin,lib,include} (no extra nesting level).
    $ExtractTemp = Join-Path $env:TEMP "ffmpeg-extract"
    Remove-Item -Recurse -Force -ErrorAction SilentlyContinue $ExtractTemp
    Expand-Archive -Path $ZipPath -DestinationPath $ExtractTemp
    $InnerDir = Get-ChildItem -Path $ExtractTemp -Directory | Select-Object -First 1
    Get-ChildItem -Path $InnerDir.FullName | Move-Item -Destination $FFmpegDir -Force
    Remove-Item -Recurse -Force $ExtractTemp
    Remove-Item -Force $ZipPath
}

# Points the dev server at deps\ffmpeg-8.1\bin via appsettings.Development.json -- note this is
# bin\, not lib\ like the Linux script: a Windows FFmpeg shared build puts the loadable DLLs
# (avcodec-62.dll etc.) in bin\, with lib\ holding only the .lib import stubs used at link time,
# which is not what SIPSorceryMedia.FFmpeg needs at runtime.
Write-Host "==> Pointing FFmpeg:LibPath at $FFmpegBinDir in appsettings.Development.json..."
$AppSettingsPath = Join-Path $ProjectDir "appsettings.Development.json"
$Config = Get-Content $AppSettingsPath -Raw | ConvertFrom-Json
if (-not $Config.PSObject.Properties["FFmpeg"]) {
    $Config | Add-Member -MemberType NoteProperty -Name "FFmpeg" -Value ([PSCustomObject]@{})
}
if (-not $Config.FFmpeg.PSObject.Properties["LibPath"]) {
    $Config.FFmpeg | Add-Member -MemberType NoteProperty -Name "LibPath" -Value $FFmpegBinDir
} else {
    $Config.FFmpeg.LibPath = $FFmpegBinDir
}
$Config | ConvertTo-Json -Depth 10 | Set-Content $AppSettingsPath

Write-Host "==> Done. Run .\startRBI_dev.sh (Git Bash/WSL) or 'dotnet run' from src\RemoteBrowserIsolation.Server to start the dev server."
