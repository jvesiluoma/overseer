#!/usr/bin/env bash
# Builds the app and packages it for a GitHub release:
#   dist/Overscreen-<version>-macOS.dmg   (drag to Applications, or run straight from the image)
#   dist/Overscreen-<version>-macOS.zip   (unzip and double-click; nothing to install)
#   dist/SHA256SUMS
# Signing follows scripts/build-app.sh (SIGN_IDENTITY, local signing cert, or ad-hoc).
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
DIST="$ROOT/dist"
APP="$ROOT/build/Overscreen.app"

[[ "${SKIP_BUILD:-0}" == 1 ]] || "$ROOT/scripts/build-app.sh"

VERSION="$(/usr/libexec/PlistBuddy -c 'Print CFBundleShortVersionString' "$APP/Contents/Info.plist")"
NAME="Overscreen-$VERSION-macOS"
rm -rf "$DIST"
mkdir -p "$DIST"

# ditto keeps the framework symlinks and code signatures intact (plain zip does not).
ditto -c -k --keepParent "$APP" "$DIST/$NAME.zip"

STAGE="$(mktemp -d)"
trap 'rm -rf "$STAGE"' EXIT
ditto "$APP" "$STAGE/Overscreen.app"
ln -s /Applications "$STAGE/Applications"
hdiutil create -quiet -volname "Overscreen $VERSION" -srcfolder "$STAGE" -fs HFS+ -format UDZO "$DIST/$NAME.dmg"

(cd "$DIST" && shasum -a 256 "$NAME.dmg" "$NAME.zip" > SHA256SUMS)
cat "$DIST/SHA256SUMS"
