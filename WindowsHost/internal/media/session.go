package media

import (
	"errors"
	"fmt"
	"sync"

	"github.com/jvesiluoma/overseer/WindowsHost/internal/protocol"
	"github.com/jvesiluoma/overseer/WindowsHost/internal/server"
	"github.com/pion/interceptor"
	"github.com/pion/rtp"
	"github.com/pion/webrtc/v4"
)

// h264Fmtp is Constrained Baseline 3.1 with FU-A packetization, the profile every browser decodes.
const h264Fmtp = "level-asymmetry-allowed=1;packetization-mode=1;profile-level-id=42e01f"

func newAPI() (*webrtc.API, error) {
	m := &webrtc.MediaEngine{}
	feedback := []webrtc.RTCPFeedback{{Type: "goog-remb"}, {Type: "ccm", Parameter: "fir"}, {Type: "nack"}, {Type: "nack", Parameter: "pli"}}
	if err := m.RegisterCodec(webrtc.RTPCodecParameters{
		RTPCodecCapability: webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeH264, ClockRate: 90000, SDPFmtpLine: h264Fmtp, RTCPFeedback: feedback},
		PayloadType:        102,
	}, webrtc.RTPCodecTypeVideo); err != nil {
		return nil, err
	}
	registry := &interceptor.Registry{}
	if err := webrtc.RegisterDefaultInterceptors(m, registry); err != nil {
		return nil, err
	}
	se := webrtc.SettingEngine{}
	// LAN only: host candidates over UDP/IPv4, no STUN/TURN.
	se.SetNetworkTypes([]webrtc.NetworkType{webrtc.NetworkTypeUDP4})
	return webrtc.NewAPI(webrtc.WithMediaEngine(m), webrtc.WithInterceptorRegistry(registry), webrtc.WithSettingEngine(se)), nil
}

// session is one browser: a send-only H.264 track plus the "input" DataChannel.
type session struct {
	engine   *Engine
	req      server.SessionRequest
	delegate server.SessionDelegate

	mu            sync.Mutex
	pc            *webrtc.PeerConnection
	track         *webrtc.TrackLocalStaticRTP
	display       Display
	pipeline      *Pipeline
	mapper        *protocol.InputMapper
	offerSent     bool
	pendingLocal  []protocol.IceCandidate
	remoteSet     bool
	pendingRemote []protocol.IceCandidate
	streaming     bool
	closed        bool
	width, height int // encoded size
}

func (s *session) start() {
	s.delegate.Send(protocol.Status("preparing", 0, 0))
	w, h := protocol.ModeFor(&s.req.Viewport, s.req.Display.Width, s.req.Display.Height)
	display, err := s.engine.platform.AcquireDisplay(s.req.Display, s.req.Settings, w, h)
	if errors.Is(err, ErrNoVirtualDisplayDriver) {
		s.end("driver_missing")
		return
	}
	if err != nil {
		s.end(fmt.Sprintf("display_failed: %v", err))
		return
	}
	if err := s.createPeer(display); err != nil {
		display.Release()
		s.end(fmt.Sprintf("peer_failed: %v", err))
	}
}

func (s *session) createPeer(display Display) error {
	pc, err := s.engine.api.NewPeerConnection(webrtc.Configuration{ICEServers: []webrtc.ICEServer{},
		BundlePolicy: webrtc.BundlePolicyMaxBundle, RTCPMuxPolicy: webrtc.RTCPMuxPolicyRequire})
	if err != nil {
		return err
	}
	track, err := webrtc.NewTrackLocalStaticRTP(webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeH264, ClockRate: 90000,
		SDPFmtpLine: h264Fmtp}, "video", "overscreen")
	if err != nil {
		pc.Close()
		return err
	}
	transceiver, err := pc.AddTransceiverFromTrack(track, webrtc.RTPTransceiverInit{Direction: webrtc.RTPTransceiverDirectionSendonly})
	if err != nil {
		pc.Close()
		return err
	}
	go drainRTCP(transceiver.Sender())

	ordered := true
	channel, err := pc.CreateDataChannel("input", &webrtc.DataChannelInit{Ordered: &ordered})
	if err != nil {
		pc.Close()
		return err
	}
	channel.OnMessage(func(msg webrtc.DataChannelMessage) {
		if msg.IsString {
			s.handleInput(msg.Data)
		}
	})

	pc.OnICECandidate(func(c *webrtc.ICECandidate) {
		if c == nil {
			return
		}
		init := c.ToJSON()
		candidate := protocol.IceCandidate{Candidate: init.Candidate, SDPMid: init.SDPMid}
		if init.SDPMLineIndex != nil {
			candidate.SDPMLineIndex = *init.SDPMLineIndex
		}
		s.mu.Lock()
		if !s.offerSent {
			s.pendingLocal = append(s.pendingLocal, candidate)
			s.mu.Unlock()
			return
		}
		s.mu.Unlock()
		s.delegate.Send(protocol.Candidate(candidate))
	})
	pc.OnICEConnectionStateChange(func(state webrtc.ICEConnectionState) {
		switch state {
		case webrtc.ICEConnectionStateConnected, webrtc.ICEConnectionStateCompleted:
			s.startStreaming()
		case webrtc.ICEConnectionStateFailed:
			s.end("ice_failed")
		}
	})

	bw, bh := display.Size()
	w, h := fitEven(bw, bh)
	mapper := protocol.NewInputMapper(display.Bounds(), nil)
	mapper.InvertScroll = s.req.Settings.InvertScroll

	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		pc.Close()
		display.Release()
		return nil
	}
	s.pc, s.track, s.display, s.mapper, s.width, s.height = pc, track, display, mapper, w, h
	s.mu.Unlock()

	offer, err := pc.CreateOffer(nil)
	if err != nil {
		return err
	}
	if err := pc.SetLocalDescription(offer); err != nil {
		return err
	}
	s.delegate.Send(protocol.Offer(offer.SDP))
	s.mu.Lock()
	s.offerSent = true
	pending := s.pendingLocal
	s.pendingLocal = nil
	s.mu.Unlock()
	for _, c := range pending {
		s.delegate.Send(protocol.Candidate(c))
	}
	return nil
}

// drainRTCP keeps the interceptors (NACK, reports) running; keyframes come every 2 s anyway.
func drainRTCP(sender *webrtc.RTPSender) {
	buf := make([]byte, 1500)
	for {
		if _, _, err := sender.Read(buf); err != nil {
			return
		}
	}
}

func (s *session) startStreaming() {
	s.mu.Lock()
	if s.streaming || s.closed || s.display == nil {
		s.mu.Unlock()
		return
	}
	s.streaming = true
	track, display, w, h := s.track, s.display, s.width, s.height
	pipeline := NewPipeline(s.engine.ffmpeg, s.engine.encoder, func(p *rtp.Packet) error {
		_ = track.WriteRTP(p) // fails only while the transport is not up yet or closing
		return nil
	})
	pipeline.OnExit = func(err error) { s.end(fmt.Sprintf("capture_failed: %v", err)) }
	s.pipeline = pipeline
	s.mu.Unlock()

	if err := pipeline.Start(display.Source(s.req.Settings.FPS), w, h, s.req.Settings.FPS, s.bitrate(w, h)); err != nil {
		s.end(fmt.Sprintf("capture_failed: %v", err))
		return
	}
	s.delegate.Send(protocol.Status("streaming", w, h))
}

func (s *session) bitrate(w, h int) int {
	return protocol.Bitrate(w, h, s.req.Settings.FPS, s.req.Settings.MaxBitrateMbps, s.req.ConcurrentSessions)
}

func (s *session) Handle(msg protocol.ClientMessage) {
	switch msg.Type {
	case "answer":
		s.mu.Lock()
		pc := s.pc
		s.mu.Unlock()
		if pc == nil {
			return
		}
		if err := pc.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeAnswer, SDP: msg.SDP}); err != nil {
			s.end(fmt.Sprintf("answer_rejected: %v", err))
			return
		}
		s.mu.Lock()
		s.remoteSet = true
		pending := s.pendingRemote
		s.pendingRemote = nil
		s.mu.Unlock()
		for _, c := range pending {
			s.addCandidate(pc, c)
		}
	case "candidate":
		s.mu.Lock()
		pc := s.pc
		if pc == nil || !s.remoteSet {
			if len(s.pendingRemote) < 64 {
				s.pendingRemote = append(s.pendingRemote, msg.Candidate)
			}
			s.mu.Unlock()
			return
		}
		s.mu.Unlock()
		s.addCandidate(pc, msg.Candidate)
	case "viewport":
		go s.resize(msg.Viewport)
	}
}

func (s *session) addCandidate(pc *webrtc.PeerConnection, c protocol.IceCandidate) {
	index := c.SDPMLineIndex
	_ = pc.AddICECandidate(webrtc.ICECandidateInit{Candidate: c.Candidate, SDPMid: c.SDPMid, SDPMLineIndex: &index})
}

func (s *session) resize(viewport protocol.Viewport) {
	w, h := protocol.ModeFor(&viewport, s.req.Display.Width, s.req.Display.Height)
	s.mu.Lock()
	display, pipeline, closed := s.display, s.pipeline, s.closed
	s.mu.Unlock()
	if display == nil || closed {
		return
	}
	if pipeline != nil {
		pipeline.Hold()
	}
	changed, err := display.Resize(w, h)
	if err != nil {
		s.delegate.Send(protocol.Error("resize_failed", err.Error()))
	}
	if !changed && (pipeline == nil || pipeline.Unhold()) {
		return // same size, and the capture (if any) is still running
	}
	bw, bh := display.Size()
	ew, eh := fitEven(bw, bh)
	s.mu.Lock()
	s.width, s.height = ew, eh
	if s.mapper != nil {
		s.mapper.Bounds = display.Bounds()
	}
	s.mu.Unlock()
	if pipeline == nil {
		return // not streaming yet; startStreaming uses the new size
	}
	if err := pipeline.Start(display.Source(s.req.Settings.FPS), ew, eh, s.req.Settings.FPS, s.bitrate(ew, eh)); err != nil {
		s.end(fmt.Sprintf("capture_failed: %v", err))
		return
	}
	s.delegate.Send(protocol.Status("streaming", ew, eh))
}

func (s *session) handleInput(data []byte) {
	event, err := protocol.DecodeInput(data)
	if err != nil {
		return
	}
	if event.Type == "stats" {
		s.delegate.Stats(event.Stats)
		return
	}
	s.mu.Lock()
	mapper, display := s.mapper, s.display
	if mapper == nil || display == nil || !s.engine.allowInput() {
		s.mu.Unlock()
		return
	}
	mapper.Bounds = display.Bounds()
	mapper.InvertScroll = s.engine.invertScroll()
	actions := mapper.Handle(event)
	s.mu.Unlock()
	s.engine.platform.Inject(actions)
}

func (s *session) end(reason string) {
	s.mu.Lock()
	closed := s.closed
	s.mu.Unlock()
	if closed {
		return
	}
	s.delegate.Ended(reason)
	s.Close()
}

func (s *session) Close() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	pc, pipeline, display, mapper := s.pc, s.pipeline, s.display, s.mapper
	s.pc, s.pipeline, s.display = nil, nil, nil
	s.mu.Unlock()
	if mapper != nil {
		s.engine.platform.Inject(mapper.Reset())
	}
	if pipeline != nil {
		pipeline.Stop()
	}
	if pc != nil {
		_ = pc.Close()
	}
	if display != nil {
		display.Release()
	}
}

// fitEven scales a size into the encoder limits, keeping the aspect ratio, with even sides.
func fitEven(w, h int) (int, int) {
	scale := min(1.0, float64(protocol.MaxPixelWidth)/float64(w), float64(protocol.MaxPixelHeight)/float64(h))
	ew, eh := int(float64(w)*scale)/2*2, int(float64(h)*scale)/2*2
	return max(ew, 2), max(eh, 2)
}
