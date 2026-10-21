# Overscreen

Turn any browser into an extra display for your Mac: a car's browser, an iPad, an Android
tablet or an old laptop. Overscreen creates a real virtual monitor on macOS and streams it over
WebRTC on your local network. Touch input on the device controls the Mac.

- **No app on the device.** You open `https://<your-mac>` and enter the pairing code.
- **Local only and encrypted.** No account, cloud relay or telemetry. The page, pairing and
  signaling use HTTPS with the Mac's own certificate, and the video is DTLS-SRTP.
- **Low latency.** H.264 via VideoToolbox with libwebrtc congestion control. Touch, drag,
  double-click, two-finger scroll and right-click are supported.
- **Up to 4 displays,** each sized to the device that connects (or a fixed size, optionally HiDPI).

> Status: early development (v0.1). The cross-platform core (protocol, pairing, server, web
> client) is tested on Linux CI. The macOS layer (virtual display, capture, WebRTC, input, UI) is
> new and needs testing on real hardware. See [`docs/ROADMAP.md`](docs/ROADMAP.md).

## Requirements

- macOS 14 Sonoma or later (Apple silicon or Intel).
- Xcode 15+ / Swift 5.9+ to build.
- On first use: Screen Recording permission (to capture the virtual display), plus Accessibility
  permission if you want touch control.

## Download

Get `Overscreen-<version>-macOS.dmg` or `.zip` from the
[releases page](https://github.com/jvesiluoma/overseer/releases). There is no installer: open the
disk image and run Overscreen from it, or drag it to Applications. From the zip, unzip and
double-click. The app lives in the menu bar, not the Dock.

The builds are not notarized by Apple yet, so the first launch is blocked with "Apple could not
verify…". Open **System Settings → Privacy & Security**, scroll down and click **Open Anyway**
(or run `xattr -dr com.apple.quarantine /path/to/Overscreen.app`). Check the download against
`SHA256SUMS` from the same release first.

## Build

```sh
scripts/create-dev-cert.sh      # once: local signing cert, so permissions survive rebuilds
scripts/build-app.sh            # → build/Overscreen.app
open build/Overscreen.app
```

For distribution, set `SIGN_IDENTITY="Developer ID Application: …"` and notarize the result.

## Use

1. Click the menu bar icon and press **Start**.
2. On the other device, open one of the `https://` URLs shown, or scan the QR code. The browser
   warns about the self-signed certificate the first time. Check that the fingerprint matches the
   one in the menu, then continue.
3. Enter the 6-digit pairing code from the menu and pick a display.
4. The display goes full screen. Tap to click, hold and move to drag, use two fingers to scroll,
   and tap with two fingers to right-click. The small handle at the top opens a menu with stats
   and Disconnect.

## Using it in a car

The car's browser has to reach the Mac directly. Two setups need no extra software or root access:

- **Phone by USB (most reliable).** Connect the phone to the Mac and turn on USB tethering (on an
  iPhone, Personal Hotspot). In System Settings → General → Sharing → Internet Sharing, share the
  phone's connection to Wi-Fi with a network name and password, then turn it on. Join that
  network in the car. The Mac leaves its own Wi-Fi network while sharing.
- **Phone hotspot.** Join the phone's hotspot on both the Mac and the car. Some Android phones
  stop hotspot devices from reaching each other; use USB then.

The menu shows which of these it detects, puts the hotspot address first and points the QR code
at it. Both setups use a password-protected Wi-Fi network that only you control. If the car's
browser refuses the self-signed certificate, "Allow unencrypted connections" is a reasonable
fallback there, but not on shared networks. These setups have not been tested in a Tesla yet;
see the checklist in [`docs/ROADMAP.md`](docs/ROADMAP.md).

## Develop

The repository holds two Swift packages:

| Path | What | Platforms |
|---|---|---|
| `/` (`Package.swift`) | `OverscreenCore` (protocol, pairing, input mapping, display-mode maths) and `OverscreenServer` (SwiftNIO HTTP/WebSocket server, embedded web client), plus `overscreen-devserver` | macOS, Linux |
| `MacApp/` | `CVirtualDisplay` (ObjC shim), `OverscreenMac` (virtual display, ScreenCaptureKit, libwebrtc, CGEvent), `OverscreenApp` (SwiftUI menu bar) | macOS |

```sh
swift test                                   # core + server unit tests
swift build --product overscreen-devserver
node --test Tests/web/*.test.cjs            # gesture unit tests + end-to-end protocol tests
swift run overscreen-devserver --port 8080   # real server and web client with a fake media engine
```

The web client lives in `WebClient/`. After editing it, run `python3 -I scripts/embed-webclient.py`
(a test fails if you forget).

Docs: [`docs/PROTOCOL.md`](docs/PROTOCOL.md), [`docs/SECURITY.md`](docs/SECURITY.md).

## Clean-room note

Overscreen was written from a behavioural specification. It contains no code, assets or text
from any commercial product.

## License

GNU Affero General Public License v3.0 (see [`LICENSE`](LICENSE)). libwebrtc (BSD-3-Clause),
SwiftNIO and swift-crypto (Apache-2.0) keep their own licences.
