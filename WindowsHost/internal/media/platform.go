// Package media streams a display to browsers: FFmpeg captures and encodes H.264, and Pion WebRTC
// carries it to the browser and brings back input over the "input" DataChannel.
//
// The OS-specific parts (which display, how FFmpeg grabs it, how input is injected) sit behind
// Platform, so the streaming path can also run on macOS/Linux with a test pattern.
package media

import (
	"errors"

	"github.com/jvesiluoma/overseer/WindowsHost/internal/protocol"
	"github.com/jvesiluoma/overseer/WindowsHost/internal/settings"
)

// ErrNoVirtualDisplayDriver means extend mode cannot work because the Virtual Display Driver is
// not installed (or is disabled). Sessions end with bye{reason:"driver_missing"}.
var ErrNoVirtualDisplayDriver = errors.New("the Virtual Display Driver is not installed")

// Platform provides displays to stream and a way to inject input.
type Platform interface {
	// AcquireDisplay prepares the display for a session: a virtual monitor of about
	// width x height pixels in extend mode, or the configured monitor in mirror mode.
	AcquireDisplay(display settings.Display, s settings.Settings, width, height int) (Display, error)
	Inject(actions []protocol.MouseAction)
}

// Display is one streamed monitor.
type Display interface {
	// Size in pixels of what is captured (before any scaling for the encoder).
	Size() (width, height int)
	// Bounds in virtual-desktop pixels, for mapping input.
	Bounds() protocol.Rect
	// Resize asks for a new size; false means the size did not change (e.g. mirror mode).
	Resize(width, height int) (bool, error)
	// Source describes how FFmpeg grabs the display.
	Source(fps int) Source
	Release()
}

// Source is the FFmpeg input side: global arguments (hardware devices), optional input arguments
// (an input device such as gdigrab) and a filter chain that produces raw frames of the display.
// Without InputArgs the filter must be a source filter (ddagrab, testsrc2); with them it
// processes the input and may be empty.
type Source struct {
	GlobalArgs []string
	InputArgs  []string
	Filter     string
}
