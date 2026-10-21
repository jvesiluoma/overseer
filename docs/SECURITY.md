# Security model

Overscreen streams part of your desktop and accepts mouse input over the network. Treat it
like a remote-desktop server.

## Threats considered

| Attacker | Defence |
|---|---|
| Someone on the same network listens in, or tampers with traffic (man in the middle) | The web client, pairing API and signaling WebSocket are served **only over TLS** (TLS 1.2+). The host makes a self-signed P-256 certificate on first start and keeps it in `~/Library/Application Support/Overscreen/` (key file `0600`), so its SHA-256 fingerprint stays stable. The menu shows that fingerprint, so the user can check it against the browser's certificate warning on first visit. The plain HTTP port answers every request with a `307` redirect to HTTPS and serves no API or WebSocket. Media is DTLS-SRTP, and the DTLS fingerprints travel inside the TLS-protected signaling, so a network attacker cannot swap them. |
| Someone else on the same Wi-Fi tries to watch or control the display | Pairing is **on by default**. A 6-digit code is shown only on the Mac and rotates after each use. There is a per-IP lockout with exponential backoff, a global failure budget that rotates the code, and a constant-time comparison. Every API call except `info` and `pair` needs a 128-bit token. |
| A web page the device or the Mac visits tries to drive the local server (CSRF, DNS rebinding) | No CORS headers. `/api/pair` only accepts `application/json`, which a page cannot send cross-origin without a CORS preflight. The `Host` header allowlist rejects rebinding domains. The WebSocket upgrade checks `Origin` against `Host`. The CSP forbids inline script and framing. |
| A malicious request path (traversal / file read) | Static assets are compiled into the binary as an in-memory table, and the server never touches the filesystem. |
| A malformed or hostile client message | Strict JSON decoding with size caps (64 KiB signaling, 1 KiB input) and range checks on viewport, display index and coordinates. Input is clamped to the virtual display. The connection closes after repeated protocol errors. |
| A paired device misbehaving | "Forget all devices" revokes every token. Input can be switched off (view-only). A new connection on a display replaces the old one, and the menu bar shows who is connected. |
| Local privilege escalation | **No privileged helper, no root code and no shell scripts.** The app runs as the user with the hardened runtime, without library-validation exemptions or debugger entitlements. (The planned hotspot mode will add a helper. Its rules are in `PLAN` M4: typed XPC commands, code-signing requirements on both sides, and no `/tmp`.) |
| Phoning home | None. No telemetry, update check, licence server or cloud signaling. |

## Windows host

The Windows host implements the same protocol and defences (pairing, lockout, Host/Origin checks,
TLS with a self-signed certificate kept in `%APPDATA%\Overscreen`, no CORS, fixed asset table),
and is covered by the same protocol tests. Differences:

- **No privileged code.** It runs as the user. Extend mode uses the Virtual Display Driver, which
  the user installs separately; its control pipe is open to every local user by the driver's
  design, and Overscreen only sends it `SETDISPLAYCOUNT` and `RELOAD_DRIVER`. To offer the
  browser's exact size, Overscreen appends a resolution to the driver's `vdd_settings.xml`.
- **FFmpeg** runs as a child process and sends RTP to a loopback UDP port; packets from other
  addresses are dropped.
- **Status page** (`/host`, with the pairing code) is served only to loopback clients.
- **Input** cannot reach elevated windows (UIPI), and the secure desktop (UAC, lock screen) is not
  captured.

## Known limitations

- **Self-signed certificate.** Browsers warn on first visit, and the protection depends on the
  user checking the fingerprint before accepting it. Someone who is already intercepting traffic
  at that moment could present their own certificate. After that, the browser remembers the
  accepted certificate, and a different one triggers a new warning.
- **Typed `http://` addresses.** The first redirect from the plain port is not protected, so an
  active attacker could intercept it. Use the `https://` address or the QR code.
- **"Allow unencrypted connections"** (off by default) serves everything over plain HTTP too, for
  browsers that refuse self-signed certificates. With it on, anyone on the network can read the
  pairing code and token and take over the session. The page and the menu show a warning.
- **Token location.** The token lives in the browser's `localStorage`, so anyone who can use that
  browser profile can reconnect until the tokens are revoked.
- **Private API.** `CGVirtualDisplay` is a private Apple API. It is only resolved at runtime and
  never handed untrusted data beyond validated sizes.

## Reporting

Please report vulnerabilities privately through GitHub Security Advisories on the repository.
