package winplat

import "testing"

func TestBestMode(t *testing.T) {
	// The driver's default list.
	available := []mode{{1920, 1080, 60}, {2560, 1440, 60}, {3840, 2160, 60}, {1280, 720, 60}, {1600, 1200, 60}}
	cases := []struct{ w, h, wantW, wantH int }{
		{2560, 1440, 2560, 1440}, // exact
		{1920, 1200, 1920, 1080}, // 16:10 browser: biggest that fits, close aspect
		{1280, 1024, 1280, 720},  // nothing 5:4 except 1600x1200, which is too big
		{1000, 600, 1280, 720},   // nothing fits: the smallest
		{1600, 1200, 1600, 1200},
	}
	for _, c := range cases {
		m, ok := bestMode(available, c.w, c.h)
		if !ok || m.width != c.wantW || m.height != c.wantH {
			t.Errorf("bestMode(%dx%d) = %dx%d, want %dx%d", c.w, c.h, m.width, m.height, c.wantW, c.wantH)
		}
	}
	if _, ok := bestMode(nil, 1920, 1080); ok {
		t.Error("empty list")
	}
}
