package app

import (
	"encoding/base64"
	"fmt"
	"html"
	"strings"

	"github.com/jvesiluoma/overseer/WindowsHost/internal/netinfo"
	"github.com/jvesiluoma/overseer/WindowsHost/internal/settings"
	qrcode "github.com/skip2/go-qrcode"
)

// statusPage is shown in the PC's own browser ("Open status page" in the tray): addresses with a
// QR code, the pairing code and certificate fingerprint, and setup help. No script; it reloads
// itself every few seconds.
func (a *App) statusPage() []byte {
	st := a.Status()
	a.mu.Lock()
	extendHelp := a.extendHelp
	a.mu.Unlock()
	var b strings.Builder
	esc := html.EscapeString
	b.WriteString(`<!doctype html><html lang="en"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1"><meta http-equiv="refresh" content="3">
<title>Overscreen</title><style>
:root{color-scheme:light dark;--bg:#f6f7f9;--card:#fff;--text:#1b1d21;--muted:#5d6470;--accent:#2563eb;--warn:#b45309;--line:#e3e6eb}
@media (prefers-color-scheme:dark){:root{--bg:#111316;--card:#1b1e23;--text:#e9ebee;--muted:#9aa2ad;--accent:#6aa0ff;--warn:#f59e0b;--line:#2b2f36}}
body{margin:0;background:var(--bg);color:var(--text);font:15px/1.5 system-ui,-apple-system,"Segoe UI",sans-serif}
main{max-width:760px;margin:0 auto;padding:24px 16px}
h1{font-size:22px;margin:0 0 4px}h2{font-size:16px;margin:0 0 8px}
.muted{color:var(--muted)}.card{background:var(--card);border:1px solid var(--line);border-radius:12px;padding:16px;margin:14px 0}
.code{font-size:40px;letter-spacing:.12em;font-weight:700;font-variant-numeric:tabular-nums}
.row{display:flex;gap:20px;flex-wrap:wrap;align-items:center}
.qr{width:180px;height:180px;image-rendering:pixelated;background:#fff;border-radius:8px;padding:6px}
a{color:var(--accent)}code{font-size:13px;word-break:break-all}.warn{color:var(--warn)}
ul,ol{margin:6px 0;padding-left:20px}li{margin:3px 0}
.attention{border:2px solid var(--warn)}.attention h2{color:var(--warn)}
</style></head><body><main>`)
	b.WriteString(`<h1>Overscreen</h1><p class="muted">` + esc(a.HostName) + " · version " + esc(a.Version))
	if st.Encoder != "" {
		b.WriteString(" · encoder " + esc(st.Encoder))
	}
	b.WriteString("</p>")

	if st.Error != "" {
		b.WriteString(`<div class="card warn"><h2>Not running</h2>` + esc(st.Error) + `</div>`)
	}
	if !st.Running {
		b.WriteString("</main></body></html>")
		return []byte(b.String())
	}

	if st.Settings.Mode == settings.ModeExtend && extendHelp != "" {
		b.WriteString(`<div class="card attention"><h2>⚠ One step left: install the Virtual Display Driver</h2>` + extendHelp + `</div>`)
	}

	b.WriteString(`<div class="card"><h2>Open on the other device</h2><div class="row">`)
	if len(st.URLs) > 0 {
		if png, err := qrcode.Encode(st.URLs[0], qrcode.Medium, 256); err == nil {
			b.WriteString(`<img class="qr" alt="QR code for ` + esc(st.URLs[0]) + `" src="data:image/png;base64,` + base64.StdEncoding.EncodeToString(png) + `">`)
		}
	}
	b.WriteString("<div><ul>")
	for _, u := range st.URLs {
		b.WriteString(`<li><code>` + esc(u) + `</code></li>`)
	}
	if len(st.URLs) == 0 {
		b.WriteString("<li>No network connection.</li>")
	}
	b.WriteString(`</ul><p class="muted">The browser warns about the certificate the first time. Continue only if the fingerprint it shows starts with <code>` +
		esc(prefix(st.Fingerprint, 23)) + `</code>.</p></div></div></div>`)

	if st.Settings.RequirePairing {
		b.WriteString(`<div class="card"><h2>Pairing code</h2><div class="code">` + esc(st.PairingCode) + `</div>`)
		b.WriteString(`<p class="muted">Changes after each use.</p></div>`)
	}

	b.WriteString(`<div class="card"><h2>Connected</h2>`)
	if len(st.Clients) == 0 {
		b.WriteString(`<p class="muted">Nobody yet.</p>`)
	} else {
		b.WriteString("<ul>")
		for _, c := range st.Clients {
			line := fmt.Sprintf("%s on display %d", c.RemoteAddress, c.DisplayIndex+1)
			if c.LatencyMs != nil {
				line += fmt.Sprintf(" · %d ms", *c.LatencyMs)
			}
			if c.Stats != nil && c.Stats.FPS != nil {
				line += fmt.Sprintf(" · %.0f fps", *c.Stats.FPS)
			}
			b.WriteString("<li>" + esc(line) + "</li>")
		}
		b.WriteString("</ul>")
	}
	b.WriteString("</div>")

	b.WriteString(`<div class="card"><h2>Car or a device without Wi-Fi</h2>`)
	switch {
	case st.Hotspot != nil && st.Hotspot.Kind == netinfo.WindowsHotspot:
		b.WriteString(`<p>Mobile hotspot is on. Join it on the device and open the first address.</p>`)
	case st.Hotspot != nil && st.Hotspot.Kind == netinfo.IPhoneHotspot:
		b.WriteString(`<p>This PC is on an iPhone hotspot. Join the same hotspot on the device.</p>`)
	default:
		b.WriteString(`<ul><li><b>Phone by USB (most reliable):</b> plug in the phone and turn on USB tethering (iPhone: Personal Hotspot). Then turn on <b>Settings → Network &amp; internet → Mobile hotspot</b>, share the phone's connection over Wi-Fi, and join that network on the device. The address becomes <code>https://192.168.137.1</code>.</li>
<li><b>Phone hotspot:</b> join the phone's hotspot on both this PC and the device. Some Android phones keep hotspot devices apart; use USB then.</li></ul>`)
	}
	b.WriteString(`</div></main></body></html>`)
	return []byte(b.String())
}

func prefix(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
