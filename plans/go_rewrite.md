## The project

Rewrite the backand in go lang. 
Ui has to remain the same.
Do not change the REST API
The project has to run with c# and with go backend too.
I need a new script set which works the same way as for c# backend, build bocker image with go, start backend with go.

## Decisions (locked)

- **WebRTC library:** `pion/webrtc` — de-facto Go standard; supports pre-negotiated data
  channels with fixed id, send-only VP8 track, full-ICE-in-answer via gathering-complete wait
  (matches current no-trickle single-round-trip signaling).
- **VP8 encoding:** cgo bindings directly against `libvpx` (not FFmpeg's libavcodec) — simpler
  binding surface than routing through FFmpeg, avoids needing full FFmpeg dev headers. Real
  encoder lives behind a `-tags vpx` build tag (`internal/video/encoder_vpx.go`); default build
  uses a stub (`internal/video/encoder_stub.go`) that errors at runtime so `go build ./...` still
  works without `libvpx-dev` installed. **Part 12's Docker image MUST install `libvpx-dev` and
  build the Go binary with `-tags vpx`** or the shipped server will have a non-functional video
  pipeline.
- **Headless browser driver:** `chromedp` — pure-Go CDP client, no separate driver binary/runtime
  to bundle (unlike playwright-go), direct access to `Page.startScreencast`/`screencastFrame`/
  `screencastFrameAck` and `Input.dispatch*` used today.
- **DB:** separate DB file for the Go backend (e.g. `rbi-go.db`), own schema, own admin
  bootstrap/password hashing. No EF-Core-schema or ASP.NET-Identity-hash-format coupling.
- **Go source location:** `src/rbi-go/`, own copy of/symlink to `wwwroot/`; C# project untouched.
- **JWT:** Go backend signs its own HS256 tokens with the same `Jwt:Key/Issuer/Audience` config
  keys; tokens are not required to validate across backends.

## Implementation Checklist

- [x] Part 1: Go project skeleton + config + HTTP server foundation — new `src/rbi-go/` module, config loader reading the same keys as appsettings.json/env vars (ports, Jwt, Proxy, WebRtc, FFmpeg, ConnectionStrings), a router, `GET /health` returning `{"status":"ok"}`, per-request INFO logging middleware, startup bound-address log, and static serving of `wwwroot/` (index.html default doc + admin/). Done = `go run` serves /health and index.html identically to the C# server.
- [x] Part 2: SQLite data layer + schema — open/create the DB and create all six tables (AdminUsers, SitePolicies, RequestLogs, RootCertificateAuthorities, VideoEncoderSettings, LogLevelSetting) with the unique indexes EF Core defines (AdminUser.Email, SitePolicy.HostPattern) and the single-row settings convention. Done = app starts against a fresh DB and against an existing one without error; a smoke test can insert/read each table.
- [x] Part 3: Admin JWT auth (tests: 43 tests added, all passing) — `AdminAuthService` equivalent (bootstrap-or-login, one admin row, password hashing, HS256 token issuance) plus JWT bearer validation middleware, and endpoints `POST /api/admin/auth/login` + `GET /api/admin/auth/status`. Done = status reports bootstrapped=false initially, first login bootstraps + returns a token, subsequent wrong creds return 401, and a valid token passes the middleware.
- [x] Part 4: Policy engine + site CRUD + logging (tests passing) + site CRUD + logging — in-memory longest-host-match resolver (exact or subdomain, deny→null), `GET/POST/PUT/DELETE /api/admin/sites` (host normalization, uniqueness conflict, JSON ViewMode as string names), request-log writer, public `GET /api/policy/resolve`, and `GET /api/admin/logs`. Done = CRUD round-trips, resolve returns the right mode/403, logs endpoint paginates newest-first, all matching C# JSON shapes.
- [x] Part 5: Settings stores + admin endpoints (tests passing) + admin endpoints — video-encoder mode store (Auto/CPU/GPU, single row id=1) with GPU probe, log-level store that live-adjusts the logger, and `GET/PUT /api/admin/settings/video-encoder` + `/log-level`. Done = get/put round-trip each setting, GPU probe surfaces available+description, log-level change takes effect without restart. (Gpu mode may keep the C# "fail loudly" semantics.)
- [x] Part 6: Root CA store + leaf cert minter + rootca endpoints (tests: 64 tests added, all passing) — persist/replace a single uploaded PFX, in-memory cached active CA with invalidation, RSA leaf minting (SAN DNS, serverAuth EKU, keyUsage, not-after ≤ CA), mint cache with clear-on-change, and `GET/POST/DELETE /api/admin/rootca` + `GET /api/admin/rootca/certificate` (multipart upload, public DER download). Done = upload validates CA constraints, mint produces a browser-valid leaf chained to the CA, delete/replace clears caches.
- [x] Part 7: HTML no-input injector (tests: 20 tests added, all passing) — port `HtmlNoInputInjector` (parse HTML, inject the read-only/no-input CSS/markers, re-serialize), including Content-Encoding inflate (gzip/br/deflate) before parsing. Done = a known HTML input produces the same injected output as the C# injector for the HtmlNoInput case; non-HTML/compressed inputs handled.
- [x] Part 8: TLS-intercepting forward proxy (tests: 76 tests added, all passing) — hand-rolled TCP listener (not the HTTP server): CONNECT parse, self-host blind-tunnel to the app's own port, non-intercept-port blind tunnel, policy check + deny 502, TLS terminate with minted leaf (SNI-driven, ALPN http/1.1), single-exchange origin forward, plain-HTTP absolute-URI path, HtmlNoInput injection, and the video-mode interstitial linking to `index.html?url=`. Depends on Parts 4, 6, 7. Done = browser pointed at the proxy loads an Html-allowed site through interception, gets the interstitial for a Video site, and 502s on an unmatched host.
- [x] Part 9: WebRTC session manager (pion) (tests: 20 tests added, all passing) — build the answerer: send-only VP8 track added pre-answer, pre-negotiated data channel id=1, set-remote/create-answer with ICE-gathering-complete wait, fixed publishable UDP port range, and SDP host-candidate/`c=` line rewrite to the advertised IP. Done = a captured browser offer yields a valid answer SDP with rewritten candidates; connection reaches `connected` against a test client.
- [x] Part 10: Headless browser session manager (tests: 14 tests added, all passing) — chromedp: one shared browser, a fresh isolated context+page per session sized to the requested viewport, navigate to target (DOMContentLoaded), and teardown. Done = a session opens, navigates a page, and closes without leaking contexts.
- [x] Part 11: Video pipeline + input forwarding + session endpoint (tests: 21+15 tests added, all passing) — CDP screencast → JPEG decode → VP8 encode → pion `WriteRTP`/track with latest-wins mailbox + periodic keyframes; input data-channel JSON replay (mouse always, keyboard gated by allowKeyboard) in strict arrival order; and wire `POST /api/session/offer` (policy re-resolve, mode-mismatch 409, deny 403, allowKeyboard from mode). Depends on Parts 4, 9, 10. Done = opening index.html?url= against a Video-allowed site streams live VP8 and forwards clicks/scroll; VideoNoInput drops keystrokes server-side.
- [x] Part 12: Go script set + Docker (verified: docker build + run + `/health` 200 + static serving) — `startRBI_go_dev.sh`, `startTests_go.sh`, a Go Dockerfile (Chromium + `libvpx-dev` + Go binary built with `-tags vpx` — see Part 11's VP8 encoding decision, non-optional or video is silently broken), and `scripts/build_docker_go.sh` / `scripts/run_docker_go.sh` mirroring the C# scripts (own image/container name, e.g. `rbi-go`, same bind-mounted `./data`, same published ports, `--network host`/`--shm-size`). Done = `./startRBI_go_dev.sh` runs the Go server, tests run, and the Go image builds and serves the full flow — the C# scripts/image still work unchanged.
