// Package winplat is the Windows platform: monitors, the Virtual Display Driver, Desktop
// Duplication output lookup for FFmpeg, and input injection.
package winplat

import (
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	user32                        = windows.NewLazySystemDLL("user32.dll")
	procEnumDisplayDevicesW       = user32.NewProc("EnumDisplayDevicesW")
	procEnumDisplaySettingsExW    = user32.NewProc("EnumDisplaySettingsExW")
	procChangeDisplaySettingsExW  = user32.NewProc("ChangeDisplaySettingsExW")
	procSetCursorPos              = user32.NewProc("SetCursorPos")
	procSendInput                 = user32.NewProc("SendInput")
	procSetProcessDpiAwarenessCtx = user32.NewProc("SetProcessDpiAwarenessContext")
)

const (
	displayDeviceAttachedToDesktop = 0x1
	displayDevicePrimaryDevice     = 0x4
	eddGetDeviceInterfaceName      = 0x1

	enumCurrentSettings = 0xFFFFFFFF

	dmPosition         = 0x00000020
	dmPelsWidth        = 0x00080000
	dmPelsHeight       = 0x00100000
	dmDisplayFrequency = 0x00400000

	cdsUpdateRegistry    = 0x00000001
	cdsNoReset           = 0x10000000
	dispChangeSuccessful = 0
)

// displayDevice is DISPLAY_DEVICEW.
type displayDevice struct {
	cb           uint32
	DeviceName   [32]uint16
	DeviceString [128]uint16
	StateFlags   uint32
	DeviceID     [128]uint16
	DeviceKey    [128]uint16
}

// devMode is DEVMODEW (display variant of the unions); 220 bytes.
type devMode struct {
	DeviceName         [32]uint16
	SpecVersion        uint16
	DriverVersion      uint16
	Size               uint16
	DriverExtra        uint16
	Fields             uint32
	PositionX          int32
	PositionY          int32
	DisplayOrientation uint32
	DisplayFixedOutput uint32
	Color              int16
	Duplex             int16
	YResolution        int16
	TTOption           int16
	Collate            int16
	FormName           [32]uint16
	LogPixels          uint16
	BitsPerPel         uint32
	PelsWidth          uint32
	PelsHeight         uint32
	DisplayFlags       uint32
	DisplayFrequency   uint32
	ICMMethod          uint32
	ICMIntent          uint32
	MediaType          uint32
	DitherType         uint32
	Reserved1          uint32
	Reserved2          uint32
	PanningWidth       uint32
	PanningHeight      uint32
}

func newDevMode() devMode {
	var dm devMode
	dm.Size = uint16(unsafe.Sizeof(dm))
	return dm
}

func enumDisplayDevices(device *uint16, index uint32, flags uint32) (displayDevice, bool) {
	var dd displayDevice
	dd.cb = uint32(unsafe.Sizeof(dd))
	r, _, _ := procEnumDisplayDevicesW.Call(uintptr(unsafe.Pointer(device)), uintptr(index), uintptr(unsafe.Pointer(&dd)), uintptr(flags))
	return dd, r != 0
}

func enumDisplaySettings(device string, mode uint32) (devMode, bool) {
	dm := newDevMode()
	name, err := syscall.UTF16PtrFromString(device)
	if err != nil {
		return dm, false
	}
	r, _, _ := procEnumDisplaySettingsExW.Call(uintptr(unsafe.Pointer(name)), uintptr(mode), uintptr(unsafe.Pointer(&dm)), 0)
	return dm, r != 0
}

// changeDisplaySettings applies dm to one monitor and saves it; returns a DISP_CHANGE_* code.
func changeDisplaySettings(device string, dm *devMode, flags uint32) int32 {
	name, err := syscall.UTF16PtrFromString(device)
	if err != nil {
		return -5 // DISP_CHANGE_BADPARAM
	}
	r, _, _ := procChangeDisplaySettingsExW.Call(uintptr(unsafe.Pointer(name)), uintptr(unsafe.Pointer(dm)), 0, uintptr(flags), 0)
	return int32(r)
}

// applyPendingDisplayChanges commits changes made with cdsNoReset.
func applyPendingDisplayChanges() int32 {
	r, _, _ := procChangeDisplaySettingsExW.Call(0, 0, 0, 0, 0)
	return int32(r)
}

// EnablePerMonitorDPIAwareness makes all coordinates physical pixels (capture, display settings
// and SetCursorPos then agree). Must be called before any window is created.
func EnablePerMonitorDPIAwareness() {
	const perMonitorAwareV2 = ^uintptr(3) // DPI_AWARENESS_CONTEXT_PER_MONITOR_AWARE_V2 = -4
	if procSetProcessDpiAwarenessCtx.Find() == nil {
		procSetProcessDpiAwarenessCtx.Call(perMonitorAwareV2)
	}
}

func wstr(s []uint16) string { return windows.UTF16ToString(s) }

// Layout checks against the Windows headers (64-bit).
var (
	_ [unsafe.Sizeof(devMode{}) - 220]byte
	_ [220 - unsafe.Sizeof(devMode{})]byte
	_ [unsafe.Sizeof(displayDevice{}) - 840]byte
	_ [840 - unsafe.Sizeof(displayDevice{})]byte
	_ [unsafe.Sizeof(mouseInput{}) - 40]byte
	_ [40 - unsafe.Sizeof(mouseInput{})]byte
)
