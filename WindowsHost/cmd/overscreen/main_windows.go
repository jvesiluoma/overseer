// Command overscreen is the Windows host: a tray app that turns any browser into an extra
// display for this PC. Build: GOOS=windows go build -ldflags "-H windowsgui" ./WindowsHost/cmd/overscreen
package main

import (
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"fyne.io/systray"
	"github.com/jvesiluoma/overseer/WindowsHost/internal/app"
	"github.com/jvesiluoma/overseer/WindowsHost/internal/media"
	"github.com/jvesiluoma/overseer/WindowsHost/internal/settings"
	"github.com/jvesiluoma/overseer/WindowsHost/internal/winplat"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// version is set at build time: -ldflags "-X main.version=0.2.0".
var version = "dev"

const vddHelp = `<p>Extend mode adds a virtual monitor through the free, signed
<a href="https://github.com/VirtualDrivers/Virtual-Display-Driver">Virtual Display Driver</a>. Install it once:</p>
<ol><li>Get the <b>Virtual Driver Control</b> app from the driver's
<a href="https://github.com/VirtualDrivers/Virtual-Display-Driver/releases">releases page</a>, or run
<code>winget install --id=VirtualDrivers.Virtual-Display-Driver -e</code>.</li>
<li>Open it and click <b>Install</b> (administrator rights needed), then restart Overscreen.</li></ol>
<p>Until then, choose <b>Mirror main screen</b> in the tray menu: it streams your existing screen and needs no driver.</p>`

func main() {
	winplat.EnablePerMonitorDPIAwareness()
	dataDir := dataDirectory()
	setUpLogging(dataDir)

	if !singleInstance() {
		messageBox("Overscreen is already running. Look for its icon in the notification area (system tray).")
		return
	}

	platform := winplat.NewPlatform()
	hostName, _ := os.Hostname()
	a := app.New(dataDir, version, hostName)
	a.NewEngine = func(s settings.Settings) (app.Engine, string, error) {
		e, err := media.NewEngine(platform, s)
		if err != nil {
			return nil, "", err
		}
		return e, e.EncoderName(), nil
	}
	systray.Run(func() { onReady(a, platform) }, func() { a.Stop() })
}

type menu struct {
	status, code, clients                *systray.MenuItem
	openStatus                           *systray.MenuItem
	extend, mirror                       *systray.MenuItem
	allowInput, requirePairing, insecure *systray.MenuItem
	autostart, forget, quit              *systray.MenuItem
}

func onReady(a *app.App, platform *winplat.Platform) {
	systray.SetIcon(app.Icon(false))
	systray.SetTitle("Overscreen")
	systray.SetTooltip("Overscreen")

	m := &menu{}
	m.status = systray.AddMenuItem("Starting…", "")
	m.status.Disable()
	m.code = systray.AddMenuItem("", "")
	m.code.Disable()
	m.clients = systray.AddMenuItem("Nobody connected", "")
	m.clients.Disable()
	m.openStatus = systray.AddMenuItem("Show address and QR code…", "Opens the status page in your browser")
	systray.AddSeparator()
	s := a.Settings()
	m.extend = systray.AddMenuItemCheckbox("Extend: add a virtual display", "Needs the Virtual Display Driver", s.Mode == settings.ModeExtend)
	m.mirror = systray.AddMenuItemCheckbox("Mirror main screen", "Streams your existing screen; no driver needed", s.Mode == settings.ModeMirror)
	m.allowInput = systray.AddMenuItemCheckbox("Allow touch and mouse control", "", s.AllowInput)
	m.requirePairing = systray.AddMenuItemCheckbox("Require pairing code", "", s.RequirePairing)
	m.insecure = systray.AddMenuItemCheckbox("Allow unencrypted connections",
		"For browsers that refuse the certificate. Others on the network could watch or take over.", s.AllowInsecureHTTP)
	m.autostart = systray.AddMenuItemCheckbox("Start with Windows", "", autostartEnabled())
	m.forget = systray.AddMenuItem("Forget all devices", "Every browser has to pair again")
	systray.AddSeparator()
	m.quit = systray.AddMenuItem("Quit Overscreen", "")

	if !winplat.VDDInstalled() {
		a.SetExtendHelp(vddHelp)
	}
	updates := make(chan struct{}, 1)
	a.OnChange = func() {
		select {
		case updates <- struct{}{}:
		default:
		}
	}
	go func() {
		for range updates {
			m.refresh(a)
		}
	}()
	go func() {
		platform.HideIdleVirtualDisplays()
		if err := a.Start(); err != nil {
			log.Printf("start: %v", err)
		}
		first := a.Status()
		if first.Running && firstRun(a) {
			openURL(a.LocalStatusURL())
		}
		for range time.Tick(2 * time.Second) {
			a.Refresh()
		}
	}()
	go m.loop(a, platform)
}

func (m *menu) refresh(a *app.App) {
	st := a.Status()
	switch {
	case st.Running && len(st.URLs) > 0:
		m.status.SetTitle("Open " + st.URLs[0])
	case st.Running:
		m.status.SetTitle("Running; no network connection")
	case st.Error != "":
		m.status.SetTitle("Not running: " + truncate(st.Error, 70))
	default:
		m.status.SetTitle("Starting…")
	}
	if st.Settings.RequirePairing && st.Running {
		m.code.SetTitle("Pairing code: " + st.PairingCode[:3] + " " + st.PairingCode[3:])
		m.code.Show()
	} else {
		m.code.Hide()
	}
	if len(st.Clients) == 0 {
		m.clients.SetTitle("Nobody connected")
		systray.SetIcon(app.Icon(false))
		systray.SetTooltip("Overscreen")
	} else {
		var parts []string
		for _, c := range st.Clients {
			p := c.RemoteAddress
			if c.LatencyMs != nil {
				p += fmt.Sprintf(" (%d ms)", *c.LatencyMs)
			}
			parts = append(parts, p)
		}
		m.clients.SetTitle("Connected: " + strings.Join(parts, ", "))
		systray.SetIcon(app.Icon(true))
		systray.SetTooltip(fmt.Sprintf("Overscreen: %d connected", len(st.Clients)))
	}
	setChecked(m.extend, st.Settings.Mode == settings.ModeExtend)
	setChecked(m.mirror, st.Settings.Mode == settings.ModeMirror)
	setChecked(m.allowInput, st.Settings.AllowInput)
	setChecked(m.requirePairing, st.Settings.RequirePairing)
	setChecked(m.insecure, st.Settings.AllowInsecureHTTP)
}

func (m *menu) loop(a *app.App, platform *winplat.Platform) {
	for {
		select {
		case <-m.openStatus.ClickedCh:
			openURL(a.LocalStatusURL())
		case <-m.extend.ClickedCh:
			if !winplat.VDDInstalled() {
				a.SetExtendHelp(vddHelp)
				openURL(a.LocalStatusURL())
			}
			a.UpdateSettings(func(s *settings.Settings) { s.Mode = settings.ModeExtend })
		case <-m.mirror.ClickedCh:
			a.UpdateSettings(func(s *settings.Settings) { s.Mode = settings.ModeMirror })
		case <-m.allowInput.ClickedCh:
			a.UpdateSettings(func(s *settings.Settings) { s.AllowInput = !s.AllowInput })
		case <-m.requirePairing.ClickedCh:
			a.UpdateSettings(func(s *settings.Settings) { s.RequirePairing = !s.RequirePairing })
		case <-m.insecure.ClickedCh:
			a.UpdateSettings(func(s *settings.Settings) { s.AllowInsecureHTTP = !s.AllowInsecureHTTP })
		case <-m.autostart.ClickedCh:
			setAutostart(!autostartEnabled())
			setChecked(m.autostart, autostartEnabled())
		case <-m.forget.ClickedCh:
			a.RevokeAllDevices()
		case <-m.quit.ClickedCh:
			a.Stop()
			platform.HideIdleVirtualDisplays()
			systray.Quit()
			return
		}
	}
}

func setChecked(item *systray.MenuItem, on bool) {
	if on {
		item.Check()
	} else {
		item.Uncheck()
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func dataDirectory() string {
	base, err := os.UserConfigDir() // %APPDATA%
	if err != nil {
		base = os.TempDir()
	}
	dir := filepath.Join(base, "Overscreen")
	_ = os.MkdirAll(dir, 0o700)
	return dir
}

// setUpLogging writes to overscreen.log next to the settings (a GUI app has no console).
func setUpLogging(dir string) {
	path := filepath.Join(dir, "overscreen.log")
	if info, err := os.Stat(path); err == nil && info.Size() > 1<<20 {
		_ = os.Rename(path, path+".old")
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		log.SetOutput(io.Discard)
		return
	}
	log.SetOutput(f)
	log.Printf("Overscreen %s starting", version)
}

// firstRun is true once, so the status page opens by itself after the first start only.
func firstRun(a *app.App) bool {
	marker := filepath.Join(dataDirectory(), ".welcomed")
	if _, err := os.Stat(marker); err == nil {
		return false
	}
	_ = os.WriteFile(marker, nil, 0o600)
	return true
}

func singleInstance() bool {
	name, _ := windows.UTF16PtrFromString(`Local\OverscreenHost`)
	_, err := windows.CreateMutex(nil, false, name)
	return err != windows.ERROR_ALREADY_EXISTS
}

func openURL(url string) {
	verb, _ := windows.UTF16PtrFromString("open")
	target, _ := windows.UTF16PtrFromString(url)
	_ = windows.ShellExecute(0, verb, target, nil, nil, windows.SW_SHOWNORMAL)
}

func messageBox(text string) {
	t, _ := windows.UTF16PtrFromString(text)
	c, _ := windows.UTF16PtrFromString("Overscreen")
	_, _ = windows.MessageBox(0, t, c, windows.MB_OK|windows.MB_ICONINFORMATION)
}

const runKey = `Software\Microsoft\Windows\CurrentVersion\Run`

func autostartEnabled() bool {
	k, err := registry.OpenKey(registry.CURRENT_USER, runKey, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	defer k.Close()
	_, _, err = k.GetStringValue("Overscreen")
	return err == nil
}

func setAutostart(on bool) {
	k, err := registry.OpenKey(registry.CURRENT_USER, runKey, registry.SET_VALUE)
	if err != nil {
		return
	}
	defer k.Close()
	if !on {
		_ = k.DeleteValue("Overscreen")
		return
	}
	if exe, err := os.Executable(); err == nil {
		_ = k.SetStringValue("Overscreen", `"`+exe+`"`)
	}
}
