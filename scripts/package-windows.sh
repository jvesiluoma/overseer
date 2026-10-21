#!/usr/bin/env bash
# Builds the Windows host and packages a portable zip per architecture:
#   dist/Overscreen-<version>-Windows-x64.zip
#   dist/Overscreen-<version>-Windows-arm64.zip
# Each holds Overscreen.exe, ffmpeg.exe (pinned BtbN GPL build, checksum-verified) and notes.
# Runs on macOS, Linux or Windows (Git Bash) with Go installed. Usage:
#   VERSION=0.2.0 scripts/package-windows.sh
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
VERSION="${VERSION:-$(/usr/libexec/PlistBuddy -c 'Print CFBundleShortVersionString' "$ROOT/MacApp/Resources/Info.plist" 2>/dev/null || echo dev)}"
DIST="$ROOT/dist"
CACHE="$ROOT/build/ffmpeg-cache"

# FFmpeg from https://github.com/BtbN/FFmpeg-Builds (GPL build: includes libx264 as the software
# fallback encoder). Pinned to one autobuild; bump the tag, names and hashes together.
FFMPEG_TAG="autobuild-2026-09-30-13-08"
FFMPEG_VERSION="n8.1.3-9-g29e619e767"
ffmpeg_sha256() {
  case "$1" in
    win64) echo "7b801cdd3a1a0bb54ae6f572187e68b4ed54f52086cfee133fab2180bfb429fa" ;;
    winarm64) echo "061a770557c30bbf8e0b6ca3810288a60f72b85088378b84be03277746f34f17" ;;
  esac
}

sha256() { if command -v sha256sum >/dev/null; then sha256sum "$1" | cut -d' ' -f1; else shasum -a 256 "$1" | cut -d' ' -f1; fi; }

fetch_ffmpeg() { # $1 = win64 | winarm64; prints the path of the verified zip
  local name="ffmpeg-$FFMPEG_VERSION-$1-gpl-8.1.zip" file
  file="$CACHE/$name"
  mkdir -p "$CACHE"
  if [[ ! -f "$file" ]]; then
    curl -fsSL -o "$file.part" "https://github.com/BtbN/FFmpeg-Builds/releases/download/$FFMPEG_TAG/$name" >&2
    mv "$file.part" "$file"
  fi
  local got
  got="$(sha256 "$file")"
  if [[ "$got" != "$(ffmpeg_sha256 "$1")" ]]; then
    rm -f "$file"
    echo "checksum mismatch for $name: $got" >&2
    exit 1
  fi
  echo "$file"
}

package() { # $1 = Go arch, $2 = FFmpeg arch, $3 = label
  local stage="$ROOT/build/windows-$1/Overscreen" zip
  rm -rf "$stage" && mkdir -p "$stage"
  (cd "$ROOT" && GOOS=windows GOARCH="$1" CGO_ENABLED=0 go build -trimpath \
    -ldflags "-H windowsgui -s -w -X main.version=$VERSION" -o "$stage/Overscreen.exe" ./WindowsHost/cmd/overscreen)

  zip="$(fetch_ffmpeg "$2")"
  local tmp
  tmp="$(mktemp -d)"
  unzip -q -j "$zip" "*/bin/ffmpeg.exe" "*/LICENSE.txt" -d "$tmp"
  mv "$tmp/ffmpeg.exe" "$stage/ffmpeg.exe"
  mv "$tmp/LICENSE.txt" "$stage/FFMPEG-LICENSE.txt"
  rm -rf "$tmp"
  cp "$ROOT/LICENSE" "$stage/LICENSE.txt"
  sed -e "s/@VERSION@/$VERSION/g" -e "s/@FFMPEG_TAG@/$FFMPEG_TAG/g" -e "s/@FFMPEG_VERSION@/$FFMPEG_VERSION/g" \
    "$ROOT/WindowsHost/README-Windows.txt" | sed 's/$/\r/' > "$stage/README.txt"

  mkdir -p "$DIST"
  local out="$DIST/Overscreen-$VERSION-Windows-$3.zip"
  rm -f "$out"
  (cd "$(dirname "$stage")" && zip -q -r -X "$out" Overscreen)
  echo "built $out"
}

package amd64 win64 x64
package arm64 winarm64 arm64
(cd "$DIST" && for f in Overscreen-"$VERSION"-Windows-*.zip; do echo "$(sha256 "$f")  $f"; done)
