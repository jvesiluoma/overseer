package winplat

import "math"

type mode struct{ width, height, hz int }

// bestMode picks the supported mode closest to width x height: an exact match, else the closest
// aspect ratio among modes no larger than wanted (the browser letterboxes the rest), else the
// smallest mode.
func bestMode(available []mode, width, height int) (mode, bool) {
	if len(available) == 0 {
		return mode{}, false
	}
	want := float64(width) / float64(height)
	score := func(m mode) float64 {
		aspect := math.Abs(math.Log(float64(m.width) / float64(m.height) / want))
		fill := float64(m.width*m.height) / float64(width*height)
		if m.width > width || m.height > height {
			return 10 + aspect + fill // only if nothing fits
		}
		return aspect*4 + (1 - fill)
	}
	best := available[0]
	for _, m := range available {
		if m.width == width && m.height == height {
			return m, true
		}
		if score(m) < score(best) {
			best = m
		}
	}
	return best, true
}
