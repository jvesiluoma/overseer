package protocol

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestDecodeHello(t *testing.T) {
	m, err := DecodeClient([]byte(`{"type":"hello","display":1,"viewport":{"width":1280,"height":800,"dpr":1.5}}`))
	if err != nil || m.Type != "hello" || m.Display != 1 || m.Viewport.DPR != 1.5 {
		t.Fatalf("got %+v, %v", m, err)
	}
}

func TestDecodeRejectsBadInput(t *testing.T) {
	bad := []string{
		`not json`,
		`{"type":"hello"}`,
		`{"type":"hello","viewport":{"width":10,"height":800,"dpr":1}}`,
		`{"type":"hello","display":64,"viewport":{"width":1280,"height":800,"dpr":1}}`,
		`{"type":"answer","sdp":""}`,
		`{"type":"pong"}`,
		`{"type":"nonsense"}`,
		`{"type":"hello","display":"1","viewport":{"width":1280,"height":800,"dpr":1}}`,
	}
	for _, b := range bad {
		if _, err := DecodeClient([]byte(b)); err == nil {
			t.Errorf("accepted %s", b)
		}
	}
	if _, err := DecodeClient([]byte(`{"type":"bye","sdp":"` + strings.Repeat("x", MaxSignalingMessageBytes) + `"}`)); err != ErrTooLarge {
		t.Errorf("oversized message: %v", err)
	}
}

func TestEncodeOmitsAbsentFields(t *testing.T) {
	var got map[string]any
	_ = json.Unmarshal(Status("preparing", 0, 0).Encode(), &got)
	if len(got) != 2 || got["state"] != "preparing" {
		t.Fatalf("preparing status: %v", got)
	}
	_ = json.Unmarshal(Status("streaming", 1920, 1200).Encode(), &got)
	if got["width"] != 1920.0 || got["height"] != 1200.0 {
		t.Fatalf("streaming status: %v", got)
	}
	got = nil
	_ = json.Unmarshal(Ping(3, nil).Encode(), &got)
	if _, ok := got["latencyMs"]; ok || got["id"] != 3.0 {
		t.Fatalf("ping: %v", got)
	}
}

func TestModeFor(t *testing.T) {
	cases := []struct {
		v            *Viewport
		cw, ch       int
		wantW, wantH int
	}{
		{&Viewport{1280, 800, 1.5}, 0, 0, 1920, 1200},
		{&Viewport{3000, 2000, 2}, 0, 0, 3600, 2400}, // scaled into 3840x2400, aspect kept
		{&Viewport{320, 200, 1}, 0, 0, 768, 480},     // scaled up to the minimum
		{nil, 0, 0, 1920, 1200},
		{&Viewport{1280, 800, 1}, 1600, 900, 1600, 900},
		{&Viewport{1001, 701, 1}, 0, 0, 1000, 700}, // even sizes for H.264
	}
	for _, c := range cases {
		w, h := ModeFor(c.v, c.cw, c.ch)
		if w != c.wantW || h != c.wantH {
			t.Errorf("ModeFor(%+v, %d, %d) = %dx%d, want %dx%d", c.v, c.cw, c.ch, w, h, c.wantW, c.wantH)
		}
	}
}

func TestBitrate(t *testing.T) {
	if b := Bitrate(1920, 1080, 30, 8, 1); b != 6_220_800 {
		t.Errorf("1080p30: %d", b)
	}
	if b := Bitrate(3840, 2160, 60, 8, 2); b != 4_000_000 {
		t.Errorf("capped and shared: %d", b)
	}
	if b := Bitrate(640, 480, 24, 8, 1); b != BitrateFloor {
		t.Errorf("floor: %d", b)
	}
}

func TestDecodeInput(t *testing.T) {
	e, err := DecodeInput([]byte(`{"t":"scroll","x":0.5,"y":0.5,"dy":0.1}`))
	if err != nil || e.Type != "scroll" || e.DY != 0.1 || e.DX != 0 {
		t.Fatalf("%+v %v", e, err)
	}
	if _, err := DecodeInput([]byte(`{"t":"down","x":0.5}`)); err == nil {
		t.Error("missing y accepted")
	}
	e, err = DecodeInput([]byte(`{"t":"stats","rtt":12.5,"fps":30}`))
	if err != nil || *e.Stats.RTTMs != 12.5 || e.Stats.JitterMs != nil {
		t.Fatalf("stats: %+v %v", e, err)
	}
}

func TestInputMapperClampsAndCountsClicks(t *testing.T) {
	now := time.Unix(0, 0)
	m := NewInputMapper(Rect{X: 1920, Y: 0, Width: 1280, Height: 800}, func() time.Time { return now })
	if p := m.Point(2, -1); p != (Point{X: 1920 + 1279, Y: 0}) {
		t.Fatalf("clamp: %+v", p)
	}
	down := m.Handle(InputEvent{Type: "down", X: 0.5, Y: 0.5})
	if len(down) != 2 || down[1].Kind != "leftDown" || down[1].ClickCount != 1 || down[1].Point != (Point{2560, 400}) {
		t.Fatalf("down: %+v", down)
	}
	m.Handle(InputEvent{Type: "up", X: 0.5, Y: 0.5})
	now = now.Add(200 * time.Millisecond)
	second := m.Handle(InputEvent{Type: "down", X: 0.501, Y: 0.5})
	if second[1].ClickCount != 2 {
		t.Fatalf("double click: %+v", second)
	}
	if drag := m.Handle(InputEvent{Type: "move", X: 0.6, Y: 0.5}); drag[0].Kind != "drag" {
		t.Fatalf("drag: %+v", drag)
	}
	if r := m.Reset(); len(r) != 1 || r[0].Kind != "leftUp" {
		t.Fatalf("reset: %+v", r)
	}
	now = now.Add(time.Second)
	if third := m.Handle(InputEvent{Type: "down", X: 0.5, Y: 0.5}); third[1].ClickCount != 1 {
		t.Fatalf("slow click counted as multi: %+v", third)
	}
}

func TestInputMapperScroll(t *testing.T) {
	m := NewInputMapper(Rect{Width: 1000, Height: 500}, nil)
	a := m.Handle(InputEvent{Type: "scroll", X: 0.5, Y: 0.5, DX: 0, DY: 0.1})
	if last := a[len(a)-1]; last.Kind != "scroll" || last.DY != 50 {
		t.Fatalf("scroll: %+v", a)
	}
	m.InvertScroll = true
	a = m.Handle(InputEvent{Type: "scroll", X: 0.5, Y: 0.5, DY: 0.1})
	if a[len(a)-1].DY != -50 {
		t.Fatalf("inverted: %+v", a)
	}
}
