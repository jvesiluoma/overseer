package media

import (
	"fmt"
	"testing"
	"time"

	"github.com/jvesiluoma/overseer/WindowsHost/internal/protocol"
	"github.com/jvesiluoma/overseer/WindowsHost/internal/server"
	"github.com/jvesiluoma/overseer/WindowsHost/internal/settings"
)

type noDriverPlatform struct{}

func (noDriverPlatform) AcquireDisplay(settings.Display, settings.Settings, int, int) (Display, error) {
	return nil, fmt.Errorf("%w; install it", ErrNoVirtualDisplayDriver)
}
func (noDriverPlatform) Inject([]protocol.MouseAction) {}

type endRecorder struct{ ended chan string }

func (endRecorder) Send(protocol.ServerMessage) {}
func (endRecorder) Stats(protocol.ClientStats)  {}
func (r endRecorder) Ended(reason string)       { r.ended <- reason }

func TestMissingDriverEndsWithItsOwnReason(t *testing.T) {
	e := &Engine{platform: noDriverPlatform{}, settings: settings.Default()}
	r := endRecorder{ended: make(chan string, 1)}
	if _, err := e.MakeSession(server.SessionRequest{}, r); err != nil {
		t.Fatal(err)
	}
	select {
	case reason := <-r.ended:
		if reason != "driver_missing" {
			t.Fatalf("reason %q, want driver_missing", reason)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("session did not end")
	}
}
