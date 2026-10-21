package media

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/pion/rtp"
)

// Encoder is one FFmpeg H.264 encoder with low-latency settings.
type Encoder struct {
	Name   string
	PixFmt string
	args   func(fps, bitrate int) []string
}

func (e Encoder) Args(fps, bitrate int) []string {
	gop := strconv.Itoa(fps * 2) // an IDR at least every 2 s, so a lost packet heals quickly
	common := []string{"-c:v", e.Name, "-g", gop, "-bf", "0",
		"-b:v", strconv.Itoa(bitrate), "-maxrate", strconv.Itoa(bitrate), "-bufsize", strconv.Itoa(bitrate / 2)}
	return append(common, e.args(fps, bitrate)...)
}

// Encoders in order of preference: GPU encoders first, then Media Foundation, then software.
// All produce Constrained Baseline, which every WebRTC browser decodes.
var Encoders = []Encoder{
	{Name: "h264_nvenc", PixFmt: "nv12", args: func(int, int) []string {
		return []string{"-preset", "p1", "-tune", "ull", "-zerolatency", "1", "-rc", "cbr", "-profile:v", "baseline", "-delay", "0"}
	}},
	{Name: "h264_amf", PixFmt: "nv12", args: func(int, int) []string {
		return []string{"-usage", "ultralowlatency", "-quality", "speed", "-rc", "cbr", "-profile:v", "constrained_baseline"}
	}},
	{Name: "h264_qsv", PixFmt: "nv12", args: func(int, int) []string {
		return []string{"-preset", "veryfast", "-profile:v", "baseline", "-look_ahead", "0", "-async_depth", "1"}
	}},
	{Name: "h264_mf", PixFmt: "nv12", args: func(int, int) []string {
		return []string{"-hw_encoding", "1", "-scenario", "display_remoting", "-rate_control", "cbr", "-profile:v", "baseline"}
	}},
	{Name: "h264_videotoolbox", PixFmt: "nv12", args: func(int, int) []string {
		return []string{"-realtime", "1", "-profile:v", "baseline", "-prio_speed", "1"}
	}},
	{Name: "libx264", PixFmt: "yuv420p", args: func(int, int) []string {
		// One thread and one slice per frame: no frame-threading delay.
		return []string{"-preset", "ultrafast", "-tune", "zerolatency", "-profile:v", "baseline", "-threads", "1",
			"-x264-params", "sliced-threads=0:repeat-headers=1"}
	}},
}

// FFmpegPath prefers ffmpeg(.exe) next to our executable (the release ships it), then PATH.
func FFmpegPath() (string, error) {
	name := "ffmpeg"
	if runtime.GOOS == "windows" {
		name = "ffmpeg.exe"
	}
	if exe, err := os.Executable(); err == nil {
		candidate := filepath.Join(filepath.Dir(exe), name)
		if _, err := os.Stat(candidate); err == nil {
			return candidate, nil
		}
	}
	path, err := exec.LookPath(name)
	if err != nil {
		return "", errors.New("ffmpeg not found next to the app or on PATH")
	}
	return path, nil
}

var (
	probeOnce    sync.Once
	probedResult *Encoder
	probedErr    error
)

// PickEncoder returns the first encoder that actually works on this machine (a GPU encoder can be
// compiled in but have no hardware behind it). OVERSCREEN_ENCODER=<name> restricts the choice to
// one encoder, for troubleshooting. The result is cached.
func PickEncoder(ffmpeg string) (Encoder, error) {
	probeOnce.Do(func() {
		only := os.Getenv("OVERSCREEN_ENCODER")
		for _, e := range Encoders {
			if only != "" && e.Name != only {
				continue
			}
			if probeEncoder(ffmpeg, e) {
				enc := e
				probedResult = &enc
				return
			}
		}
		probedErr = errors.New("no working H.264 encoder in ffmpeg")
	})
	if probedErr != nil {
		return Encoder{}, probedErr
	}
	return *probedResult, nil
}

func probeEncoder(ffmpeg string, e Encoder) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	args := []string{"-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "color=c=black:s=640x480:r=30",
		"-frames:v", "5", "-pix_fmt", e.PixFmt}
	args = append(args, e.Args(30, 2_000_000)...)
	args = append(args, "-f", "null", "-")
	cmd := exec.CommandContext(ctx, ffmpeg, args...)
	hideWindow(cmd)
	return cmd.Run() == nil
}

// Pipeline runs one FFmpeg process that encodes a display and sends RTP to a loopback UDP socket,
// and forwards those packets to a sink with continuous sequence numbers and timestamps (so an
// encoder restart, e.g. after a resize, looks like a normal stream to the browser).
type Pipeline struct {
	ffmpeg  string
	encoder Encoder
	sink    func(*rtp.Packet) error

	mu      sync.Mutex
	cmd     *exec.Cmd
	conn    *net.UDPConn
	stderr  *tailBuffer
	stopped bool
	held    bool // exits are expected (the display is being changed); see Hold
	exited  bool // the current FFmpeg run has ended
	gen     int
	rw      rewriter
	// OnExit is called when FFmpeg exits on its own (not via Stop/Restart), with its last output.
	OnExit func(err error)
}

func NewPipeline(ffmpeg string, encoder Encoder, sink func(*rtp.Packet) error) *Pipeline {
	return &Pipeline{ffmpeg: ffmpeg, encoder: encoder, sink: sink}
}

// Start (or restart) encoding src scaled to width x height.
func (p *Pipeline) Start(src Source, width, height, fps, bitrate int) error {
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		return err
	}
	port := conn.LocalAddr().(*net.UDPAddr).Port
	filter := fmt.Sprintf("scale=w=%d:h=%d:flags=bicubic:out_color_matrix=bt709:out_range=tv,format=%s[v]",
		width, height, p.encoder.PixFmt)
	if src.Filter != "" {
		filter = src.Filter + "," + filter
	}
	args := []string{"-hide_banner", "-loglevel", "error", "-nostdin"}
	args = append(args, src.GlobalArgs...)
	if len(src.InputArgs) > 0 {
		args = append(args, src.InputArgs...)
		filter = "[0:v]" + filter
	}
	args = append(args, "-filter_complex", filter, "-map", "[v]")
	args = append(args, p.encoder.Args(fps, bitrate)...)
	args = append(args, "-colorspace", "bt709", "-color_primaries", "bt709", "-color_trc", "bt709", "-color_range", "tv",
		"-bsf:v", "dump_extra=freq=keyframe", // SPS/PPS before every IDR
		"-flush_packets", "1", "-f", "rtp", fmt.Sprintf("rtp://127.0.0.1:%d?pkt_size=1200", port))
	cmd := exec.Command(p.ffmpeg, args...)
	hideWindow(cmd)
	stderr := &tailBuffer{max: 4096}
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		conn.Close()
		return fmt.Errorf("starting ffmpeg: %w", err)
	}

	p.mu.Lock()
	old, oldConn := p.cmd, p.conn
	p.cmd, p.conn, p.stderr, p.stopped, p.held, p.exited = cmd, conn, stderr, false, false, false
	p.gen++
	gen := p.gen
	p.rw.restart()
	p.mu.Unlock()
	stopProcess(old, oldConn)

	go p.forward(conn, gen)
	go func() {
		err := cmd.Wait()
		p.mu.Lock()
		current := p.gen == gen && !p.stopped && !p.held
		if p.gen == gen {
			p.exited = true
		}
		p.mu.Unlock()
		if current && p.OnExit != nil {
			if msg := strings.TrimSpace(stderr.String()); msg != "" {
				err = fmt.Errorf("%v: %s", err, msg)
			}
			p.OnExit(err)
		}
	}()
	return nil
}

func (p *Pipeline) forward(conn *net.UDPConn, gen int) {
	buf := make([]byte, 1500)
	for {
		n, from, err := conn.ReadFromUDP(buf)
		if err != nil {
			return
		}
		if !from.IP.IsLoopback() {
			continue
		}
		var pkt rtp.Packet
		if pkt.Unmarshal(buf[:n]) != nil {
			continue
		}
		p.mu.Lock()
		if p.gen != gen {
			p.mu.Unlock()
			return
		}
		p.rw.rewrite(&pkt, time.Now())
		p.mu.Unlock()
		if p.sink(&pkt) != nil {
			return
		}
	}
}

// Hold makes an FFmpeg exit expected until the next Start or Unhold: changing the display (for
// example reloading the virtual display driver) can end the capture under it.
func (p *Pipeline) Hold() {
	p.mu.Lock()
	p.held = true
	p.mu.Unlock()
}

// Unhold ends a Hold without restarting. It reports false if FFmpeg exited meanwhile, in which
// case the caller should Start again.
func (p *Pipeline) Unhold() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.held = false
	return !p.exited
}

func (p *Pipeline) Stop() {
	p.mu.Lock()
	cmd, conn := p.cmd, p.conn
	p.cmd, p.conn, p.stopped = nil, nil, true
	p.mu.Unlock()
	stopProcess(cmd, conn)
}

func stopProcess(cmd *exec.Cmd, conn *net.UDPConn) {
	if cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
	if conn != nil {
		conn.Close()
	}
}

// rewriter maps each FFmpeg run's random sequence numbers and timestamps onto one continuous stream.
type rewriter struct {
	started   bool
	pending   bool // the next packet starts a new FFmpeg run
	seqOffset uint16
	tsOffset  uint32
	lastSeq   uint16
	lastTS    uint32
	lastAt    time.Time
}

func (r *rewriter) restart() { r.pending = true }

func (r *rewriter) rewrite(p *rtp.Packet, now time.Time) {
	if !r.started || r.pending {
		if r.started {
			// Continue after the last packet; advance the clock by the real gap (90 kHz).
			gap := uint32(now.Sub(r.lastAt).Seconds() * 90000)
			r.seqOffset = r.lastSeq + 1 - p.SequenceNumber
			r.tsOffset = r.lastTS + max(gap, 1) - p.Timestamp
		}
		r.started, r.pending = true, false
	}
	p.SequenceNumber += r.seqOffset
	p.Timestamp += r.tsOffset
	r.lastSeq, r.lastTS, r.lastAt = p.SequenceNumber, p.Timestamp, now
}

// tailBuffer keeps the last bytes FFmpeg wrote to stderr, for error messages.
type tailBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
	max int
}

func (t *tailBuffer) Write(b []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf.Write(b)
	if over := t.buf.Len() - t.max; over > 0 {
		t.buf.Next(over)
	}
	return len(b), nil
}

func (t *tailBuffer) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.buf.String()
}

var _ io.Writer = (*tailBuffer)(nil)
