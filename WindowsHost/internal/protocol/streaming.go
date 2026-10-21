package protocol

import "math"

const (
	MaxPixelWidth  = 3840
	MaxPixelHeight = 2400
	MinPixelWidth  = 640
	MinPixelHeight = 480
	FallbackWidth  = 1920
	FallbackHeight = 1200
	BitrateFloor   = 1_500_000
)

// ModeFor picks the display size for a viewport (CSS px × dpr), or for a fixed custom size when
// width and height are non-zero. The result fits the limits, keeps the aspect ratio and is even
// (H.264 4:2:0).
func ModeFor(viewport *Viewport, customWidth, customHeight int) (int, int) {
	var w, h float64
	switch {
	case customWidth > 0 && customHeight > 0:
		w, h = float64(customWidth), float64(customHeight)
	case viewport != nil && viewport.Plausible():
		w, h = viewport.Width*viewport.DPR, viewport.Height*viewport.DPR
	default:
		w, h = FallbackWidth, FallbackHeight
	}
	down := math.Min(1, math.Min(MaxPixelWidth/w, MaxPixelHeight/h))
	w, h = w*down, h*down
	up := math.Max(1, math.Max(MinPixelWidth/w, MinPixelHeight/h))
	w, h = math.Min(w*up, MaxPixelWidth), math.Min(h*up, MaxPixelHeight)
	align := func(v float64) int { return max(2, int(v)/2*2) }
	return align(w), align(h)
}

// Bitrate for one session: enough for the resolution, capped by the user's maximum shared across
// concurrent sessions, never below the floor.
func Bitrate(width, height, fps int, maxMbps float64, sessions int) int {
	cap := int(maxMbps*1_000_000) / max(sessions, 1)
	// ~0.1 bit per pixel per frame is plenty for desktop content in H.264.
	wanted := int(float64(width*height*fps) * 0.1)
	return max(min(wanted, cap), BitrateFloor)
}
