#!/usr/bin/env bash
# Measures live per-frame VP8 transcode time (the "Video frame ... in {Ms}ms" debug log line
# VideoTrackStreamer already emits) against a real running server, so perf claims in
# plans/11_video_pipeline_speedup.md can be checked against actual numbers instead of just the
# standalone decode/encode correctness test.
#
# Runs the server from a given worktree/repo checkout, in video mode, against a continuously
# animating local page (fixtures/animated.html) for a fixed window, then reports the median/p95
# per-frame transcode ms and frame count from the server's Debug log.
#
# Usage: measure_video_perf.sh <worktree-root> <label> [duration-seconds]
#
# Isolated ports/DB, same convention as run_e2e.sh -- never touches a developer's own instance.
# Everything created is removed on exit.
set -uo pipefail

WORKTREE_ROOT="$1"
LABEL="$2"
DURATION="${3:-20}"
FIXTURE_PAGE="${4:-animated.html}"

# Fixture HTML lives in this checkout (tests/e2e/fixtures/), which may not exist in an older
# worktree/baseline checkout -- fall back to the current script's own fixtures dir so the same
# animated page is used for every measurement, keeping the comparison apples-to-apples.
SELF_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
FIXTURES_DIR="$SELF_DIR/fixtures"
CLIENT_PROJECT="$SELF_DIR/WebRtcTestClient/WebRtcTestClient.csproj"

SERVER_PROJECT="$WORKTREE_ROOT/src/RemoteBrowserIsolation.Server"

HTTP_PORT=15239
PROXY_PORT=18180
WEBRTC_UDP_START=41100
WEBRTC_UDP_END=41109
STATIC_PORT=18181

WORKDIR="$(mktemp -d /tmp/rbi-perf.XXXXXX)"
DB_PATH="$WORKDIR/rbi-perf.db"
SERVER_LOG="$WORKDIR/server.log"

SERVER_PID=""
STATIC_PID=""

cleanup() {
  [[ -n "$SERVER_PID" ]] && kill "$SERVER_PID" 2>/dev/null
  [[ -n "$STATIC_PID" ]] && kill "$STATIC_PID" 2>/dev/null
  wait 2>/dev/null
  rm -rf "$WORKDIR"
}
trap cleanup EXIT INT TERM

echo "== Measuring: $LABEL ($WORKTREE_ROOT) ==" >&2

python3 -m http.server "$STATIC_PORT" --directory "$FIXTURES_DIR" --bind 127.0.0.1 >"$WORKDIR/static.log" 2>&1 &
STATIC_PID=$!
sleep 0.5

(
  cd "$SERVER_PROJECT"
  export ASPNETCORE_ENVIRONMENT=Development
  export ASPNETCORE_URLS="http://127.0.0.1:$HTTP_PORT"
  export ConnectionStrings__Sqlite="Data Source=$DB_PATH"
  export Proxy__Port="$PROXY_PORT"
  export Proxy__Bind="127.0.0.1"
  export WebRtc__UdpPortStart="$WEBRTC_UDP_START"
  export WebRtc__UdpPortEnd="$WEBRTC_UDP_END"
  export WebRtc__AdvertisedIp="127.0.0.1"
  export Logging__LogLevel__Default=Debug
  dotnet build -v quiet && exec dotnet run --no-build --no-launch-profile
) >"$SERVER_LOG" 2>&1 &
SERVER_PID=$!

BASE="http://127.0.0.1:$HTTP_PORT"
for _ in $(seq 1 60); do
  curl -fsS "$BASE/health" >/dev/null 2>&1 && break
  sleep 1
done
if ! curl -fsS "$BASE/health" >/dev/null 2>&1; then
  echo "Server never became healthy. Log:" >&2
  tail -n 80 "$SERVER_LOG" >&2
  exit 1
fi

TOKEN=$(curl -fsS -X POST "$BASE/api/admin/auth/login" -H "Content-Type: application/json" \
  -d '{"email":"perf@example.invalid","password":"perf-password-123"}' | jq -r '.token')
curl -fsS -X POST "$BASE/api/admin/sites" -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d '{"hostPattern":"127.0.0.1","viewMode":"VideoAllowInput"}' >/dev/null

# Drives one long-lived session so the transcode loop keeps running (and logging) for the full
# measurement window; the client's own connectivity assertions are irrelevant here.
dotnet run --project "$CLIENT_PROJECT" --no-build -- \
  --server-url "$BASE" \
  --target-url "http://127.0.0.1:$STATIC_PORT/$FIXTURE_PAGE" \
  --timeout-seconds "$DURATION" >"$WORKDIR/client.log" 2>&1 || true

python3 - "$SERVER_LOG" "$LABEL" <<'PYEOF'
import re, sys, statistics

log_path, label = sys.argv[1], sys.argv[2]
pattern = re.compile(r"Video frame \d+ for .*: jpeg \d+B -> vp8 \d+B in ([\d.]+)ms")
values = []
with open(log_path, errors="replace") as f:
    for line in f:
        m = pattern.search(line)
        if m:
            values.append(float(m.group(1)))

if not values:
    print(f"RESULT label={label} frames=0 median_ms=NA p95_ms=NA")
    sys.exit(0)

values.sort()
median = statistics.median(values)
p95 = values[int(len(values) * 0.95) - 1] if len(values) > 1 else values[0]
print(f"RESULT label={label} frames={len(values)} median_ms={median:.2f} p95_ms={p95:.2f} min_ms={values[0]:.2f} max_ms={values[-1]:.2f}")
PYEOF
