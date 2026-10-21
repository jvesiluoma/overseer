package winplat

import (
	"math"
	"sync"
	"unsafe"

	"github.com/jvesiluoma/overseer/WindowsHost/internal/protocol"
)

const (
	inputMouse           = 0
	mouseeventfLeftDown  = 0x0002
	mouseeventfLeftUp    = 0x0004
	mouseeventfRightDown = 0x0008
	mouseeventfRightUp   = 0x0010
	mouseeventfWheel     = 0x0800
	mouseeventfHWheel    = 0x1000
	wheelDelta           = 120
	// Pixels of content movement per wheel notch: Windows scrolls 3 lines (~50 px) per notch.
	pixelsPerNotch = 50.0
)

// mouseInput is INPUT with the MOUSEINPUT union member (40 bytes on 64-bit).
type mouseInput struct {
	Type      uint32
	_         uint32 // padding before the union on 64-bit
	Dx, Dy    int32
	MouseData uint32
	Flags     uint32
	Time      uint32
	_         uint32
	ExtraInfo uintptr
}

// Injector turns mouse actions into real input. Windows works out double clicks itself from the
// timing and position of presses, so the click count is not needed. Input cannot reach windows
// of elevated (administrator) programs unless Overscreen runs elevated too.
type Injector struct {
	mu               sync.Mutex
	scrollX, scrollY float64 // sub-notch remainders
}

func (in *Injector) Perform(actions []protocol.MouseAction) {
	in.mu.Lock()
	defer in.mu.Unlock()
	for _, a := range actions {
		switch a.Kind {
		case "move", "drag":
			setCursor(a.Point)
		case "leftDown":
			setCursor(a.Point)
			sendMouse(mouseeventfLeftDown, 0)
		case "leftUp":
			setCursor(a.Point)
			sendMouse(mouseeventfLeftUp, 0)
		case "rightClick":
			setCursor(a.Point)
			sendMouse(mouseeventfRightDown, 0)
			sendMouse(mouseeventfRightUp, 0)
		case "scroll":
			// Content pulled down (DY > 0) = scroll up = positive wheel delta.
			in.scrollY += float64(a.DY) * wheelDelta / pixelsPerNotch
			// Content pulled right (DX > 0) = scroll left = negative horizontal delta.
			in.scrollX -= float64(a.DX) * wheelDelta / pixelsPerNotch
			if dy := math.Trunc(in.scrollY); dy != 0 {
				sendMouse(mouseeventfWheel, int32(dy))
				in.scrollY -= dy
			}
			if dx := math.Trunc(in.scrollX); dx != 0 {
				sendMouse(mouseeventfHWheel, int32(dx))
				in.scrollX -= dx
			}
		}
	}
}

func setCursor(p protocol.Point) {
	procSetCursorPos.Call(uintptr(int32(p.X)), uintptr(int32(p.Y)))
}

func sendMouse(flags uint32, data int32) {
	in := mouseInput{Type: inputMouse, Flags: flags, MouseData: uint32(data)}
	procSendInput.Call(1, uintptr(unsafe.Pointer(&in)), unsafe.Sizeof(in))
}
