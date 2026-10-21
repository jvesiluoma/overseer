package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/jvesiluoma/overseer/WindowsHost/internal/protocol"
)

const (
	pingInterval      = 2 * time.Second
	pongTimeout       = 10 * time.Second
	maxProtocolErrors = 5
)

// outlet sends signaling messages to one WebSocket from any goroutine.
type outlet struct {
	conn      *websocket.Conn
	hub       *Hub
	sessionID func() string
	closeOnce sync.Once
}

func (o *outlet) Send(msg protocol.ServerMessage) {
	ctx, cancel := withTimeout(5 * time.Second)
	defer cancel()
	if o.conn.Write(ctx, websocket.MessageText, msg.Encode()) != nil {
		o.Close()
	}
}

func (o *outlet) Stats(stats protocol.ClientStats) {
	if id := o.sessionID(); id != "" {
		o.hub.Update(id, nil, &stats)
	}
}

func (o *outlet) Ended(reason string) {
	o.Send(protocol.Bye(reason))
	o.Close()
}

func (o *outlet) Close() {
	o.closeOnce.Do(func() {
		// The close handshake waits for the peer; don't block the caller.
		go func() { _ = o.conn.Close(websocket.StatusNormalClosure, "") }()
	})
}

// signaling is one WebSocket: hello -> media session, relays answer/candidates, keeps a ping clock.
type signaling struct {
	ctx    *Context
	engine MediaEngine
	remote string
	out    *outlet

	mu             sync.Mutex
	session        Session
	id             string
	pingID         int
	pingSentAt     map[int]time.Time
	lastPong       time.Time
	latencyMs      *int
	protocolErrors int
}

func runSignaling(ctx *Context, engine MediaEngine, conn *websocket.Conn, remote string) {
	conn.SetReadLimit(protocol.MaxSignalingMessageBytes)
	s := &signaling{ctx: ctx, engine: engine, remote: remote, pingSentAt: map[int]time.Time{}, lastPong: time.Now()}
	s.out = &outlet{conn: conn, hub: ctx.Hub, sessionID: func() string { s.mu.Lock(); defer s.mu.Unlock(); return s.id }}

	done := make(chan struct{})
	go s.pingLoop(done)
	defer func() {
		close(done)
		s.teardown()
	}()
	for {
		typ, data, err := conn.Read(context.Background())
		if err != nil {
			return
		}
		if typ == websocket.MessageText {
			s.handleText(data)
		}
	}
}

func (s *signaling) handleText(data []byte) {
	msg, err := protocol.DecodeClient(data)
	if err != nil {
		s.mu.Lock()
		s.protocolErrors++
		tooMany := s.protocolErrors >= maxProtocolErrors
		s.mu.Unlock()
		s.out.Send(protocol.Error("bad_message", err.Error()))
		if tooMany {
			s.out.Close()
		}
		return
	}
	switch msg.Type {
	case "hello":
		s.startSession(msg.Display, msg.Viewport)
	case "answer", "candidate", "viewport":
		s.mu.Lock()
		session := s.session
		s.mu.Unlock()
		if session != nil {
			session.Handle(msg)
		}
	case "pong":
		now := time.Now()
		s.mu.Lock()
		s.lastPong = now
		sent, ok := s.pingSentAt[msg.ID]
		delete(s.pingSentAt, msg.ID)
		id := s.id
		var ms int
		if ok {
			ms = int(now.Sub(sent).Round(time.Millisecond) / time.Millisecond)
			s.latencyMs = &ms
		}
		s.mu.Unlock()
		if ok && id != "" {
			s.ctx.Hub.Update(id, &ms, nil)
		}
	case "bye":
		s.out.Close()
	}
}

func (s *signaling) startSession(display int, viewport protocol.Viewport) {
	s.mu.Lock()
	started := s.session != nil || s.id != ""
	s.mu.Unlock()
	if started {
		s.out.Send(protocol.Error("already_started", "hello was already sent"))
		return
	}
	st := s.ctx.Settings()
	if display < 0 || display >= len(st.Displays) {
		s.out.Send(protocol.Error("no_such_display", "display is not configured"))
		return
	}
	id := makeSessionID()
	s.mu.Lock()
	s.id = id
	s.mu.Unlock()
	out := s.out
	total := s.ctx.Hub.Register(ConnectedClient{ID: id, DisplayIndex: display, RemoteAddress: s.remote, ConnectedAt: time.Now()},
		func(reason string) {
			out.Send(protocol.Bye(reason))
			out.Close()
		})
	session, err := s.engine.MakeSession(SessionRequest{ID: id, Display: st.Displays[display], Viewport: viewport,
		Settings: st, ConcurrentSessions: total}, out)
	if err != nil {
		s.ctx.Hub.Unregister(id)
		s.out.Send(protocol.Error("start_failed", err.Error()))
		s.out.Close()
		return
	}
	s.mu.Lock()
	s.session = session
	s.mu.Unlock()
}

func (s *signaling) pingLoop(done chan struct{}) {
	ticker := time.NewTicker(pingInterval)
	defer ticker.Stop()
	for {
		select {
		case <-done:
			return
		case now := <-ticker.C:
			s.mu.Lock()
			if now.Sub(s.lastPong) > pongTimeout {
				s.mu.Unlock()
				s.out.Send(protocol.Bye("timeout"))
				s.out.Close()
				return
			}
			s.pingID++
			id := s.pingID
			s.pingSentAt[id] = now
			for k, t := range s.pingSentAt {
				if now.Sub(t) >= pongTimeout {
					delete(s.pingSentAt, k)
				}
			}
			latency := s.latencyMs
			s.mu.Unlock()
			s.out.Send(protocol.Ping(id, latency))
		}
	}
}

func (s *signaling) teardown() {
	s.mu.Lock()
	session, id := s.session, s.id
	s.session = nil
	s.mu.Unlock()
	if session != nil {
		session.Close()
	}
	if id != "" {
		s.ctx.Hub.Unregister(id)
	}
	s.out.Close()
}

func makeSessionID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
