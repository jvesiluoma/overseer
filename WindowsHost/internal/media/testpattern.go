package media

import (
	"fmt"
	"log"
	"sync"

	"github.com/jvesiluoma/overseer/WindowsHost/internal/protocol"
	"github.com/jvesiluoma/overseer/WindowsHost/internal/settings"
)

// TestPlatform streams an FFmpeg test pattern instead of a monitor and logs input instead of
// injecting it. It lets the whole streaming path run on any OS (dev server, browser tests).
type TestPlatform struct {
	mu     sync.Mutex
	Events []protocol.MouseAction
}

func (t *TestPlatform) AcquireDisplay(_ settings.Display, _ settings.Settings, width, height int) (Display, error) {
	return &testDisplay{width: width, height: height}, nil
}

func (t *TestPlatform) Inject(actions []protocol.MouseAction) {
	if len(actions) == 0 {
		return
	}
	t.mu.Lock()
	t.Events = append(t.Events, actions...)
	t.mu.Unlock()
	for _, a := range actions {
		log.Printf("input: %s %d,%d", a.Kind, a.Point.X, a.Point.Y)
	}
}

type testDisplay struct {
	mu            sync.Mutex
	width, height int
}

func (d *testDisplay) Size() (int, int) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.width, d.height
}

func (d *testDisplay) Bounds() protocol.Rect {
	w, h := d.Size()
	return protocol.Rect{Width: w, Height: h}
}

func (d *testDisplay) Resize(width, height int) (bool, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.width == width && d.height == height {
		return false, nil
	}
	d.width, d.height = width, height
	return true, nil
}

func (d *testDisplay) Source(fps int) Source {
	w, h := d.Size()
	// testsrc2 renders as fast as it can; realtime paces it like a screen grab.
	return Source{Filter: fmt.Sprintf("testsrc2=size=%dx%d:rate=%d,realtime", w, h, fps)}
}

func (d *testDisplay) Release() {}
