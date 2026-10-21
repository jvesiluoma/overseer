package winplat

import (
	"errors"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/jvesiluoma/overseer/WindowsHost/internal/media"
	"github.com/jvesiluoma/overseer/WindowsHost/internal/protocol"
	"github.com/jvesiluoma/overseer/WindowsHost/internal/settings"
)

// Platform implements media.Platform: extend mode uses the Virtual Display Driver's monitors,
// mirror mode an existing monitor.
type Platform struct {
	injector Injector

	mu sync.Mutex
	// Virtual monitors in use, by display ID.
	inUse map[int]bool
	// HideWhenIdle: switch the virtual monitors off when no session uses them, so windows are
	// never left on an invisible screen.
	HideWhenIdle bool
}

func NewPlatform() *Platform {
	return &Platform{inUse: map[int]bool{}, HideWhenIdle: true}
}

func (p *Platform) Inject(actions []protocol.MouseAction) { p.injector.Perform(actions) }

func (p *Platform) AcquireDisplay(d settings.Display, s settings.Settings, width, height int) (media.Display, error) {
	if s.Mode == settings.ModeMirror {
		m, ok := MonitorNamed(s.MirrorMonitor)
		if !ok {
			return nil, fmt.Errorf("monitor %q is not connected", s.MirrorMonitor)
		}
		return &mirrorDisplay{name: m.Name}, nil
	}
	if !VDDInstalled() {
		return nil, fmt.Errorf("%w; install it (winget install VirtualDrivers.Virtual-Display-Driver) or switch to Mirror mode", ErrNoVirtualDisplayDriver)
	}
	p.mu.Lock()
	p.inUse[d.ID] = true
	needed := 0
	for id := range p.inUse {
		needed = max(needed, id+1)
	}
	p.mu.Unlock()

	monitor, err := ensureVirtualMonitor(d.ID, needed)
	if err != nil {
		p.release(d.ID)
		return nil, err
	}
	display := &virtualDisplay{platform: p, id: d.ID, name: monitor.Name}
	if _, err := display.Resize(width, height); err != nil {
		log.Printf("overscreen: %v", err) // keep streaming at whatever size the monitor has
	}
	return display, nil
}

// ensureVirtualMonitor makes sure at least `needed` virtual monitors exist and returns the one
// for display `index`, attached to the desktop.
func ensureVirtualMonitor(index, needed int) (Monitor, error) {
	monitors := VirtualMonitors()
	if len(monitors) < needed {
		if err := SetVirtualDisplayCount(needed); err != nil {
			return Monitor{}, err
		}
		monitors = waitForVirtualMonitors(needed, 10*time.Second)
		if len(monitors) < needed {
			return Monitor{}, errors.New("the virtual monitor did not appear")
		}
	}
	m := monitors[index]
	if !m.Attached {
		if err := attachRightOf(m.Name, mode{width: 1920, height: 1080}); err != nil {
			return Monitor{}, err
		}
		for i := 0; i < 20; i++ {
			if again, ok := MonitorNamed(m.Name); ok {
				return again, nil
			}
			time.Sleep(100 * time.Millisecond)
		}
		return Monitor{}, fmt.Errorf("%s could not be added to the desktop", m.Name)
	}
	return m, nil
}

func waitForVirtualMonitors(n int, timeout time.Duration) []Monitor {
	deadline := time.Now().Add(timeout)
	for {
		monitors := VirtualMonitors()
		if len(monitors) >= n || time.Now().After(deadline) {
			return monitors
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func (p *Platform) release(id int) {
	p.mu.Lock()
	delete(p.inUse, id)
	idle := len(p.inUse) == 0
	hide := p.HideWhenIdle
	p.mu.Unlock()
	if idle && hide {
		if err := SetVirtualDisplayCount(0); err != nil && !errors.Is(err, ErrNoVirtualDisplayDriver) {
			log.Printf("overscreen: hiding virtual displays: %v", err)
		}
	}
}

// HideIdleVirtualDisplays switches leftover virtual monitors off (at startup, after a crash).
func (p *Platform) HideIdleVirtualDisplays() {
	p.mu.Lock()
	idle := len(p.inUse) == 0 && p.HideWhenIdle
	p.mu.Unlock()
	if idle && VDDInstalled() && len(VirtualMonitors()) > 0 {
		_ = SetVirtualDisplayCount(0)
	}
}

// virtualDisplay is a Virtual Display Driver monitor sized to the browser.
type virtualDisplay struct {
	platform *Platform
	id       int
	mu       sync.Mutex
	name     string
	released bool
}

func (d *virtualDisplay) monitor() Monitor {
	d.mu.Lock()
	name := d.name
	d.mu.Unlock()
	m, _ := MonitorNamed(name)
	return m
}

func (d *virtualDisplay) Size() (int, int) {
	b := d.monitor().Bounds
	return b.Width, b.Height
}

func (d *virtualDisplay) Bounds() protocol.Rect { return d.monitor().Bounds }

func (d *virtualDisplay) Resize(width, height int) (bool, error) {
	d.mu.Lock()
	name := d.name
	d.mu.Unlock()
	available := modes(name)
	target, ok := bestMode(available, width, height)
	if !ok {
		return false, errors.New("the virtual monitor reports no resolutions")
	}
	if target.width != width || target.height != height {
		// Add the exact size to the driver's list; this reloads the driver, which re-creates
		// the monitors, so look ours up again.
		if added, err := addVirtualResolution(width, height); err != nil {
			log.Printf("overscreen: cannot add %dx%d to the virtual display driver: %v", width, height, err)
		} else if added {
			d.mu.Lock()
			id := d.id
			d.mu.Unlock()
			monitors := waitForVirtualMonitors(id+1, 10*time.Second)
			if id < len(monitors) {
				if m, err := ensureVirtualMonitor(id, id+1); err == nil {
					d.mu.Lock()
					d.name = m.Name
					d.mu.Unlock()
					name = m.Name
				}
			}
			if t, ok := bestMode(modes(name), width, height); ok {
				target = t
			}
		}
	}
	current := d.monitor().Bounds
	if current.Width == target.width && current.Height == target.height {
		return false, nil
	}
	if err := setMode(name, target); err != nil {
		return false, err
	}
	return true, nil
}

func (d *virtualDisplay) Source(fps int) media.Source {
	return duplicationSource(d.monitor().Name, fps)
}

func (d *virtualDisplay) Release() {
	d.mu.Lock()
	if d.released {
		d.mu.Unlock()
		return
	}
	d.released = true
	d.mu.Unlock()
	d.platform.release(d.id)
}

// mirrorDisplay streams an existing monitor at its own resolution.
type mirrorDisplay struct{ name string }

func (d *mirrorDisplay) Size() (int, int) {
	m, _ := MonitorNamed(d.name)
	return m.Bounds.Width, m.Bounds.Height
}

func (d *mirrorDisplay) Bounds() protocol.Rect {
	m, _ := MonitorNamed(d.name)
	return m.Bounds
}

func (d *mirrorDisplay) Resize(int, int) (bool, error) { return false, nil }

func (d *mirrorDisplay) Source(fps int) media.Source { return duplicationSource(d.name, fps) }

func (d *mirrorDisplay) Release() {}

// duplicationSource grabs a monitor with Desktop Duplication (FFmpeg ddagrab) on the GPU that
// drives it, then copies frames to system memory for scaling and encoding.
func duplicationSource(monitor string, fps int) media.Source {
	adapter, output, err := DuplicationOutput(monitor)
	if err != nil {
		log.Printf("overscreen: %v; trying the first output", err)
	}
	return media.Source{
		GlobalArgs: []string{"-init_hw_device", fmt.Sprintf("d3d11va=dd:%d", adapter), "-filter_hw_device", "dd"},
		Filter:     fmt.Sprintf("ddagrab=output_idx=%d:framerate=%d:draw_mouse=1:dup_frames=1,hwdownload,format=bgra", output, fps),
	}
}
