// Package protocol implements the browser <-> host protocol from docs/PROTOCOL.md.
package protocol

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
)

// Version of the browser <-> host protocol.
const Version = 1

// MaxSignalingMessageBytes caps a single signaling message; SDP offers are typically < 8 KiB.
const MaxSignalingMessageBytes = 64 * 1024

// Viewport is the browser viewport in CSS pixels plus device pixel ratio.
type Viewport struct {
	Width  float64 `json:"width"`
	Height float64 `json:"height"`
	DPR    float64 `json:"dpr"`
}

// Plausible rejects values a real browser cannot produce (NaN, negative, absurdly large).
func (v Viewport) Plausible() bool {
	finite := func(f float64) bool { return !math.IsNaN(f) && !math.IsInf(f, 0) }
	return finite(v.Width) && finite(v.Height) && finite(v.DPR) &&
		v.Width >= 64 && v.Width <= 16384 && v.Height >= 64 && v.Height <= 16384 &&
		v.DPR >= 0.5 && v.DPR <= 4
}

type IceCandidate struct {
	Candidate     string  `json:"candidate"`
	SDPMid        *string `json:"sdpMid,omitempty"`
	SDPMLineIndex uint16  `json:"sdpMLineIndex"`
}

// ClientMessage is a message the browser sends over the signaling WebSocket.
type ClientMessage struct {
	Type      string // hello, answer, candidate, viewport, pong, bye
	Display   int
	Viewport  Viewport
	SDP       string
	Candidate IceCandidate
	ID        int
}

var (
	ErrMalformed = errors.New("malformed")
	ErrTooLarge  = errors.New("invalid value: size")
)

type wireClient struct {
	Type          string    `json:"type"`
	Display       *int      `json:"display"`
	Viewport      *Viewport `json:"viewport"`
	SDP           *string   `json:"sdp"`
	Candidate     *string   `json:"candidate"`
	SDPMid        *string   `json:"sdpMid"`
	SDPMLineIndex *uint16   `json:"sdpMLineIndex"`
	ID            *int      `json:"id"`
}

// DecodeClient parses and validates one signaling message from the browser.
func DecodeClient(data []byte) (ClientMessage, error) {
	if len(data) > MaxSignalingMessageBytes {
		return ClientMessage{}, ErrTooLarge
	}
	var w wireClient
	if err := json.Unmarshal(data, &w); err != nil || w.Type == "" {
		return ClientMessage{}, ErrMalformed
	}
	m := ClientMessage{Type: w.Type}
	missing := func(f string) error { return fmt.Errorf("missing field: %s", f) }
	invalid := func(f string) error { return fmt.Errorf("invalid value: %s", f) }
	switch w.Type {
	case "hello", "viewport":
		if w.Viewport == nil {
			return m, missing("viewport")
		}
		if !w.Viewport.Plausible() {
			return m, invalid("viewport")
		}
		m.Viewport = *w.Viewport
		if w.Type == "hello" && w.Display != nil {
			if *w.Display < 0 || *w.Display >= 64 {
				return m, invalid("display")
			}
			m.Display = *w.Display
		}
	case "answer":
		if w.SDP == nil || *w.SDP == "" {
			return m, missing("sdp")
		}
		m.SDP = *w.SDP
	case "candidate":
		if w.Candidate == nil {
			return m, missing("candidate")
		}
		m.Candidate = IceCandidate{Candidate: *w.Candidate, SDPMid: w.SDPMid}
		if w.SDPMLineIndex != nil {
			m.Candidate.SDPMLineIndex = *w.SDPMLineIndex
		}
	case "pong":
		if w.ID == nil {
			return m, missing("id")
		}
		m.ID = *w.ID
	case "bye":
	default:
		return m, fmt.Errorf("unknown type: %s", w.Type)
	}
	return m, nil
}

// ServerMessage is a message the host sends; absent fields are omitted from the JSON.
type ServerMessage struct {
	Type          string  `json:"type"`
	SDP           *string `json:"sdp,omitempty"`
	Candidate     *string `json:"candidate,omitempty"`
	SDPMid        *string `json:"sdpMid,omitempty"`
	SDPMLineIndex *uint16 `json:"sdpMLineIndex,omitempty"`
	ID            *int    `json:"id,omitempty"`
	LatencyMs     *int    `json:"latencyMs,omitempty"`
	State         string  `json:"state,omitempty"`
	Width         *int    `json:"width,omitempty"`
	Height        *int    `json:"height,omitempty"`
	Code          string  `json:"code,omitempty"`
	Message       string  `json:"message,omitempty"`
	Reason        string  `json:"reason,omitempty"`
}

func Offer(sdp string) ServerMessage { return ServerMessage{Type: "offer", SDP: &sdp} }

func Candidate(c IceCandidate) ServerMessage {
	i := c.SDPMLineIndex
	return ServerMessage{Type: "candidate", Candidate: &c.Candidate, SDPMid: c.SDPMid, SDPMLineIndex: &i}
}

func Ping(id int, latencyMs *int) ServerMessage {
	return ServerMessage{Type: "ping", ID: &id, LatencyMs: latencyMs}
}

// Status reports "preparing" (no size yet) or "streaming" with the encoded size in pixels.
func Status(state string, width, height int) ServerMessage {
	m := ServerMessage{Type: "status", State: state}
	if width > 0 && height > 0 {
		m.Width, m.Height = &width, &height
	}
	return m
}

func Error(code, message string) ServerMessage {
	return ServerMessage{Type: "error", Code: code, Message: message}
}

func Bye(reason string) ServerMessage { return ServerMessage{Type: "bye", Reason: reason} }

// Encode cannot fail for these plain types.
func (m ServerMessage) Encode() []byte {
	data, err := json.Marshal(m)
	if err != nil {
		return []byte(`{"type":"error"}`)
	}
	return data
}
