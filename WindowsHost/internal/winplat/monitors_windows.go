package winplat

import (
	"fmt"
	"math"
	"slices"
	"strings"
	"syscall"

	"github.com/jvesiluoma/overseer/WindowsHost/internal/protocol"
)

// Monitor is one display output known to Windows.
type Monitor struct {
	// GDI device name, e.g. \\.\DISPLAY3.
	Name     string
	Adapter  string // adapter description, e.g. "Virtual Display Driver"
	DeviceID string // monitor device ID, contains MTT1337 for the Virtual Display Driver
	Attached bool
	Primary  bool
	Bounds   protocol.Rect
}

// IsVirtual: monitors created by the Virtual Display Driver (EDID manufacturer MTT, product 1337).
func (m Monitor) IsVirtual() bool {
	id := strings.ToUpper(m.DeviceID)
	return strings.Contains(id, "MTT1337") || strings.EqualFold(m.Adapter, "Virtual Display Driver")
}

// Monitors lists display outputs; detached ones have empty Bounds.
func Monitors() []Monitor {
	var result []Monitor
	for i := uint32(0); ; i++ {
		adapter, ok := enumDisplayDevices(nil, i, 0)
		if !ok {
			break
		}
		m := Monitor{
			Name:     wstr(adapter.DeviceName[:]),
			Adapter:  wstr(adapter.DeviceString[:]),
			Attached: adapter.StateFlags&displayDeviceAttachedToDesktop != 0,
			Primary:  adapter.StateFlags&displayDevicePrimaryDevice != 0,
		}
		name, _ := syscall.UTF16PtrFromString(m.Name)
		if monitor, ok := enumDisplayDevices(name, 0, eddGetDeviceInterfaceName); ok {
			m.DeviceID = wstr(monitor.DeviceID[:])
		} else {
			continue // an adapter output with no monitor behind it
		}
		if m.Attached {
			if dm, ok := enumDisplaySettings(m.Name, enumCurrentSettings); ok {
				m.Bounds = protocol.Rect{X: int(dm.PositionX), Y: int(dm.PositionY), Width: int(dm.PelsWidth), Height: int(dm.PelsHeight)}
			}
		}
		result = append(result, m)
	}
	return result
}

// MonitorNamed returns the monitor with the given GDI name, or the primary one for "".
func MonitorNamed(name string) (Monitor, bool) {
	for _, m := range Monitors() {
		if (name == "" && m.Primary) || (name != "" && strings.EqualFold(m.Name, name)) {
			return m, m.Attached
		}
	}
	return Monitor{}, false
}

// VirtualMonitors returns the Virtual Display Driver's monitors in a stable order.
func VirtualMonitors() []Monitor {
	var result []Monitor
	for _, m := range Monitors() {
		if m.IsVirtual() {
			result = append(result, m)
		}
	}
	slices.SortFunc(result, func(a, b Monitor) int { return strings.Compare(a.DeviceID, b.DeviceID) })
	return result
}

// modes lists the resolutions a monitor supports (any refresh rate, 60 Hz preferred).
func modes(name string) []mode {
	seen := map[[2]int]mode{}
	for i := uint32(0); ; i++ {
		dm, ok := enumDisplaySettings(name, i)
		if !ok {
			break
		}
		key := [2]int{int(dm.PelsWidth), int(dm.PelsHeight)}
		m := mode{int(dm.PelsWidth), int(dm.PelsHeight), int(dm.DisplayFrequency)}
		if old, ok := seen[key]; !ok || math.Abs(float64(m.hz-60)) < math.Abs(float64(old.hz-60)) {
			seen[key] = m
		}
	}
	result := make([]mode, 0, len(seen))
	for _, m := range seen {
		result = append(result, m)
	}
	return result
}

// setMode changes a monitor's resolution and saves it.
func setMode(name string, m mode) error {
	dm, ok := enumDisplaySettings(name, enumCurrentSettings)
	if !ok {
		dm = newDevMode()
	}
	dm.PelsWidth, dm.PelsHeight = uint32(m.width), uint32(m.height)
	dm.Fields = dmPelsWidth | dmPelsHeight
	if m.hz > 0 {
		dm.DisplayFrequency = uint32(m.hz)
		dm.Fields |= dmDisplayFrequency
	}
	if r := changeDisplaySettings(name, &dm, cdsUpdateRegistry); r != dispChangeSuccessful {
		return fmt.Errorf("could not set %dx%d on %s (code %d)", m.width, m.height, name, r)
	}
	return nil
}

// attachRightOf attaches a detached monitor to the desktop, to the right of everything else.
func attachRightOf(name string, m mode) error {
	right := 0
	for _, other := range Monitors() {
		if other.Attached {
			right = max(right, other.Bounds.X+other.Bounds.Width)
		}
	}
	dm := newDevMode()
	dm.PositionX, dm.PositionY = int32(right), 0
	dm.PelsWidth, dm.PelsHeight = uint32(m.width), uint32(m.height)
	dm.Fields = dmPosition | dmPelsWidth | dmPelsHeight
	if r := changeDisplaySettings(name, &dm, cdsUpdateRegistry|cdsNoReset); r != dispChangeSuccessful {
		return fmt.Errorf("could not attach %s (code %d)", name, r)
	}
	if r := applyPendingDisplayChanges(); r != dispChangeSuccessful {
		return fmt.Errorf("could not apply display changes (code %d)", r)
	}
	return nil
}
