# Remote Browser Isolation (RBI)

## What this is

Server that sits in front of your users' web browsing as a **forward proxy**: point your browser's
proxy setting at this server and every HTTP/HTTPS request you make transits it. **Setting the
browser's proxy to this server is required** — it's not an optional extra, it's how the app sees
traffic at all. Two things happen per request, decided by admin-managed per-hostname policy:

- **HTML mode (the default/normal path)** — the proxy intercepts the `CONNECT`, terminates TLS
  with a cert minted off an admin-uploaded root CA, fetches the real page, relays it straight back.
  Normal browsing speed, real client-side rendering. Used for hosts the policy trusts.
- **Video mode (escalation for risky/untrusted sites)** — the proxy serves a static interstitial
  instead of any real content, linking to a WebRTC session: server-side headless Chromium
  (Playwright) renders the page, streams it to the client as a VP8 video track, client input goes
  back over a data channel. Zero page code (HTML/JS/CSS) ever reaches the client here — it only
  ever sees pixels. Used for hosts the policy doesn't trust to touch the user's real browser.

Unlisted hosts are blocked outright (deny-by-default).

Per-site policy (admin-managed) decides, per hostname, which of four modes applies — or blocks the
site outright (deny-by-default: an unlisted host is blocked):

| Mode | Rendering | Input | Enforcement |
|------|-----------|-------|-------------|
| `VideoAllowInput` | Server-side headless Chromium, streamed as VP8 video | Forwarded to the page | — |
| `VideoNoInput` | Server-side headless Chromium, streamed as VP8 video | Mouse forwarded (click/scroll/navigate); keyboard dropped before replay | **Server-side** — the forwarder drops keydown/keyup before they ever reach the page, so a malicious/modified client can't forge keystrokes by sending raw channel messages |
| `HtmlAllowInput` | Real page HTML/JS/CSS relayed to the client's own browser via the TLS-intercepting proxy | Full input, normal browsing | — |
| `HtmlNoInput` | Same relay as `HtmlAllowInput` | Text entry/editable controls visually disabled via an injected `<style>` (links/scroll/navigation still work) | **Client-side only** — a hostile or modified client can strip the injected CSS and type anyway; this mode is a UI nudge, not a security boundary |

`VideoNoInput` is the only "no keyboard" mode that's actually trustworthy against a malicious
client. `HtmlNoInput` is cosmetic: since the real page executes in the user's own browser, the
server has no way to stop a client from re-enabling input once it decrypts the response.

## What this achieves — security & privacy

- **Isolation from untrusted/unknown web content.** In video mode the user's real browser executes
  none of the target page's JavaScript/HTML — malicious scripts, drive-by exploits, and browser
  0-days on the target site can't reach the user's actual machine, only the disposable headless
  Chromium instance on the server.
- **Server-side-enforced keyboard blocking.** `VideoNoInput` mode drops keydown/keyup events
  server-side before they're replayed against the rendered page (mouse still works, for
  click/scroll/navigate), so a compromised/malicious client can't bypass a "look but don't type"
  policy by forging keyboard events — it's not a client-side filter that a hostile browser
  extension or hand-rolled client could skip.
- **Centralized, per-site policy control.** An admin defines which hosts are isolated, in what
  mode, and which are blocked entirely — a single choke point instead of relying on every
  end-user's own browser/extension hygiene.
- **No page code crosses the trust boundary in video mode.** The client only ever receives an
  encoded video stream + audio-free pixels; there's no DOM, no cookies, no JS execution context
  for the target site to attack.
- **Full request visibility.** Every request is logged, giving an audit trail of what sites users
  actually reached and through which mode.
- **TLS interception is explicit and admin-controlled.** HTML mode's forward proxy mints leaf
  certificates off an admin-uploaded root CA — nothing is intercepted unless the operator sets that
  up and installs the CA into client trust stores.

This is not a silver bullet: it isolates the *rendering* of a page, not every possible egress or
data-exfiltration path (e.g. it doesn't inspect what a user types into a form). It substantially
reduces the attack surface exposed to the user's real machine.

## Architecture at a glance

Single ASP.NET Core (net9.0) project at `src/RemoteBrowserIsolation.Server`, SIPSorcery for WebRTC,
Playwright for headless Chromium, SQLite for policy/admin/CA storage. See `CLAUDE.md` for the
detailed internals (data channel negotiation, signaling flow, service responsibilities).

---

## Developer installation

Steps to get a working dev environment from a clean machine.

1. **Clone the repo**
   ```bash
   git clone <repo-url> remote_browser_isolation
   cd remote_browser_isolation
   ```

2. **Install the .NET 9 SDK**
   Follow https://dotnet.microsoft.com/download/dotnet/9.0 for your OS, then confirm:
   ```bash
   dotnet --version   # expect a 9.x SDK
   ```

3. **Restore dependencies and build**
   ```bash
   cd src/RemoteBrowserIsolation.Server
   dotnet build
   ```
   This pulls all NuGet packages (SIPSorcery, SIPSorceryMedia.FFmpeg, Microsoft.Playwright,
   EF Core/Sqlite, AngleSharp, etc.) automatically.

4. **Install Playwright's headless Chromium and FFmpeg 8.x**

   **Linux (x86_64):**
   ```bash
   cd ../..   # back to repo root
   ./scripts/install_dependencies.sh
   ```

   **Windows (x64):** in PowerShell, from the repo root:
   ```powershell
   .\scripts\install_dependencies.ps1
   ```

   Either automates what used to be three manual steps: installs `pwsh` if missing (Linux only —
   PowerShell is assumed already present on Windows), downloads Playwright's headless Chromium
   (`Microsoft.Playwright`'s NuGet package only restores bindings, not the browser binary itself),
   downloads a matching FFmpeg 8.x shared build (`SIPSorceryMedia.FFmpeg` needs `libavcodec.so.62`
   / `avcodec-62.dll` — neither Ubuntu's apt package nor any Windows package manager ships FFmpeg
   8.x) into `./deps/ffmpeg-8.1/`, and points `FFmpeg:LibPath` at it (the `lib/` directory on
   Linux; the `bin/` directory on Windows, since that's where the loadable DLLs actually live) via
   `src/RemoteBrowserIsolation.Server/appsettings.Development.json`.

   On other platforms, or if you'd rather do it by hand: run
   `pwsh bin/Debug/net9.0/playwright.ps1 install --with-deps chromium` from
   `src/RemoteBrowserIsolation.Server` (get `pwsh` from
   https://learn.microsoft.com/powershell/scripting/install/installing-powershell if needed), grab
   an `n8.x-...-shared` build for your OS from
   https://github.com/BtbN/FFmpeg-Builds/releases, and either set `FFmpeg:LibPath` in
   `appsettings.Development.json` or export `FFmpeg__LibPath=/path/to/ffmpeg/lib`.

5. **Run the dev server**
   From the repo root:
   ```bash
   ./startRBI_dev.sh
   ```
   This sets `ASPNETCORE_ENVIRONMENT=Development` and runs the app. Watch the startup log for the
   bound address (e.g. `http://localhost:5139`).

6. **Verify it's up**
   Browser → `http://localhost:<port>/health` — should show `{"status":"ok"}`.

7. **Generate a root CA** (needed for HTML mode; skip if you only want video mode)
   From the repo root, in another terminal:
   ```bash
   ./scripts/generate_root_ca.sh
   ```
   Writes `certs/rootCA.crt` (import into your OS/browser trust store), `certs/rootCA.pfx`
   (upload into the admin console next), `certs/rootCA.key` (private, keep secret). Prints a PFX
   password at the end — save it.

8. **Bootstrap the admin account, upload the CA, add a site policy**
    Browser → `http://localhost:<port>/admin/`. No account exists yet — the first email/password
    you submit on the login screen becomes the admin account.
    - **Root CA** tab → upload `certs/rootCA.pfx` with the password from step 7.
    - **Policies** tab → add a host (e.g. `example.com`) with a mode (`HtmlAllowInput`,
      `HtmlNoInput`, `VideoAllowInput`, `VideoNoInput`). Unlisted hosts are blocked by default.

9. **Point your browser's proxy setting at the server** (required for HTML-mode sites)
    HTTP and HTTPS proxy → `localhost:<port from step 5's "Proxy" config>` — default `8080`
    (`Proxy:Port` in `appsettings.json`). This is how the app sees your browsing traffic; without it
    only direct visits to `/admin/` and `/index.html` reach the app.

10. **Try the full flow**
    - HTML-mode host: navigate to it directly in your address bar — the proxy you just configured
      intercepts and relays it.
    - Video-mode host: open `http://localhost:<port>/index.html`, enter the URL, click Fetch.

11. **Run tests** (currently a no-op until a test project exists)
    ```bash
    ./startTests.sh
    ```

---

## Docker installation

Turnkey path for running the whole thing in a container, without installing .NET, Chromium, or
FFmpeg on the host yourself. Requires only Docker.

1. **Clone the repo**
   ```bash
   git clone <repo-url> remote_browser_isolation
   cd remote_browser_isolation
   ```

2. **Install Docker**
   Follow https://docs.docker.com/get-docker/ for your OS (Docker Desktop on Mac/Windows, Docker
   Engine on Linux). Confirm:
   ```bash
   docker --version
   ```

3. **Generate a root CA** (needed for HTML mode's TLS-intercepting proxy)
   ```bash
   ./scripts/generate_root_ca.sh
   ```
   Writes three files to `./certs` (gitignored):
   - `rootCA.crt` — public cert, import into your browser/OS trust store
   - `rootCA.pfx` — key+cert bundle, upload into the app's admin console (next steps)
   - `rootCA.key` — private key, keep secret

   The script prints a PFX password at the end (auto-generated unless you pass `-p PASSWORD`) —
   save it, you need it in step 6.

4. **Build and run**
   ```bash
   ./scripts/run_docker.sh
   ```
   This script:
   - compiles the app on the host (`scripts/compile.sh`, needs the .NET 9 SDK — see Developer
     installation step 2 if you don't have it),
   - builds the `rbi:latest` image (`scripts/build_docker.sh`), which bundles headless Chromium and
     FFmpeg 8.x inside the image — nothing else to install,
   - creates a local `./data` directory and bind-mounts it into the container at `/app/data`, so
     the SQLite database (policies, admin users, root CA) survives image rebuilds,
   - starts the container, publishing `5000/tcp` (app), `8080/tcp` (forward proxy), and
     `40000-40009/udp` (WebRTC media).

   If you don't have the .NET SDK on the host and only want Docker, run `dotnet publish` inside a
   throwaway SDK container instead, or install the SDK just for the compile step — the compile step
   currently runs on the host, not inside the image build.

5. **Open the app in your browser**
   Go to `http://localhost:5000/health` — should show `{"status":"ok"}`.

6. **Bootstrap the admin account**
   Go to `http://localhost:5000/admin/`. No account exists yet — the first email/password you
   submit on the login screen becomes the admin account.

7. **Upload the root CA**
   In the admin console, open the **Root CA** tab, upload `certs/rootCA.pfx` from step 3 with the
   password the script printed.

8. **Trust the root CA on your machine**
   Import `certs/rootCA.crt` into your OS/browser trust store (double-click on most desktops, or
   your browser's certificate-settings import). Required — without it, your browser will flag every
   HTML-mode connection as untrusted TLS, since the proxy terminates it with a cert signed by this
   CA, not a public one.

9. **Point your browser's proxy setting at the container**
   HTTP and HTTPS proxy → `localhost:8080`. **This is required, not optional** — the proxy is how
   the app sees your browsing traffic at all; without it nothing reaches the app except direct
   visits to `/admin/` and `/index.html`.

10. **Add a site policy**
    In the admin console's **Policies** tab, add a host (e.g. `example.com`) with a mode:
    - `HtmlAllowInput` / `HtmlNoInput` — normal relayed browsing through the proxy you just
      configured. Navigate to the site directly in your address bar afterward.
    - `VideoAllowInput` / `VideoNoInput` — isolated rendering. Go to
      `http://localhost:5000/index.html`, enter the URL, click Fetch.

    Unlisted hosts are blocked (deny-by-default) — you'll need a policy row for every site you want
    to actually reach.

11. **Non-default host setups**
    If the browser is **not** on the same machine as the Docker host (e.g. container runs on a
    remote server), two things need the container's real reachable address instead of the
    loopback default:
    ```bash
    RBI_ADVERTISED_IP=<host-reachable-ip> RBI_SELF_HOST=<host-reachable-address> ./scripts/run_docker.sh
    ```
    - `RBI_ADVERTISED_IP` — baked into the WebRTC answer SDP's host candidate (video mode).
    - `RBI_SELF_HOST` — added to `Proxy:SelfHosts` (alongside the built-in `localhost`/`127.0.0.1`)
      so the browser's own requests to the admin console/video viewer, made through the proxy
      you configured in step 9, bypass policy-checking/TLS-interception and reach Kestrel directly
      instead of being treated like any other site.

12. **Stop / re-run**
    ```bash
    docker stop rbi
    ./scripts/run_docker.sh   # rebuilds and restarts; ./data (DB, CA, policies) is preserved
    ```

13. **Reset everything** (only if you want to wipe state)
    ```bash
    docker rm -f rbi
    rm -rf ./data ./certs
    ```
