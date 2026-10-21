package media

import (
	"testing"
	"time"

	"github.com/pion/rtp"
)

func TestRewriterKeepsStreamContinuousAcrossRestarts(t *testing.T) {
	var r rewriter
	t0 := time.Unix(100, 0)
	p := &rtp.Packet{Header: rtp.Header{SequenceNumber: 65534, Timestamp: 4_294_967_000}}
	r.rewrite(p, t0)
	p = &rtp.Packet{Header: rtp.Header{SequenceNumber: 65535, Timestamp: 4_294_967_000}}
	r.rewrite(p, t0)
	if p.SequenceNumber != 65535 {
		t.Fatalf("first run must pass through, got %d", p.SequenceNumber)
	}
	// FFmpeg restarts with new random numbers, 0.5 s later.
	r.restart()
	p = &rtp.Packet{Header: rtp.Header{SequenceNumber: 1234, Timestamp: 777}}
	r.rewrite(p, t0.Add(500*time.Millisecond))
	if p.SequenceNumber != 0 { // 65535 + 1 wraps
		t.Fatalf("sequence after restart: %d", p.SequenceNumber)
	}
	want := uint32(4_294_967_000)
	want += 45_000 // wraps too
	if p.Timestamp != want {
		t.Fatalf("timestamp after restart: %d, want %d", p.Timestamp, want)
	}
	next := &rtp.Packet{Header: rtp.Header{SequenceNumber: 1235, Timestamp: 777 + 3000}}
	r.rewrite(next, t0.Add(533*time.Millisecond))
	if next.SequenceNumber != 1 || next.Timestamp != p.Timestamp+3000 {
		t.Fatalf("offsets not kept: %+v", next.Header)
	}
}

func TestFitEven(t *testing.T) {
	if w, h := fitEven(5120, 2880); w != 3840 || h != 2160 {
		t.Fatalf("5K: %dx%d", w, h)
	}
	if w, h := fitEven(1366, 767); w != 1366 || h != 766 {
		t.Fatalf("odd: %dx%d", w, h)
	}
}

func TestEncoderArgsAreLowLatency(t *testing.T) {
	for _, e := range Encoders {
		args := e.Args(30, 4_000_000)
		has := func(k, v string) bool {
			for i := 0; i+1 < len(args); i++ {
				if args[i] == k && args[i+1] == v {
					return true
				}
			}
			return false
		}
		if !has("-bf", "0") || !has("-g", "60") || !has("-c:v", e.Name) {
			t.Errorf("%s: %v", e.Name, args)
		}
	}
}
