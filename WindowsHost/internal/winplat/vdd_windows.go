package winplat

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf16"
	"unsafe"

	"github.com/jvesiluoma/overseer/WindowsHost/internal/media"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// The Virtual Display Driver (github.com/VirtualDrivers/Virtual-Display-Driver, MIT, signed)
// listens on a named pipe that any local user may open. Each connection carries one UTF-16
// command such as "SETDISPLAYCOUNT 2" or "RELOAD_DRIVER".
const vddPipe = `\\.\pipe\MTTVirtualDisplayPipe`

// ErrNoVirtualDisplayDriver means the driver is not installed (or not running).
var ErrNoVirtualDisplayDriver = media.ErrNoVirtualDisplayDriver

var procWaitNamedPipeW = windows.NewLazySystemDLL("kernel32.dll").NewProc("WaitNamedPipeW")

// vddPipeExists reports whether the driver's control pipe exists (busy counts as existing).
func vddPipeExists() bool {
	name, _ := windows.UTF16PtrFromString(vddPipe)
	r, _, err := procWaitNamedPipeW.Call(uintptr(unsafe.Pointer(name)), 1)
	return r != 0 || errors.Is(err, windows.ERROR_SEM_TIMEOUT)
}

// VDDInstalled reports whether the driver is present: its control pipe exists, or it has
// monitors (driver versions differ in how they expose the pipe).
func VDDInstalled() bool {
	return vddPipeExists() || len(VirtualMonitors()) > 0
}

func vddCommand(command string) error {
	var f *os.File
	var err error
	for attempt := 0; attempt < 20; attempt++ {
		f, err = os.OpenFile(vddPipe, os.O_RDWR, 0)
		if err == nil {
			break
		}
		if errors.Is(err, windows.ERROR_FILE_NOT_FOUND) {
			return ErrNoVirtualDisplayDriver
		}
		time.Sleep(100 * time.Millisecond) // ERROR_PIPE_BUSY: another client is talking to it
	}
	if err != nil {
		return fmt.Errorf("virtual display driver pipe: %w", err)
	}
	defer f.Close()
	units := utf16.Encode([]rune(command))
	buf := make([]byte, len(units)*2)
	for i, u := range units {
		buf[2*i], buf[2*i+1] = byte(u), byte(u>>8)
	}
	_, err = f.Write(buf)
	return err
}

// SetVirtualDisplayCount makes the driver show n virtual monitors (it re-creates all of them).
func SetVirtualDisplayCount(n int) error {
	return vddCommand(fmt.Sprintf("SETDISPLAYCOUNT %d", n))
}

func reloadVirtualDisplayDriver() error { return vddCommand("RELOAD_DRIVER") }

// vddSettingsPath is the driver's XML config (resolutions, monitor count).
func vddSettingsPath() string {
	dir := `C:\VirtualDisplayDriver`
	if k, err := registry.OpenKey(registry.LOCAL_MACHINE, `SOFTWARE\MikeTheTech\VirtualDisplayDriver`, registry.QUERY_VALUE); err == nil {
		if v, _, err := k.GetStringValue("VDDPATH"); err == nil && v != "" {
			dir = v
		}
		k.Close()
	}
	return filepath.Join(dir, "vdd_settings.xml")
}

// addVirtualResolution adds width x height to the driver's resolution list if it is missing, and
// reloads the driver. It needs write access to the settings file (normally granted to users,
// because the folder sits at the root of C:). Returns false if nothing was changed.
func addVirtualResolution(width, height int) (bool, error) {
	path := vddSettingsPath()
	data, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}
	text := string(data)
	entry := fmt.Sprintf("<width>%d</width>\n            <height>%d</height>", width, height)
	compact := strings.Join(strings.Fields(text), "")
	if strings.Contains(compact, fmt.Sprintf("<width>%d</width><height>%d</height>", width, height)) {
		return false, nil
	}
	end := strings.Index(text, "</resolutions>")
	if end < 0 {
		return false, errors.New("unexpected vdd_settings.xml format")
	}
	block := fmt.Sprintf("    <resolution>\n            %s\n            <refresh_rate>60</refresh_rate>\n        </resolution>\n    ", entry)
	text = text[:end] + block + text[end:]
	tmp := path + ".overscreen.tmp"
	if err := os.WriteFile(tmp, []byte(text), 0o644); err != nil {
		return false, err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return false, err
	}
	return true, reloadVirtualDisplayDriver()
}
