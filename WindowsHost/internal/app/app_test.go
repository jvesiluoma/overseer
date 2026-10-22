package app

import (
	"bytes"
	"image/png"
	"math/rand"
	"strings"
	"testing"

	"github.com/jvesiluoma/overseer/WindowsHost/internal/server"
	"github.com/jvesiluoma/overseer/WindowsHost/internal/settings"
)

type fakeEngine struct{ server.FakeEngine }

func (*fakeEngine) SetSettings(settings.Settings) {}

func TestStartServesAndListsURLs(t *testing.T) {
	dir := t.TempDir()
	a := New(dir, "1.2.3", "Test PC")
	a.NewEngine = func(settings.Settings) (Engine, string, error) { return &fakeEngine{}, "fake", nil }
	base := 30000 + rand.Intn(20000)
	a.UpdateSettings(func(s *settings.Settings) {
		s.HTTPSPort, s.HTTPSFallbackPort, s.Port, s.FallbackPort = base, base+1, base+2, base+3
	})
	if err := a.Start(); err != nil {
		t.Fatal(err)
	}
	defer a.Stop()
	st := a.Status()
	if !st.Running || st.Port != base || st.PlainPort != base+2 || st.Encoder != "fake" || len(st.Fingerprint) != 95 {
		t.Fatalf("%+v", st)
	}
	for _, u := range st.URLs {
		if !strings.HasPrefix(u, "https://") {
			t.Fatalf("unencrypted URL listed: %s", u)
		}
	}
	if !strings.Contains(a.LocalStatusURL(), "http://localhost:") {
		t.Fatalf("status URL %s", a.LocalStatusURL())
	}
	page := string(a.statusPage())
	if !strings.Contains(page, st.PairingCode) || !strings.Contains(page, st.Fingerprint[:23]) {
		t.Fatal("status page lacks the pairing code or fingerprint")
	}

	a.UpdateSettings(func(s *settings.Settings) { s.AllowInsecureHTTP = true })
	insecure := false
	for _, u := range a.Status().URLs {
		insecure = insecure || strings.HasPrefix(u, "http://")
	}
	if len(a.Status().URLs) > 0 && !insecure {
		t.Fatal("plain URLs missing after allowing unencrypted connections")
	}
	if Load := settings.Load(dir + "/settings.json"); !Load.AllowInsecureHTTP {
		t.Fatal("setting not saved")
	}
}

func TestStatusPageLeadsWithDriverSetup(t *testing.T) {
	a := New(t.TempDir(), "1.2.3", "Test PC")
	a.NewEngine = func(settings.Settings) (Engine, string, error) { return &fakeEngine{}, "fake", nil }
	base := 30000 + rand.Intn(20000)
	a.UpdateSettings(func(s *settings.Settings) {
		s.HTTPSPort, s.HTTPSFallbackPort, s.Port, s.FallbackPort = base, base+1, base+2, base+3
	})
	if err := a.Start(); err != nil {
		t.Fatal(err)
	}
	defer a.Stop()

	a.SetExtendHelp("<p>install the driver</p>")
	page := string(a.statusPage())
	help, address := strings.Index(page, "install the driver"), strings.Index(page, "Open on the other device")
	if !a.ExtendBlocked() || help < 0 || help > address {
		t.Fatalf("driver help missing or below the address (help at %d, address at %d)", help, address)
	}

	a.UpdateSettings(func(s *settings.Settings) { s.Mode = settings.ModeMirror })
	if a.ExtendBlocked() || strings.Contains(string(a.statusPage()), "install the driver") {
		t.Fatal("driver help shown in mirror mode")
	}
	a.UpdateSettings(func(s *settings.Settings) { s.Mode = settings.ModeExtend })
	a.SetExtendHelp("")
	if a.ExtendBlocked() || strings.Contains(string(a.statusPage()), "install the driver") {
		t.Fatal("driver help still shown after the driver appeared")
	}
}

func TestIcon(t *testing.T) {
	ico := Icon(true)
	if !bytes.Equal(ico[:6], []byte{0, 0, 1, 0, 1, 0}) {
		t.Fatalf("ICO header % x", ico[:6])
	}
	if _, err := png.Decode(bytes.NewReader(ico[22:])); err != nil {
		t.Fatal(err)
	}
}
