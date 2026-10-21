package server

import (
	"sync"

	"github.com/jvesiluoma/overseer/WindowsHost/internal/protocol"
	"github.com/jvesiluoma/overseer/WindowsHost/internal/settings"
)

// SessionRequest is what the signaling layer asks the media engine to start.
type SessionRequest struct {
	ID       string
	Display  settings.Display
	Viewport protocol.Viewport
	Settings settings.Settings
	// Number of sessions (including this one) sharing the bitrate budget.
	ConcurrentSessions int
}

// SessionDelegate receives callbacks from a media session; safe to call from any goroutine.
type SessionDelegate interface {
	Send(msg protocol.ServerMessage)
	Stats(stats protocol.ClientStats)
	// Ended: the engine ended the session on its own (capture failed, peer connection failed, …).
	Ended(reason string)
}

// Session is one streaming peer. Handle receives answer/candidate/viewport messages.
type Session interface {
	Handle(msg protocol.ClientMessage)
	Close()
}

// MediaEngine is the platform media stack (virtual display + capture + WebRTC).
type MediaEngine interface {
	MakeSession(req SessionRequest, delegate SessionDelegate) (Session, error)
}

// PlaceholderOffer is what FakeEngine offers: enough for the protocol, no media.
const PlaceholderOffer = "v=0\r\no=- 0 0 IN IP4 127.0.0.1\r\ns=overscreen-fake\r\nt=0 0\r\n"

// FakeEngine completes signaling without media; used by the dev server and tests.
type FakeEngine struct {
	mu       sync.Mutex
	Sessions []*FakeSession
}

type FakeSession struct {
	Request  SessionRequest
	delegate SessionDelegate
	mu       sync.Mutex
	Received []protocol.ClientMessage
	Closed   bool
}

func (e *FakeEngine) MakeSession(req SessionRequest, delegate SessionDelegate) (Session, error) {
	s := &FakeSession{Request: req, delegate: delegate}
	e.mu.Lock()
	e.Sessions = append(e.Sessions, s)
	e.mu.Unlock()
	delegate.Send(protocol.Status("preparing", 0, 0))
	delegate.Send(protocol.Offer(PlaceholderOffer))
	return s, nil
}

func (s *FakeSession) Handle(msg protocol.ClientMessage) {
	s.mu.Lock()
	s.Received = append(s.Received, msg)
	s.mu.Unlock()
	if msg.Type == "answer" {
		w, h := protocol.ModeFor(&s.Request.Viewport, s.Request.Display.Width, s.Request.Display.Height)
		s.delegate.Send(protocol.Status("streaming", w, h))
	}
}

func (s *FakeSession) Close() {
	s.mu.Lock()
	s.Closed = true
	s.mu.Unlock()
}
