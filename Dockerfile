# Runtime-only image: scripts/compile.sh publishes the app on the host first, this Dockerfile just
# assembles the runtime (see scripts/build_docker.sh) -- no in-image `dotnet restore`/build.
FROM mcr.microsoft.com/dotnet/aspnet:9.0-noble

# curl/xz-utils: fetch + extract the FFmpeg tarball below.
# wget/gnupg/apt-transport-https: add the Microsoft package repo so `powershell` (pwsh) is
#   installable -- Playwright's .NET port only ships a playwright.ps1 installer, no shell script.
# libva2/libva-drm2: FFmpeg's VAAPI hwdevice probe (GpuEncoderProbe) dlopens libva-drm.so.2
#   through an implib-gen lazy-load stub; when the lib is absent that stub hits a hard assert()
#   and aborts the whole process (SIGABRT, not a catchable .NET exception) instead of failing the
#   probe gracefully. Installing the real lib makes the probe resolve/fail normally instead.
RUN apt-get update && apt-get install -y --no-install-recommends \
        curl xz-utils wget gnupg apt-transport-https ca-certificates libva2 libva-drm2 \
    && wget -q https://packages.microsoft.com/config/ubuntu/24.04/packages-microsoft-prod.deb -O /tmp/packages-microsoft-prod.deb \
    && dpkg -i /tmp/packages-microsoft-prod.deb \
    && rm /tmp/packages-microsoft-prod.deb \
    && apt-get update && apt-get install -y --no-install-recommends powershell \
    && rm -rf /var/lib/apt/lists/*

# FFmpeg 8.x shared libs (SIPSorceryMedia.FFmpeg's VP8 encoder needs libav*.so.62). Pinned to the
# n8.1 release asset -- verified to expose libavcodec.so.62.28.102, matching the host dev build this
# app was developed against.
RUN curl -sL https://github.com/BtbN/FFmpeg-Builds/releases/download/latest/ffmpeg-n8.1-latest-linux64-gpl-shared-8.1.tar.xz \
        -o /tmp/ffmpeg.tar.xz \
    && mkdir -p /opt/ffmpeg \
    && tar -xf /tmp/ffmpeg.tar.xz -C /opt/ffmpeg --strip-components=1 \
    && rm /tmp/ffmpeg.tar.xz \
    && rm -rf /opt/ffmpeg/include /opt/ffmpeg/bin /opt/ffmpeg/doc /opt/ffmpeg/share

# playwright.ps1 needs its full dependency closure (deps.json/runtimeconfig.json/the bundled
# .playwright driver, not just the .ps1 + Microsoft.Playwright.dll) to resolve and run via
# reflection -- copying only those two made the install step throw "missing required assets" (or,
# worse, silently no-op with a non-fatal PowerShell error, leaving PLAYWRIGHT_BROWSERS_PATH empty
# and every render fail at runtime with "Executable doesn't exist"). Copying the whole publish/
# output here means this layer no longer caches across app-only-code rebuilds, but a slower correct
# build beats a fast broken one.
COPY publish/ /app/

# Downloads headless Chromium + its OS-level deps into PLAYWRIGHT_BROWSERS_PATH, matching the
# Microsoft.Playwright package version referenced by the published app.
ENV PLAYWRIGHT_BROWSERS_PATH=/ms-playwright
RUN pwsh /app/playwright.ps1 install --with-deps chromium \
    && rm -rf /var/lib/apt/lists/*

ENV LD_LIBRARY_PATH=/opt/ffmpeg/lib
ENV FFmpeg__LibPath=/opt/ffmpeg/lib
ENV ASPNETCORE_URLS=http://0.0.0.0:5000
ENV ConnectionStrings__Sqlite="Data Source=/app/data/rbi.db"
ENV WebRtc__AdvertisedIp=127.0.0.1
ENV WebRtc__UdpPortStart=40000
ENV WebRtc__UdpPortEnd=40009
# 0.0.0.0, not the appsettings.json default of 127.0.0.1 -- the TLS-intercepting proxy must be
# reachable from outside the container (host browser's proxy setting) for HTML mode to work, since
# "inside the container" and "127.0.0.1 as seen by the container" are the same loopback either way.
ENV Proxy__Bind=0.0.0.0

WORKDIR /app
EXPOSE 5000/tcp
EXPOSE 8080/tcp
EXPOSE 40000-40009/udp

ENTRYPOINT ["dotnet", "RemoteBrowserIsolation.Server.dll"]
