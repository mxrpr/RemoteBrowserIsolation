# Plan: Add Unit Tests

## Status
No test project exists yet. `startTests.sh` finds `*Tests.csproj` and no-ops if none found — confirms tests were never started.

## Parts checklist
Ordered, self-contained parts for a plan→implement→review→test pipeline. Do the parts in order; Step 0 (Part 1) must land before any test-writing part. Priority ordering is preserved (Step 0 → P1 → P2 → P3 → P4). Each part is roughly one source file's worth of new tests.

- [x] Part 1: Create test project (Step 0) — scaffold `src/RemoteBrowserIsolation.Server.Tests/RemoteBrowserIsolation.Server.Tests.csproj` with xUnit + `Microsoft.NET.Test.Sdk` + `Microsoft.EntityFrameworkCore.InMemory` + project reference to the main csproj. Verify `./startTests.sh` discovers and actually runs xUnit (currently exits 0 silently).
- [x] Part 2: Test `HttpMessageIO.cs` (P1) — cover `ParseRequestLine`, `ReadHeadersAsync` (via `ProxyStreamReader` over `MemoryStream`), `ReadBodyAsync`/`ReadChunkedBodyAsync`, and `WriteResponseAsync`, including malformed/edge inputs. Biggest untested surface.
- [x] Part 3: Test `ProxyStreamReader.cs` (P1) — cover `ReadLineAsync` (CRLF/bare-LF/empty/no-terminator), `ReadExactAsync` (exact/short/buffered-spanning), `ReadByteAsync`, and `DrainBuffered` using `MemoryStream` fixtures, no real socket.
- [x] Part 4: Test `ProxyHeaders.cs` (P1) — table-driven tests for `IsHopByHop`: known hop-by-hop names, case-insensitivity, custom header named in a `Connection:` value, and header not named anywhere → false.
- [x] Part 5: Test `HtmlNoInputInjector.cs` (P1) — cover `Process`: `<meta charset>` normalization, legacy `http-equiv` form, no-meta insertion, and `noInput` true/false style injection; assert exact output bytes per fixture.
- [ ] Part 6: Test `LogLevelState.cs` (P1) — trivial round-trip: construct, `SetLevel`, read `CurrentLevel`.
- [ ] Part 7: Test `LeafCertificateMinter.cs` (P2) — cover static `IsNearExpiry` boundaries and `MintAsync` end-to-end against a fake `IRootCaStore` with a real self-signed test CA (SAN/EKU/basic-constraints assertions, non-RSA/expired/no-CA cases). Keep case count small (RSA keygen is slow); no OS cert store or network.
- [ ] Part 8: Test `MjpegToI420Decoder.CopyPlane` (P2) — make `CopyPlane` `internal` + `[InternalsVisibleTo]` (or reflection) and test stride-with-padding packed copy and stride-equals-width no-op. `TryDecode` stays out of scope (no FFmpeg seam).
- [ ] Part 9: Test `PageDownloader.cs` (P3) — cover `DownloadAsync`: non-http(s) scheme rejected, 2xx success with body, non-2xx failure + status, timeout, and caught `HttpRequestException`, using a fake `HttpMessageHandler` (no real network).
- [ ] Part 10: Test `PolicyEngine.cs` (P4) — `ResolveAsync` over EF Core InMemory: exact-host match wins, subdomain match, longest-pattern-wins, deny-by-default on no match, each `ViewMode` returned correctly.
- [ ] Part 11: Test `AdminAuthService.cs` (P4) — `LoginOrBootstrapAsync`: first-login bootstrap, subsequent correct login, wrong password, case-insensitive email; issued JWT decodes with expected claims/expiry from config.
- [ ] Part 12: Test `LogLevelSettingsStore.cs` (P4) — `GetLevelAsync` cache-hit vs DB-fallback vs default-Information-when-unset; `SetLevelAsync` upserts single row and mirrors to `LogLevelState`.
- [ ] Part 13: Test `VideoEncoderSettingsStore.cs` (P4) — same cache/upsert-by-fixed-id pattern, default `VideoEncoderMode.Auto`.
- [ ] Part 14: Test `RequestLogService.cs` (P4) — `LogAsync` maps `Uri`→`Url`/`Host` fields correctly and persists a row.
- [ ] Part 15: Test `RootCaStore.cs` (P4) — `GetActiveCaAsync` loads once and caches, `Invalidate` forces reload, PKCS12 blob round-trips.

## Detailed scope notes (per part)

### Part 1 — create test project (Step 0)
`src/RemoteBrowserIsolation.Server.Tests/RemoteBrowserIsolation.Server.Tests.csproj`
- xUnit + `Microsoft.NET.Test.Sdk` + `Microsoft.EntityFrameworkCore.InMemory` (for DB-backed services) + project reference to main csproj.
- Verify: `./startTests.sh` discovers and runs it (currently exits 0 silently — after this it must run xUnit and print results).

### Priority 1 — pure logic, no mocks needed (highest value/effort ratio)

Part 2 — **`Services/Proxy/HttpMessageIO.cs`** — biggest untested surface, proxy correctness depends on it.
- `ParseRequestLine`: valid line → correct method/target/version; malformed → null.
- `ReadHeadersAsync` (via `ProxyStreamReader` over `MemoryStream`): normal headers, malformed line skipped, header-folding/edge cases.
- `ReadBodyAsync` vs `ReadChunkedBodyAsync`: Transfer-Encoding takes precedence over Content-Length; hex chunk-size parsing; chunk-extension (`;ext=val`) stripped; short/incomplete body.
- `WriteResponseAsync`: status line format, hop-by-hop headers stripped, forces `Content-Length`, forces `Connection: close`.
- Verify: all pass, cover malformed/edge inputs alongside happy path.

Part 3 — **`Services/Proxy/ProxyStreamReader.cs`**
- `ReadLineAsync`: CRLF and bare-LF termination, empty line, no-terminator-at-EOF.
- `ReadExactAsync`: exact count, short-stream throws/handles, buffered-then-stream-spanning read.
- `ReadByteAsync`, `DrainBuffered`: drain returns exactly buffered bytes, splices correctly with subsequent stream reads.
- Verify: `MemoryStream` fixtures, no real socket.

Part 4 — **`Services/Proxy/ProxyHeaders.cs`**
- `IsHopByHop`: always-hop-by-hop names (`Connection`, `Keep-Alive`, `Transfer-Encoding`, etc.), case-insensitivity, custom header named in a `Connection:` value, header not named anywhere → false.
- Verify: table-driven test over the known hop-by-hop set + custom-Connection-token cases.

Part 5 — **`Services/Proxy/HtmlNoInputInjector.cs`**
- `Process`: existing `<meta charset>` normalized; legacy `http-equiv` charset form normalized; no meta tag present → one inserted; `noInput=true` injects blocking `<style>`; `noInput=false` does not.
- Verify: assert exact output HTML/bytes per fixture case.

Part 6 — **`Services/LogLevelState.cs`** — trivial: construct, `SetLevel`, read `CurrentLevel` round-trips.

### Priority 2 — pure static helpers inside otherwise stateful classes

Part 7 — **`Services/Proxy/LeafCertificateMinter.cs`**
- `IsNearExpiry` (static): boundary cases (well before expiry / just inside threshold / past expiry).
- `MintAsync` end-to-end against a fake `IRootCaStore` returning a real self-signed test CA: asserts SAN/EKU/basic-constraints on minted leaf, non-RSA CA key throws, expired/no-CA → null. Slower (RSA keygen) — keep as a handful of cases, not exhaustive.
- Verify: no network/OS cert store touched.

Part 8 — **`Services/MjpegToI420Decoder.cs`**
- `CopyPlane` (private — make `internal` + `[InternalsVisibleTo]`, or test via reflection): stride-with-padding → packed copy, stride-equals-width no-op case.
- Skip `TryDecode` itself — no mock seam over FFmpeg, would need real libavcodec + JPEG fixture; treat as integration-only, out of scope here.

### Priority 3 — needs mocked interface (no DB)

Part 9 — **`Services/PageDownloader.cs`**
- `DownloadAsync`: non-http(s) scheme rejected, 2xx → `Success=true` with body, non-2xx → `Success=false` + status captured, timeout → `Success=false` + timeout message, thrown `HttpRequestException` → caught and mapped.
- Verify: `HttpClient` constructed with a fake `HttpMessageHandler` (no real network).

### Priority 4 — needs DB (EF Core InMemory provider)

Part 10 — **`Services/PolicyEngine.cs`** — `ResolveAsync`: exact host match wins, subdomain match, longest-pattern-wins when multiple rows match, no match → deny-by-default, each `ViewMode` value returned correctly.
Part 11 — **`Services/AdminAuthService.cs`** — `LoginOrBootstrapAsync`: first-ever login bootstraps admin row, subsequent correct login succeeds, wrong password fails, case-insensitive email match; issued JWT decodes with expected claims/expiry from config.
Part 12 — **`Services/LogLevelSettingsStore.cs`** — `GetLevelAsync` cache hit vs DB fallback vs default-Information-when-unset; `SetLevelAsync` upserts single row and mirrors to `LogLevelState`.
Part 13 — **`Services/VideoEncoderSettingsStore.cs`** — same cache/upsert-by-fixed-id pattern, default `VideoEncoderMode.Auto`.
Part 14 — **`Services/RequestLogService.cs`** — `LogAsync` maps `Uri`→`Url`/`Host` fields correctly and persists row.
Part 15 — **`Services/Proxy/RootCaStore.cs`** — `GetActiveCaAsync` loads once and caches; `Invalidate` forces reload; PKCS12 blob round-trips.

## Out of scope (NOT part of the checklist — no seam / needs real hardware)
- `Services/GpuEncoderProbe.cs` — needs real GPU/FFmpeg native calls, no mockable interface.
- `Services/MjpegToI420Decoder.cs.TryDecode` — needs real libavcodec decode context.
- `Services/WebRtcSessionManager.cs`, `Services/DataChannelTransport.cs`, `Services/HeadlessBrowserSessionManager.cs`, `Services/InputEventForwarder.cs`, `Services/VideoTrackStreamer.cs`, `Services/Proxy/OriginForwarder.cs`, `Services/Proxy/TlsInterceptingProxyServer.cs` — full WebRTC/Playwright/socket integration, not unit-test shaped; would need integration-test harness, separate effort.

## Suggested order
Step 0 → Priority 1 (5 files, no mocks, fast payoff) → Priority 2 → Priority 3 → Priority 4 (heaviest setup, EF InMemory fixtures).
