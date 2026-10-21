package protocol

import (
	"encoding/json"
	"fmt"
	"math"
	"time"
)

// MaxInputMessageBytes caps one message on the "input" DataChannel.
const MaxInputMessageBytes = 1024

// ClientStats are the browser's playback statistics, shown in the tray menu.
type ClientStats struct {
	RTTMs    *float64
	JitterMs *float64
	DecodeMs *float64
	FPS      *float64
}

// InputEvent is one message from the "input" DataChannel. Coordinates are normalized to the
// visible video content: (0,0) top-left, (1,1) bottom-right.
type InputEvent struct {
	Type   string // down, move, up, scroll, rightclick, stats
	X, Y   float64
	DX, DY float64
	Stats  ClientStats
}

type wireInput struct {
	T      string   `json:"t"`
	X      *float64 `json:"x"`
	Y      *float64 `json:"y"`
	DX     *float64 `json:"dx"`
	DY     *float64 `json:"dy"`
	RTT    *float64 `json:"rtt"`
	Jitter *float64 `json:"jitter"`
	Decode *float64 `json:"decode"`
	FPS    *float64 `json:"fps"`
}

func DecodeInput(data []byte) (InputEvent, error) {
	if len(data) > MaxInputMessageBytes {
		return InputEvent{}, ErrTooLarge
	}
	var w wireInput
	if err := json.Unmarshal(data, &w); err != nil {
		return InputEvent{}, ErrMalformed
	}
	e := InputEvent{Type: w.T}
	coord := func(v *float64, name string, def *float64) (float64, error) {
		if v == nil {
			v = def
		}
		if v == nil {
			return 0, fmt.Errorf("missing field: %s", name)
		}
		if math.IsNaN(*v) || math.IsInf(*v, 0) {
			return 0, fmt.Errorf("invalid value: %s", name)
		}
		return *v, nil
	}
	var err error
	switch w.T {
	case "down", "move", "up", "rightclick", "scroll":
		if e.X, err = coord(w.X, "x", nil); err != nil {
			return e, err
		}
		if e.Y, err = coord(w.Y, "y", nil); err != nil {
			return e, err
		}
		if w.T == "scroll" {
			zero := 0.0
			if e.DX, err = coord(w.DX, "dx", &zero); err != nil {
				return e, err
			}
			if e.DY, err = coord(w.DY, "dy", &zero); err != nil {
				return e, err
			}
		}
	case "stats":
		finite := func(v *float64) *float64 {
			if v == nil || math.IsNaN(*v) || math.IsInf(*v, 0) {
				return nil
			}
			return v
		}
		e.Stats = ClientStats{RTTMs: finite(w.RTT), JitterMs: finite(w.Jitter), DecodeMs: finite(w.Decode), FPS: finite(w.FPS)}
	default:
		return e, fmt.Errorf("unknown type: %s", w.T)
	}
	return e, nil
}

// Rect is a display's frame in virtual-desktop pixels (top-left origin).
type Rect struct{ X, Y, Width, Height int }

type Point struct{ X, Y int }

// MouseAction is a platform-neutral mouse action; the Windows layer turns it into SendInput.
type MouseAction struct {
	Kind       string // move, drag, leftDown, leftUp, rightClick, scroll
	Point      Point
	ClickCount int
	// Scroll deltas in pixels; positive DY moves content down (scrolls towards the top).
	DX, DY int
}

// InputMapper turns normalized browser input into mouse actions confined to one display.
// It counts multi-clicks itself (time + distance), the way a native mouse driver would.
type InputMapper struct {
	Bounds             Rect
	InvertScroll       bool
	MultiClickInterval time.Duration
	// Max distance between taps of a multi-click, as a fraction of the display's larger side.
	MultiClickDistance float64

	now          func() time.Time
	leftHeld     bool
	lastPoint    *Point
	lastDownTime time.Time
	lastDown     Point
	clickCount   int
}

func NewInputMapper(bounds Rect, now func() time.Time) *InputMapper {
	if now == nil {
		now = time.Now
	}
	return &InputMapper{Bounds: bounds, MultiClickInterval: 400 * time.Millisecond, MultiClickDistance: 0.03, now: now}
}

func (m *InputMapper) Point(x, y float64) Point {
	clamp := func(v float64) float64 { return math.Min(math.Max(v, 0), 1) }
	w, h := float64(m.Bounds.Width), float64(m.Bounds.Height)
	// Stay strictly inside the display so the cursor never lands on a neighbour.
	px := math.Min(clamp(x)*w, math.Max(w-1, 0))
	py := math.Min(clamp(y)*h, math.Max(h-1, 0))
	return Point{X: m.Bounds.X + int(math.Round(px)), Y: m.Bounds.Y + int(math.Round(py))}
}

func (m *InputMapper) Handle(e InputEvent) []MouseAction {
	switch e.Type {
	case "down":
		p := m.Point(e.X, e.Y)
		actions := m.releaseIfHeld(p)
		now := m.now()
		threshold := math.Max(12, m.MultiClickDistance*float64(max(m.Bounds.Width, m.Bounds.Height)))
		if !m.lastDownTime.IsZero() && now.Sub(m.lastDownTime) <= m.MultiClickInterval && distance(p, m.lastDown) <= threshold {
			m.clickCount = min(m.clickCount+1, 3)
		} else {
			m.clickCount = 1
		}
		m.lastDownTime, m.lastDown, m.leftHeld = now, p, true
		m.lastPoint = &p
		return append(actions, MouseAction{Kind: "move", Point: p}, MouseAction{Kind: "leftDown", Point: p, ClickCount: m.clickCount})
	case "move":
		p := m.Point(e.X, e.Y)
		if m.lastPoint != nil && *m.lastPoint == p {
			return nil
		}
		m.lastPoint = &p
		if m.leftHeld {
			return []MouseAction{{Kind: "drag", Point: p}}
		}
		return []MouseAction{{Kind: "move", Point: p}}
	case "up":
		p := m.Point(e.X, e.Y)
		m.lastPoint = &p
		if !m.leftHeld {
			return []MouseAction{{Kind: "move", Point: p}}
		}
		m.leftHeld = false
		return []MouseAction{{Kind: "leftUp", Point: p, ClickCount: m.clickCount}}
	case "scroll":
		p := m.Point(e.X, e.Y)
		actions := m.releaseIfHeld(p)
		if m.lastPoint == nil || *m.lastPoint != p {
			actions = append(actions, MouseAction{Kind: "move", Point: p})
			m.lastPoint = &p
		}
		sign := 1.0
		if m.InvertScroll {
			sign = -1
		}
		sx := int(math.Round(e.DX * float64(m.Bounds.Width) * sign))
		sy := int(math.Round(e.DY * float64(m.Bounds.Height) * sign))
		if sx != 0 || sy != 0 {
			actions = append(actions, MouseAction{Kind: "scroll", Point: p, DX: sx, DY: sy})
		}
		return actions
	case "rightclick":
		p := m.Point(e.X, e.Y)
		actions := m.releaseIfHeld(p)
		m.lastPoint = &p
		return append(actions, MouseAction{Kind: "rightClick", Point: p})
	}
	return nil
}

// Reset releases a held button, e.g. when the session ends mid-drag.
func (m *InputMapper) Reset() []MouseAction {
	if !m.leftHeld || m.lastPoint == nil {
		return nil
	}
	m.leftHeld = false
	return []MouseAction{{Kind: "leftUp", Point: *m.lastPoint, ClickCount: m.clickCount}}
}

func (m *InputMapper) releaseIfHeld(p Point) []MouseAction {
	if !m.leftHeld {
		return nil
	}
	m.leftHeld = false
	if m.lastPoint != nil {
		p = *m.lastPoint
	}
	return []MouseAction{{Kind: "leftUp", Point: p, ClickCount: m.clickCount}}
}

func distance(a, b Point) float64 {
	return math.Hypot(float64(a.X-b.X), float64(a.Y-b.Y))
}
