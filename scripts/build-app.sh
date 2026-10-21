#!/usr/bin/env bash
# Builds build/Overscreen.app (universal unless ARCHS is set) and signs it.
#   scripts/build-app.sh                       # local use: "Overscreen Local Signing" if present
#                                              # (scripts/create-dev-cert.sh), else ad-hoc
#   SIGN_IDENTITY="Developer ID Application: …" scripts/build-app.sh   # for distribution/notarization
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
MAC="$ROOT/MacApp"
OUT="$ROOT/build"
APP="$OUT/Overscreen.app"
ARCHS="${ARCHS:-arm64 x86_64}"
LOCAL_IDENTITY="Overscreen Local Signing"
if [[ -z "${SIGN_IDENTITY:-}" ]]; then
  LOCAL_KEYCHAIN="$HOME/Library/Keychains/overscreen-signing.keychain-db"
  if [[ -f "$LOCAL_KEYCHAIN" ]]; then
    SIGN_IDENTITY="$LOCAL_IDENTITY"
    security unlock-keychain -p "$(cat "$HOME/Library/Keychains/overscreen-signing.password")" "$LOCAL_KEYCHAIN"
  else
    SIGN_IDENTITY="-"
    echo "note: ad-hoc signing; macOS permissions reset on every rebuild. Run scripts/create-dev-cert.sh once to avoid that." >&2
  fi
fi

[[ "$(uname)" == "Darwin" ]] || { echo "build-app.sh must run on macOS" >&2; exit 1; }

python3 -I "$ROOT/scripts/embed-webclient.py"

arch_flags=()
for a in $ARCHS; do arch_flags+=(--arch "$a"); done
swift build --package-path "$MAC" -c release "${arch_flags[@]}"
BIN_DIR="$(swift build --package-path "$MAC" -c release "${arch_flags[@]}" --show-bin-path)"

rm -rf "$APP"
mkdir -p "$APP/Contents/MacOS" "$APP/Contents/Frameworks" "$APP/Contents/Resources"
cp "$BIN_DIR/Overscreen" "$APP/Contents/MacOS/Overscreen"
cp "$MAC/Resources/Info.plist" "$APP/Contents/Info.plist"

# WebRTC.framework comes from the SwiftPM binary artifact.
FRAMEWORK="$(find "$BIN_DIR" "$MAC/.build/artifacts" -type d -name WebRTC.framework -path '*macos*' -print -quit 2>/dev/null || true)"
[[ -n "$FRAMEWORK" ]] || FRAMEWORK="$(find "$BIN_DIR" -maxdepth 2 -type d -name WebRTC.framework -print -quit)"
[[ -n "$FRAMEWORK" ]] || { echo "WebRTC.framework not found" >&2; exit 1; }
cp -R "$FRAMEWORK" "$APP/Contents/Frameworks/"
install_name_tool -add_rpath "@executable_path/../Frameworks" "$APP/Contents/MacOS/Overscreen" 2>/dev/null || true

# Hardened runtime enforces library validation (same Team ID for app and frameworks), which ad-hoc
# and self-signed certificates cannot satisfy: dyld would refuse to load WebRTC.framework. Enable it
# only for Apple-issued identities (required for notarization anyway).
sign_opts=(--force --timestamp=none --sign "$SIGN_IDENTITY")
[[ "$SIGN_IDENTITY" == "-" || "$SIGN_IDENTITY" == "$LOCAL_IDENTITY" ]] || sign_opts+=(--options runtime)
codesign "${sign_opts[@]}" "$APP/Contents/Frameworks/WebRTC.framework"
codesign "${sign_opts[@]}" \
  --entitlements "$MAC/Resources/Overscreen.entitlements" "$APP"
codesign --verify --strict --verbose=2 "$APP"
echo "built $APP"
