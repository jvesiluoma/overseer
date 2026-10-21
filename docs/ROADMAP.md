# Roadmap

| Milestone | Scope | Status |
|---|---|---|
| M0 Spikes | CGVirtualDisplay, SCK capture, libwebrtc loopback, car-browser smoke test | Mac parts **done**; the car-browser test is still open |
| M1 MVP (same network) | Server, signaling, our own web client, H.264 streaming, auto-sized display, touch input, menu bar | **Implemented**, and runs on macOS 26 (Apple silicon) with a browser on the LAN |
| M2 Security | Pairing on by default, lockout, Host/Origin checks, no CORS, no filesystem, no telemetry, HTTPS with a self-signed certificate (HTTP only redirects) | **Implemented** and tested |
| M3 Polish | Multi-display (≤4), stats HUD, launch at login, settings UI | **Implemented** (basic) |
| M4a Hotspot, no root | Detect macOS Internet Sharing (`bridge100+`) and iPhone Personal Hotspot, list the hotspot address first (QR code), setup guide in the menu. The phone provides internet, so the car's connectivity check passes on its own | **Implemented**; needs a test in a Tesla |
| M4b Hotspot with helper | Only if M4a fails in the car (private-IP block, or no internet at all): TEST-NET address, `go.ss` DNS, connectivity-probe answers, local-only hotspot, privileged helper (typed XPC, no shell, no /tmp) | Planned, depends on the M4a car test |
| M5 Release | Notarized builds, Homebrew cask, Sparkle (EdDSA) updates | Planned |

Ideas: keyboard forwarding from the device's on-screen keyboard, audio, clipboard sync (opt-in).

## Car test checklist (M4a)

1. Phone by USB → Mac Internet Sharing → Wi-Fi. Then, separately, the phone hotspot with both the Mac and the car joined.
2. Does the Tesla browser open `https://192.168.2.1` (or `https://172.20.10.x`)? If it shows a blank or blocked page, private addresses are blocked, which means M4b.
3. Does it let you continue past the self-signed certificate warning? If not, try "Allow unencrypted connections" and `http://`.
4. Does the car stay on the Wi-Fi, or drop it after its connectivity check?
5. Do video and touch work, and how does the latency feel?
