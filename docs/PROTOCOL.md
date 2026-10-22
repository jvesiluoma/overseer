# Overscreen protocol (v1)

How the browser client and the macOS host talk. Everything is local-network only; there are
no cloud services, STUN or TURN servers.

```
browser                                   host (Mac)
  GET /            ───────────────────►   web client (embedded in the binary)
  GET /api/info    ───────────────────►   name, displays, pairingRequired
  POST /api/pair {code} ──────────────►   {token}            (code shown in the menu bar)
  WSS /ws?token=…  ◄══════════════════►   signaling (JSON)
  WebRTC           ◄══ H.264 video ════   virtual display
                   ══ "input" DataChannel ══►  mouse events
```

## HTTP

The host listens on two ports:
- **HTTPS** (default `443`, fallback `8443`) serves everything below over TLS 1.2+. It uses a
  self-signed certificate whose SHA-256 fingerprint is shown in the host's menu.
- **HTTP** (default `80`, fallback `8080`) answers every request with `307` and
  `Location: https://<host>[:<https port>]<path>`. It serves no API or WebSocket. The only
  exception is "Allow unencrypted connections", which makes it serve the full protocol.

Every response carries `Cache-Control: no-store`, `X-Content-Type-Options: nosniff`,
`X-Frame-Options: DENY`, `Referrer-Policy: no-referrer` and a strict `Content-Security-Policy`.
There are **no CORS headers**. Connections are closed after each response.

The `Host` header must be an IP literal, `localhost`, a `*.local` name or one of the configured extra names
(`go.ss` by default). Anything else gets `421` (this defends against DNS rebinding).

| Route | Method | Notes |
|---|---|---|
| `/`, `/index.html`, `/client.js`, `/gestures.js`, `/style.css` | GET/HEAD | Fixed in-memory table. Anything else is `404`, and the filesystem is never touched. |
| `/api/info` | GET | `{"name","version","protocol":1,"pairingRequired":bool,"displays":[{"id","name","inUse"}]}` |
| `/api/pair` | POST, `Content-Type: application/json` | `{"code":"123456"}` → `200 {"token"}` · `401 {"error":"invalid_code","remainingAttempts"}` · `429 {"error":"locked_out","retryAfter"}` + `Retry-After` · `415` if the body is not JSON. With pairing disabled, any body gets a token. |
| `/api/auth` | GET, `Authorization: Bearer <token>` | `204` if valid, else `401` |
| `/ws?token=<token>` | WebSocket upgrade | Refused with `401` unless the token is valid, `Host` is allowed and `Origin` (if present) matches `Host`. |

### Pairing rules
- The code is 6 digits, comes from a CSPRNG and is shown in the menu bar.
- The code **rotates after every successful pairing** and whenever 20 failures happen within 60 s across all clients.
- Each client IP gets 5 attempts, then a lockout of 30 s that doubles on each repeat, capped at 15 min.
- Tokens are 128-bit random hex values, kept in memory only (at most 32), and "Forget all devices" revokes them.
  The browser keeps its token in `localStorage`.

## Signaling (WebSocket, JSON text frames)

Client → host:

| type | fields | |
|---|---|---|
| `hello` | `display` (int), `viewport` `{width,height,dpr}` (CSS px) | Must be the first message. Starts a session; a previous session on the same display is ended with `bye{reason:"replaced"}`. |
| `answer` | `sdp` | |
| `candidate` | `candidate`, `sdpMid`, `sdpMLineIndex` | |
| `viewport` | `viewport` | The host resizes the virtual display in place. |
| `pong` | `id` | Reply to `ping`. |
| `bye` | | |

Host → client:

| type | fields | |
|---|---|---|
| `status` | `state`: `preparing`/`streaming`, `width`, `height` (pixels) | |
| `offer` | `sdp` | The host is always the offerer. |
| `candidate` | as above | |
| `ping` | `id`, `latencyMs` (last measured round trip) | Sent every 2 s. With no `pong` for 10 s the connection closes. |
| `error` | `code`, `message` | E.g. `bad_message`, `no_such_display`, `resize_failed`. After 5 protocol errors the connection closes. |
| `bye` | `reason`: `replaced`, `host_stopped`, `revoked`, `timeout`, `ice_failed`, `driver_missing` (Windows extend mode without the Virtual Display Driver), … | |

Signaling messages are capped at 64 KiB.

## Media
The Windows host offers only H.264 Constrained Baseline (`42e01f`), encoded by FFmpeg with an
IDR at least every 2 s; it does not adapt the bitrate to congestion yet. Its `/host` page is a
status page for the PC's own browser, served to loopback clients only.

- **Video:** one send-only video track. H.264 Constrained Baseline (`42e01f`, VideoToolbox through
  libwebrtc) is offered first, with VP8 as a fallback.
  - Degradation preference is *maintain resolution*.
  - Bitrate is ≈0.1 bit/pixel/frame, capped by the user's maximum split across sessions, with a floor of 1.5 Mbps.
- **Keyframes:** libwebrtc sends keyframes on PLI/FIR and includes SPS/PPS with every IDR.
  While the screen is static the host re-sends the last frame every 100 ms, so recovery is quick.
- **ICE:** host candidates only (`iceServers: []`).

## Input (DataChannel `"input"`, ordered, JSON text, ≤ 1 KiB per message)

Coordinates are normalized (0..1) to the **visible video content** (letterboxing excluded).

| `t` | fields | host action |
|---|---|---|
| `down` | `x`,`y` | Move and press left. The click count (1–3) comes from time (< 0.4 s) and distance (< 3 % of the display) since the previous press. |
| `move` | `x`,`y` | Hover, or drag while pressed. |
| `up` | `x`,`y` | Release left. |
| `scroll` | `x`,`y`,`dx`,`dy` | `dx/dy` is the content movement as a fraction of the content size (a finger pulling down gives `dy` > 0). Becomes a pixel scroll event. |
| `rightclick` | `x`,`y` | Right press and release. |
| `stats` | `rtt`,`jitter`,`decode`,`fps` | Shown in the menu bar. |

Input is ignored when "Allow touch and mouse control" is off. Coordinates are clamped to the
display, so the pointer can never be moved onto another monitor.

### Client gestures
- **One finger:** press after 80 ms (or as soon as it moves), drag, release. A quick tap is down
  followed by up.
- **Two fingers:** pan to scroll; a tap (no movement, < 400 ms) is a right click.
- **Mouse:** buttons, wheel and the context menu map directly.
