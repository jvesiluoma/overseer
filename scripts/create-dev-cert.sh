#!/usr/bin/env bash
# Creates a self-signed code signing certificate ("Overscreen Local Signing") in its own keychain,
# ~/Library/Keychains/overscreen-signing.keychain-db. build-app.sh uses it automatically.
#
# Why: an ad-hoc signature identifies the app by the hash of its binary, so every rebuild looks
# like a new app to macOS and Screen Recording / Accessibility permissions are lost. With a stable
# certificate the identity is "bundle id + certificate" and permissions survive rebuilds.
#
# A separate keychain with a generated password (kept next to it, readable only by you) lets
# codesign use the key without login keychain prompts. Local use only; for distribution sign
# with a Developer ID instead.
#
# Remove with:
#   security delete-keychain ~/Library/Keychains/overscreen-signing.keychain-db
#   rm ~/Library/Keychains/overscreen-signing.password
set -euo pipefail

NAME="Overscreen Local Signing"
KEYCHAIN="$HOME/Library/Keychains/overscreen-signing.keychain-db"
PASSFILE="$HOME/Library/Keychains/overscreen-signing.password"

if [[ -f "$KEYCHAIN" ]]; then
  echo "$KEYCHAIN already exists"
  exit 0
fi

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

cat > "$TMP/cert.conf" <<EOF
[req]
distinguished_name = dn
x509_extensions = ext
prompt = no
[dn]
CN = $NAME
[ext]
basicConstraints = critical, CA:false
keyUsage = critical, digitalSignature
extendedKeyUsage = critical, codeSigning
EOF

openssl req -x509 -newkey rsa:2048 -nodes -days 3650 -config "$TMP/cert.conf" \
  -keyout "$TMP/key.pem" -out "$TMP/cert.pem" 2>/dev/null
P12PASS="$(openssl rand -hex 16)"
openssl pkcs12 -export -inkey "$TMP/key.pem" -in "$TMP/cert.pem" -name "$NAME" \
  -out "$TMP/cert.p12" -passout "pass:$P12PASS"

(umask 077; openssl rand -hex 24 > "$PASSFILE")
KCPASS="$(cat "$PASSFILE")"
security create-keychain -p "$KCPASS" "$KEYCHAIN"
security set-keychain-settings "$KEYCHAIN"   # no auto-lock timeout
security unlock-keychain -p "$KCPASS" "$KEYCHAIN"
security import "$TMP/cert.p12" -k "$KEYCHAIN" -P "$P12PASS" -T /usr/bin/codesign >/dev/null
# Allow Apple tools (codesign) to use the key without a confirmation dialog.
security set-key-partition-list -S apple-tool:,apple: -s -k "$KCPASS" "$KEYCHAIN" >/dev/null

# codesign looks up identities in the user search list, so add the keychain to it.
existing=()
while IFS= read -r k; do
  k="${k//\"/}"; k="${k#"${k%%[![:space:]]*}"}"
  [[ -n "$k" ]] && existing+=("$k")
done < <(security list-keychains -d user)
security list-keychains -d user -s "${existing[@]}" "$KEYCHAIN"

echo "created \"$NAME\" in $KEYCHAIN"
