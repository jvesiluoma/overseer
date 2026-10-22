// Package app owns the host: settings, TLS identity, listeners and the media engine. The tray UI
// (Windows) and tests drive it; it has no OS-specific code of its own.
package app

import (
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"github.com/jvesiluoma/overseer/WindowsHost/internal/netinfo"
	"github.com/jvesiluoma/overseer/WindowsHost/internal/pairing"
	"github.com/jvesiluoma/overseer/WindowsHost/internal/server"
	"github.com/jvesiluoma/overseer/WindowsHost/internal/settings"
	"github.com/jvesiluoma/overseer/WindowsHost/internal/tlsid"
)

// Engine is a media engine whose settings can change while it runs.
type Engine interface {
	server.MediaEngine
	SetSettings(settings.Settings)
}

// Status is a snapshot for the UI.
type Status struct {
	Running     bool
	Error       string
	Port        int // HTTPS port
	PlainPort   int // 0 if the plain listener could not start
	URLs        []string
	Fingerprint string
	Hotspot     *netinfo.Hotspot
	PairingCode string
	Clients     []server.ConnectedClient
	Settings    settings.Settings
	Encoder     string
}

type App struct {
	Version  string
	HostName string
	dataDir  string
	// NewEngine creates the media engine on first start (it may probe FFmpeg for a while).
	NewEngine func(settings.Settings) (Engine, string, error)

	pairing *pairing.Manager
	ctx     *server.Context

	mu          sync.Mutex
	settings    settings.Settings
	engine      Engine
	encoder     string
	https       *server.HTTPServer
	plain       *server.HTTPServer
	port        int
	plainPort   int
	running     bool
	lastError   string
	fingerprint string
	extendHelp  string
	addresses   []netinfo.InterfaceAddress
	hotspot     *netinfo.Hotspot

	// OnChange is called after any change the UI should show (from any goroutine).
	OnChange func()
}

func New(dataDir, version, hostName string) *App {
	s := settings.Load(filepath.Join(dataDir, "settings.json"))
	p := pairing.New(pairing.DefaultConfig(), nil)
	a := &App{Version: version, HostName: hostName, dataDir: dataDir, pairing: p, settings: s}
	a.ctx = server.NewContext(s, p, version, hostName)
	a.ctx.LocalPage = a.statusPage
	p.OnCodeChange = func(string) { a.changed() }
	a.ctx.Hub.OnChange = func([]server.ConnectedClient) { a.changed() }
	return a
}

func (a *App) changed() {
	if a.OnChange != nil {
		a.OnChange()
	}
}

func (a *App) Status() Status {
	a.mu.Lock()
	defer a.mu.Unlock()
	return Status{
		Running: a.running, Error: a.lastError, Port: a.port, PlainPort: a.plainPort,
		URLs: a.urlsLocked(), Fingerprint: a.fingerprint, Hotspot: a.hotspot,
		PairingCode: a.pairing.CurrentCode(), Clients: a.ctx.Hub.Clients(), Settings: a.settings, Encoder: a.encoder,
	}
}

// SetExtendHelp sets HTML shown on the status page when extend mode cannot work, e.g. because
// the virtual display driver is missing ("" hides it).
func (a *App) SetExtendHelp(html string) {
	a.mu.Lock()
	same := a.extendHelp == html
	a.extendHelp = html
	a.mu.Unlock()
	if !same {
		a.changed()
	}
}

// ExtendBlocked reports whether extend mode is selected but cannot work (see SetExtendHelp).
func (a *App) ExtendBlocked() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.settings.Mode == settings.ModeExtend && a.extendHelp != ""
}

func (a *App) Settings() settings.Settings {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.settings
}

// UpdateSettings applies and saves a change. Listener settings take effect on the next start.
func (a *App) UpdateSettings(change func(*settings.Settings)) {
	a.mu.Lock()
	s := a.settings
	s.Displays = slices.Clone(s.Displays)
	change(&s)
	s = s.Sanitized()
	restartPlain := a.running && s.AllowInsecureHTTP != a.settings.AllowInsecureHTTP
	a.settings = s
	engine := a.engine
	a.mu.Unlock()
	if err := settings.Save(filepath.Join(a.dataDir, "settings.json"), s); err != nil {
		log.Printf("overscreen: saving settings: %v", err)
	}
	a.ctx.SetSettings(s)
	if engine != nil {
		engine.SetSettings(s)
	}
	if restartPlain {
		a.mu.Lock()
		a.restartPlainLocked()
		a.mu.Unlock()
	}
	a.changed()
}

// Start opens the HTTPS listener (and the plain redirect listener) and creates the engine.
func (a *App) Start() error {
	a.mu.Lock()
	defer func() {
		a.mu.Unlock()
		a.changed()
	}()
	if a.running {
		return nil
	}
	err := a.startLocked()
	if err != nil {
		a.lastError = err.Error()
	} else {
		a.lastError = ""
	}
	return err
}

func (a *App) startLocked() error {
	s := a.settings
	if a.engine == nil {
		if a.NewEngine == nil {
			return errors.New("no media engine")
		}
		engine, encoder, err := a.NewEngine(s)
		if err != nil {
			return err
		}
		a.engine, a.encoder = engine, encoder
	}
	id, err := a.loadIdentity()
	if err != nil {
		return fmt.Errorf("could not create the TLS certificate: %w", err)
	}
	a.fingerprint = id.Fingerprint
	https, err := server.NewHTTPServer(a.ctx, a.engine, server.TLS, id.TLSConfig(), 0)
	if err != nil {
		return err
	}
	port, err := https.Start("0.0.0.0", []int{s.HTTPSPort, s.HTTPSFallbackPort})
	if err != nil {
		return fmt.Errorf("could not open port %d or %d: %w", s.HTTPSPort, s.HTTPSFallbackPort, err)
	}
	a.https, a.port, a.running = https, port, true
	a.restartPlainLocked()
	a.addresses = netinfo.IPv4Addresses()
	a.hotspot = netinfo.HotspotIn(a.addresses)
	return nil
}

// restartPlainLocked (re)opens the plain HTTP listener: a redirect to HTTPS, or the full client
// when unencrypted connections are allowed. It is optional: HTTPS works without it.
func (a *App) restartPlainLocked() {
	if a.plain != nil {
		a.plain.Stop()
		a.plain, a.plainPort = nil, 0
	}
	transport := server.RedirectToHTTPS
	if a.settings.AllowInsecureHTTP {
		transport = server.Plain
	}
	plain, err := server.NewHTTPServer(a.ctx, a.engine, transport, nil, a.port)
	if err != nil {
		return
	}
	port, err := plain.Start("0.0.0.0", []int{a.settings.Port, a.settings.FallbackPort})
	if err != nil {
		log.Printf("overscreen: plain HTTP listener not started: %v", err)
		return
	}
	a.plain, a.plainPort = plain, port
}

func (a *App) Stop() {
	a.mu.Lock()
	if a.https != nil {
		a.https.Stop()
	}
	if a.plain != nil {
		a.plain.Stop()
	}
	a.https, a.plain, a.port, a.plainPort, a.running = nil, nil, 0, 0, false
	a.mu.Unlock()
	a.ctx.Hub.DisconnectAll("host_stopped")
	a.changed()
}

func (a *App) RevokeAllDevices() {
	a.pairing.RevokeAll()
	a.ctx.Hub.DisconnectAll("revoked")
}

// Refresh picks up network changes (Wi-Fi switched, mobile hotspot turned on). Call it every few
// seconds; it only notifies when something changed.
func (a *App) Refresh() {
	addresses := netinfo.IPv4Addresses()
	hotspot := netinfo.HotspotIn(addresses)
	a.mu.Lock()
	changed := !slices.Equal(addresses, a.addresses) || !sameHotspot(hotspot, a.hotspot)
	a.addresses, a.hotspot = addresses, hotspot
	a.mu.Unlock()
	if changed {
		a.changed()
	}
}

func sameHotspot(a, b *netinfo.Hotspot) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

func (a *App) urlsLocked() []string {
	if !a.running {
		return nil
	}
	urls := urlsFor("https", a.port, 443, a.addresses)
	if a.settings.AllowInsecureHTTP && a.plainPort != 0 {
		urls = append(urls, urlsFor("http", a.plainPort, 80, a.addresses)...)
	}
	return urls
}

func urlsFor(scheme string, port, defaultPort int, addresses []netinfo.InterfaceAddress) []string {
	suffix := ""
	if port != defaultPort {
		suffix = fmt.Sprintf(":%d", port)
	}
	var urls []string
	for _, addr := range addresses {
		urls = append(urls, fmt.Sprintf("%s://%s%s", scheme, addr.Address, suffix))
	}
	return urls
}

// LocalStatusURL is the status page for the PC's own browser. It is served only to loopback
// clients, over the plain listener when there is one (no certificate warning for localhost).
func (a *App) LocalStatusURL() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.plainPort != 0 {
		return fmt.Sprintf("http://localhost:%d/host", a.plainPort)
	}
	return fmt.Sprintf("https://localhost:%d/host", a.port)
}

// loadIdentity keeps the certificate next to the settings, so the fingerprint (and the browser
// exceptions users accepted) survive restarts. New certificates also cover the hotspot address.
func (a *App) loadIdentity() (*tlsid.Identity, error) {
	ips := []string{netinfo.WindowsHotspotAddress, "127.0.0.1"}
	for _, addr := range netinfo.IPv4Addresses() {
		ips = append(ips, addr.Address)
	}
	dns := []string{"localhost"}
	if host, err := os.Hostname(); err == nil && host != "" {
		dns = append(dns, host+".local")
	}
	return tlsid.LoadOrCreate(a.dataDir, "Overscreen on "+a.HostName, dns, ips, time.Now())
}
