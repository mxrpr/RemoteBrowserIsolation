#!/usr/bin/env bash
# End-to-end smoke test for RemoteBrowserIsolation.Server, covering all four ViewMode policy
# settings (HtmlAllowInput, HtmlNoInput, VideoAllowInput, VideoNoInput).
#
# Spins up the real server against a throwaway SQLite DB and non-default ports (never touches
# the developer's own ./data/rbi.db or default ports), seeds it with a freshly bootstrapped
# admin account, a freshly generated (throwaway) root CA, and four site policies -- one per
# ViewMode -- then drives each mode end to end:
#   - HtmlAllowInput / HtmlNoInput: real HTTPS request through the TLS-intercepting proxy
#     (curl --proxy, trusting the throwaway CA), checking real content came back and that the
#     HtmlNoInput CSS-nudge marker is present only for that mode.
#   - VideoAllowInput / VideoNoInput: a small SIPSorcery-based WebRTC offerer test client
#     (tests/e2e/WebRtcTestClient) negotiates a real session, confirms VP8 RTP packets are
#     flowing, and sends a keydown over the input data channel; a tiny local HTTP fixture page
#     (tests/e2e/fixtures/keytest_server.py) records whether that keydown actually reached the
#     page's DOM. VideoAllowInput must see it; VideoNoInput's server-side keyboard drop
#     (InputEventForwarder) must mean it never arrives -- this is the real behavioural
#     difference between the two modes, verified without any pixel/video decoding.
#
# Everything this script creates (temp DB, temp CA/certs, logs, PID files, the fixture server)
# is removed on exit -- success, failure, or Ctrl-C -- via the EXIT trap below.
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
SERVER_PROJECT="$REPO_ROOT/src/RemoteBrowserIsolation.Server"
CLIENT_PROJECT="$SCRIPT_DIR/WebRtcTestClient/WebRtcTestClient.csproj"

# --- Isolated ports (all far from the app's own defaults: Kestrel 5139, proxy 8080, WebRTC
# 40000-40009) so this run never collides with a developer's already-running instance. ---
HTTP_PORT=15139
PROXY_PORT=18080
WEBRTC_UDP_START=41000
WEBRTC_UDP_END=41009
FIXTURE_PORT=18081

WORKDIR="$(mktemp -d /tmp/rbi-e2e.XXXXXX)"
DB_PATH="$WORKDIR/rbi-e2e.db"
CERTS_DIR="$WORKDIR/certs"
SERVER_LOG="$WORKDIR/server.log"
FIXTURE_LOG="$WORKDIR/fixture.log"
CA_PASSWORD="e2e-test-only-$(date +%s)"
ADMIN_EMAIL="e2e-admin@example.invalid"
ADMIN_PASSWORD="e2e-password-$(date +%s)"

SERVER_PID=""
FIXTURE_PID=""
PASS_COUNT=0
FAIL_COUNT=0
declare -a RESULT_LINES=()

# Tears down everything this script started and deletes every file it created, regardless of
# how the script exits (normal completion, assertion failure, or interrupt).
cleanup() {
  if [[ -n "$SERVER_PID" ]] && kill -0 "$SERVER_PID" 2>/dev/null; then
    kill "$SERVER_PID" 2>/dev/null
    wait "$SERVER_PID" 2>/dev/null
  fi
  if [[ -n "$FIXTURE_PID" ]] && kill -0 "$FIXTURE_PID" 2>/dev/null; then
    kill "$FIXTURE_PID" 2>/dev/null
    wait "$FIXTURE_PID" 2>/dev/null
  fi
  rm -rf "$WORKDIR"
}
trap cleanup EXIT INT TERM

record() {
  local status="$1" name="$2" detail="$3"
  if [[ "$status" == "PASS" ]]; then
    PASS_COUNT=$((PASS_COUNT + 1))
  else
    FAIL_COUNT=$((FAIL_COUNT + 1))
  fi
  RESULT_LINES+=("$(printf '%-4s %-28s %s' "$status" "$name" "$detail")")
  echo "[$status] $name -- $detail"
}

echo "== RBI end-to-end test =="
echo "Work dir: $WORKDIR (deleted on exit)"
mkdir -p "$CERTS_DIR"

# --- 1. Build ---------------------------------------------------------------
echo; echo "-- Building server --"
if ! (cd "$SERVER_PROJECT" && dotnet build -v quiet); then
  echo "Server build failed." >&2
  exit 1
fi

echo; echo "-- Building WebRTC test client --"
if ! (cd "$SCRIPT_DIR/WebRtcTestClient" && dotnet build -v quiet); then
  echo "WebRtcTestClient build failed." >&2
  exit 1
fi

# --- 2. Generate a throwaway root CA (not the repo's scripts/generate_root_ca.sh -- that
# script writes to ./certs and refuses to overwrite an existing CA; this test needs a fresh,
# disposable one every run, so it's generated inline straight into $WORKDIR). -----------------
echo; echo "-- Generating throwaway root CA --"
openssl genrsa -out "$CERTS_DIR/rootCA.key" 2048 2>/dev/null
openssl req -x509 -new -nodes -key "$CERTS_DIR/rootCA.key" -sha256 -days 1 \
  -out "$CERTS_DIR/rootCA.crt" -subj "/CN=RBI E2E Test Root CA" \
  -addext "basicConstraints=critical,CA:TRUE" \
  -addext "keyUsage=critical,keyCertSign,cRLSign" 2>/dev/null
openssl pkcs12 -export -inkey "$CERTS_DIR/rootCA.key" -in "$CERTS_DIR/rootCA.crt" \
  -out "$CERTS_DIR/rootCA.pfx" -name "RBI E2E Test Root CA" -passout "pass:$CA_PASSWORD" 2>/dev/null

# --- 3. Start the fixture server (keytest.html + keydown beacon) ------------
echo; echo "-- Starting local keytest fixture server on :$FIXTURE_PORT --"
python3 "$SCRIPT_DIR/fixtures/keytest_server.py" "$FIXTURE_PORT" >"$FIXTURE_LOG" 2>&1 &
FIXTURE_PID=$!
sleep 0.5

# --- 4. Start the RBI server against the throwaway DB/ports -----------------
echo; echo "-- Starting RBI server (isolated DB + ports) --"
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
  exec dotnet run --no-build --no-launch-profile
) >"$SERVER_LOG" 2>&1 &
SERVER_PID=$!

BASE="http://127.0.0.1:$HTTP_PORT"
echo "Waiting for $BASE/health ..."
UP=0
for _ in $(seq 1 60); do
  if curl -fsS "$BASE/health" >/dev/null 2>&1; then
    UP=1
    break
  fi
  if ! kill -0 "$SERVER_PID" 2>/dev/null; then
    echo "Server process exited early. Log:" >&2
    cat "$SERVER_LOG" >&2
    exit 1
  fi
  sleep 1
done
if [[ "$UP" -ne 1 ]]; then
  echo "Server did not become healthy in time. Log:" >&2
  cat "$SERVER_LOG" >&2
  exit 1
fi
echo "Server is up."

# --- 5. Bootstrap admin account (fresh DB -> first login call creates it) ---
echo; echo "-- Bootstrapping admin account --"
LOGIN_RESPONSE=$(curl -fsS -X POST "$BASE/api/admin/auth/login" \
  -H "Content-Type: application/json" \
  -d "{\"email\":\"$ADMIN_EMAIL\",\"password\":\"$ADMIN_PASSWORD\"}")
TOKEN=$(echo "$LOGIN_RESPONSE" | jq -r '.token')
if [[ -z "$TOKEN" || "$TOKEN" == "null" ]]; then
  echo "Admin bootstrap failed: $LOGIN_RESPONSE" >&2
  exit 1
fi
AUTH_HEADER="Authorization: Bearer $TOKEN"

# --- 6. Upload the throwaway root CA -----------------------------------------
echo; echo "-- Uploading root CA --"
CA_UPLOAD=$(curl -fsS -X POST "$BASE/api/admin/rootca" -H "$AUTH_HEADER" \
  -F "pfx=@$CERTS_DIR/rootCA.pfx" -F "password=$CA_PASSWORD")
echo "$CA_UPLOAD" | jq -e '.thumbprint' >/dev/null || { echo "CA upload failed: $CA_UPLOAD" >&2; exit 1; }

# --- 7. Seed one site policy per ViewMode ------------------------------------
# HTML modes point at real public sites (IANA reserved example domains: stable, minimal,
# no anti-bot/rate-limit concerns). Video modes point at the local keytest fixture under two
# different hostnames (127.0.0.1 vs localhost) that resolve to the same fixture server, since
# SitePolicy.HostPattern is a unique-per-host string, not a port -- this lets AllowInput and
# NoInput each get their own policy row against the same underlying page.
create_policy() {
  local host="$1" mode="$2"
  local resp
  resp=$(curl -fsS -X POST "$BASE/api/admin/sites" -H "$AUTH_HEADER" -H "Content-Type: application/json" \
    -d "{\"hostPattern\":\"$host\",\"viewMode\":\"$mode\"}")
  echo "$resp" | jq -e '.id' >/dev/null || { echo "Failed to create policy $host -> $mode: $resp" >&2; exit 1; }
}
echo; echo "-- Seeding site policies --"
create_policy "example.com" "HtmlAllowInput"
create_policy "example.org" "HtmlNoInput"
create_policy "127.0.0.1" "VideoAllowInput"
create_policy "localhost" "VideoNoInput"

# --- 8. HtmlAllowInput: real content relayed, no CSS-nudge marker -----------
echo; echo "-- Testing HtmlAllowInput (example.com) --"
BODY=$(curl -fsS --proxy "http://127.0.0.1:$PROXY_PORT" --cacert "$CERTS_DIR/rootCA.crt" "https://example.com/" || true)
if [[ "$BODY" == *"Example Domain"* ]] && [[ "$BODY" != *"pointer-events:none!important"* ]]; then
  record PASS "HtmlAllowInput" "real content relayed, no no-input marker"
else
  record FAIL "HtmlAllowInput" "content check failed (len=${#BODY})"
fi

# --- 9. HtmlNoInput: real content relayed, WITH CSS-nudge marker ------------
echo; echo "-- Testing HtmlNoInput (example.org) --"
BODY=$(curl -fsS --proxy "http://127.0.0.1:$PROXY_PORT" --cacert "$CERTS_DIR/rootCA.crt" "https://example.org/" || true)
if [[ "$BODY" == *"Example Domain"* ]] && [[ "$BODY" == *"pointer-events:none!important"* ]]; then
  record PASS "HtmlNoInput" "real content relayed, no-input marker present"
else
  record FAIL "HtmlNoInput" "content/marker check failed (len=${#BODY})"
fi

# --- 10. VideoAllowInput: session connects, video flows, keydown reaches the page ----
echo; echo "-- Testing VideoAllowInput (127.0.0.1 keytest) --"
ALLOW_RESULT=$(dotnet run --project "$CLIENT_PROJECT" --no-build -- \
  --server-url "$BASE" \
  --target-url "http://127.0.0.1:$FIXTURE_PORT/keytest.html?token=allow" \
  --timeout-seconds 20 2>&1 | tee -a "$WORKDIR/video-allow.log" | grep '^RESULT ' || true)
sleep 1
BEACONS=$(curl -fsS "http://127.0.0.1:$FIXTURE_PORT/results")
if [[ "$ALLOW_RESULT" == *'"connected":true'* ]] && [[ "$ALLOW_RESULT" == *'"videoPacketsReceived":'* ]] \
  && [[ "$ALLOW_RESULT" != *'"videoPacketsReceived":0'* ]] && [[ "$BEACONS" == *"allow"* ]]; then
  record PASS "VideoAllowInput" "video flowed + keydown reached page ($ALLOW_RESULT)"
else
  record FAIL "VideoAllowInput" "video/keydown check failed ($ALLOW_RESULT / beacons=$BEACONS)"
fi

# --- 11. VideoNoInput: session connects, video flows, keydown must NOT reach the page ----
echo; echo "-- Testing VideoNoInput (localhost keytest) --"
NOINPUT_RESULT=$(dotnet run --project "$CLIENT_PROJECT" --no-build -- \
  --server-url "$BASE" \
  --target-url "http://localhost:$FIXTURE_PORT/keytest.html?token=noinput" \
  --timeout-seconds 20 2>&1 | tee -a "$WORKDIR/video-noinput.log" | grep '^RESULT ' || true)
sleep 1
BEACONS=$(curl -fsS "http://127.0.0.1:$FIXTURE_PORT/results")
if [[ "$NOINPUT_RESULT" == *'"connected":true'* ]] && [[ "$NOINPUT_RESULT" == *'"videoPacketsReceived":'* ]] \
  && [[ "$NOINPUT_RESULT" != *'"videoPacketsReceived":0'* ]] && [[ "$BEACONS" != *"noinput"* ]]; then
  record PASS "VideoNoInput" "video flowed + keydown correctly blocked ($NOINPUT_RESULT)"
else
  record FAIL "VideoNoInput" "video/block check failed ($NOINPUT_RESULT / beacons=$BEACONS)"
fi

# --- Report -------------------------------------------------------------------
echo
echo "== Results =========================================="
for line in "${RESULT_LINES[@]}"; do echo "$line"; done
echo "======================================================"
echo "$PASS_COUNT passed, $FAIL_COUNT failed"

if [[ "$FAIL_COUNT" -gt 0 ]]; then
  echo
  echo "Server log tail (for debugging):"
  tail -n 60 "$SERVER_LOG"
  exit 1
fi
exit 0
