#!/usr/bin/env bash
# Generates a root Certificate Authority for the RemoteBrowserIsolation TLS-intercepting proxy.
#
# Produces three files in ./certs (gitignored -- private key material):
#   rootCA.key  RSA private key (keep secret; never share/commit)
#   rootCA.crt  public certificate (PEM) -- IMPORT INTO YOUR BROWSER/OS trust store
#   rootCA.pfx  PKCS#12 bundle (key+cert, password-protected) -- UPLOAD TO THE APP
#
# The app's leaf minter (LeafCertificateMinter) only supports RSA-signed CAs, so this
# script forces RSA. The CA gets basicConstraints CA:true + keyUsage keyCertSign,cRLSign,
# which browsers require to trust it as an issuer.
#
# Usage:
#   ./scripts/generate_root_ca.sh [-p PASSWORD] [-d DAYS] [-n "COMMON NAME"] [-b KEYBITS]
# Env fallbacks: CA_PASSWORD, CA_DAYS, CA_CN, CA_BITS.
# If no password is given, a random one is generated and printed at the end.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
CERTS_DIR="$ROOT_DIR/certs"

PASSWORD="${CA_PASSWORD:-}"
DAYS="${CA_DAYS:-3650}"
CN="${CA_CN:-RemoteBrowserIsolation Root CA}"
BITS="${CA_BITS:-4096}"

# Parse flags (override env).
while getopts ":p:d:n:b:h" opt; do
  case "$opt" in
    p) PASSWORD="$OPTARG" ;;
    d) DAYS="$OPTARG" ;;
    n) CN="$OPTARG" ;;
    b) BITS="$OPTARG" ;;
    h) grep '^#' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
    \?) echo "Unknown option -$OPTARG" >&2; exit 2 ;;
    :) echo "Option -$OPTARG needs an argument" >&2; exit 2 ;;
  esac
done

command -v openssl >/dev/null || { echo "openssl not found on PATH" >&2; exit 1; }

# Generate a random password if none supplied, so the PFX is never left unprotected.
GENERATED=0
if [[ -z "$PASSWORD" ]]; then
  PASSWORD="$(openssl rand -base64 18)"
  GENERATED=1
fi

mkdir -p "$CERTS_DIR"
KEY="$CERTS_DIR/rootCA.key"
CRT="$CERTS_DIR/rootCA.crt"
PFX="$CERTS_DIR/rootCA.pfx"

# Refuse to clobber an existing CA silently -- overwriting invalidates every already-minted
# leaf and every browser that already trusts the old cert.
if [[ -e "$KEY" || -e "$CRT" || -e "$PFX" ]]; then
  echo "Refusing to overwrite existing CA files in $CERTS_DIR (rootCA.key/crt/pfx)." >&2
  echo "Delete them first if you really want a new CA." >&2
  exit 1
fi

# 1) RSA private key.
openssl genrsa -out "$KEY" "$BITS" 2>/dev/null

# 2) Self-signed root cert with CA extensions browsers require.
openssl req -x509 -new -nodes -key "$KEY" -sha256 -days "$DAYS" -out "$CRT" \
  -subj "/CN=$CN" \
  -addext "basicConstraints=critical,CA:TRUE" \
  -addext "keyUsage=critical,keyCertSign,cRLSign"

# 3) PKCS#12 bundle for upload (key + cert, encrypted with PASSWORD).
openssl pkcs12 -export -inkey "$KEY" -in "$CRT" -out "$PFX" \
  -name "$CN" -passout "pass:$PASSWORD"

chmod 600 "$KEY" "$PFX"

echo
echo "Root CA generated in $CERTS_DIR :"
echo "  rootCA.crt  -> import into your browser / OS trust store"
echo "  rootCA.pfx  -> upload in the app admin (Root CA) with the password below"
echo "  rootCA.key  -> private key, keep secret"
echo
if [[ "$GENERATED" -eq 1 ]]; then
  echo "PFX password (generated, save it now): $PASSWORD"
else
  echo "PFX password: (the one you supplied)"
fi
