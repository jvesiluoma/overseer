//go:build integration

// Integration tests against real Windows display APIs. They need an interactive desktop, FFmpeg
// (FFMPEG=path or on PATH) and, for the virtual display test, the Virtual Display Driver.
//
//	go test -tags integration -v ./WindowsHost/internal/winplat
package winplat

import (
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unsafe"

	"github.com/jvesiluoma/overseer/WindowsHost/internal/media"
	"github.com/jvesiluoma/overseer/WindowsHost/internal/protocol"
	"github.com/jvesiluoma/overseer/WindowsHost/internal/settings"
)

func ffmpegPath(t *testing.T) string {
	if p := os.Getenv("FFMPEG"); p != "" {
		return p
	}
	p, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not found")
	}
	return p
}

// grabFrame captures one frame of a display through the same FFmpeg source the app uses.
func grabFrame(t *testing.T, d media.Display) (int, int) {
	src := d.Source(30)
	out := filepath.Join(t.TempDir(), "frame.png")
	args := append([]string{"-hide_banner", "-loglevel", "error"}, src.GlobalArgs...)
	args = append(args, src.InputArgs...)
	if src.Filter != "" {
		chain := src.Filter + "[v]"
		if len(src.InputArgs) > 0 {
			chain = "[0:v]" + chain
		}
		args = append(args, "-filter_complex", chain, "-map", "[v]")
	}
	args = append(args, "-frames:v", "1", "-y", out)
	cmd := exec.Command(ffmpegPath(t), args...)
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("ffmpeg %v: %v\n%s", args, err, b)
	}
	f, err := os.Open(out)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	img, err := png.Decode(f)
	if err != nil {
		t.Fatal(err)
	}
	return img.Bounds().Dx(), img.Bounds().Dy()
}

var procGetCursorPos = user32.NewProc("GetCursorPos")

func cursor() protocol.Point {
	var p struct{ X, Y int32 }
	procGetCursorPos.Call(uintptr(unsafe.Pointer(&p)))
	return protocol.Point{X: int(p.X), Y: int(p.Y)}
}

func TestListMonitors(t *testing.T) {
	EnablePerMonitorDPIAwareness()
	for _, m := range Monitors() {
		t.Logf("%s adapter=%q id=%q attached=%v primary=%v bounds=%+v virtual=%v", m.Name, m.Adapter, m.DeviceID, m.Attached, m.Primary, m.Bounds, m.IsVirtual())
		if m.Attached {
			a, o, err := DuplicationOutput(m.Name)
			t.Logf("  duplication adapter=%d output=%d err=%v", a, o, err)
		}
	}
	t.Logf("virtual display driver installed: %v (pipe %v)", VDDInstalled(), vddPipeExists())
	if entries, err := os.ReadDir(`\\.\pipe\`); err == nil {
		for _, e := range entries {
			if n := e.Name(); strings.Contains(strings.ToLower(n), "mtt") || strings.Contains(strings.ToLower(n), "virtual") {
				t.Logf("  pipe %s", n)
			}
		}
	}
	for adapter := 0; adapter < 3; adapter++ {
		t.Logf("  Direct3D 11 on adapter %d: %v", adapter, d3d11Available(adapter))
	}
}

func TestMirrorPrimary(t *testing.T) {
	EnablePerMonitorDPIAwareness()
	p := NewPlatform()
	s := settings.Default()
	s.Mode = settings.ModeMirror
	d, err := p.AcquireDisplay(settings.Display{}, s, 1280, 720)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Release()
	w, h := d.Size()
	gw, gh := grabFrame(t, d)
	t.Logf("primary %dx%d, captured %dx%d", w, h, gw, gh)
	if gw != w || gh != h {
		t.Fatalf("captured %dx%d, monitor is %dx%d", gw, gh, w, h)
	}
	b := d.Bounds()
	target := protocol.Point{X: b.X + b.Width/3, Y: b.Y + b.Height/3}
	p.Inject([]protocol.MouseAction{{Kind: "move", Point: target}})
	if got := cursor(); got != target {
		t.Fatalf("cursor at %+v, want %+v", got, target)
	}
}

func TestVirtualDisplayLifecycle(t *testing.T) {
	EnablePerMonitorDPIAwareness()
	if !VDDInstalled() {
		t.Skip("Virtual Display Driver not installed")
	}
	p := NewPlatform()
	d, err := p.AcquireDisplay(settings.Display{ID: 0}, settings.Default(), 1920, 1080)
	if err != nil {
		t.Fatal(err)
	}
	w, h := d.Size()
	b := d.Bounds()
	t.Logf("virtual display %dx%d at %+v; monitors: %+v", w, h, b, VirtualMonitors())
	if w != 1920 || h != 1080 {
		t.Errorf("size %dx%d, want 1920x1080", w, h)
	}
	if gw, gh := grabFrame(t, d); gw != w || gh != h {
		t.Errorf("captured %dx%d", gw, gh)
	}

	// A size that is not in the driver's default list gets added to it.
	changed, err := d.Resize(1280, 800)
	w, h = d.Size()
	t.Logf("resize to 1280x800: changed=%v err=%v now %dx%d", changed, err, w, h)
	if err != nil || w != 1280 || h != 800 {
		t.Errorf("resize: %v, %dx%d; modes: %v", err, w, h, modes(d.(*virtualDisplay).monitor().Name))
	}
	if gw, gh := grabFrame(t, d); gw != w || gh != h {
		t.Errorf("captured %dx%d after resize", gw, gh)
	}

	b = d.Bounds()
	target := protocol.Point{X: b.X + b.Width/2, Y: b.Y + b.Height/2}
	p.Inject([]protocol.MouseAction{{Kind: "move", Point: target}})
	if got := cursor(); got != target {
		t.Errorf("cursor at %+v, want %+v on the virtual display", got, target)
	}

	d.Release()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		attached := 0
		for _, m := range VirtualMonitors() {
			if m.Attached {
				attached++
			}
		}
		if attached == 0 {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Errorf("virtual monitor still attached after release: %+v", VirtualMonitors())
}
