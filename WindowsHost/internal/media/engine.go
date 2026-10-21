package media

import (
	"fmt"
	"sync"

	"github.com/jvesiluoma/overseer/WindowsHost/internal/server"
	"github.com/jvesiluoma/overseer/WindowsHost/internal/settings"
	"github.com/pion/webrtc/v4"
)

// Engine implements server.MediaEngine on top of a Platform.
type Engine struct {
	platform Platform
	api      *webrtc.API
	ffmpeg   string
	encoder  Encoder

	mu       sync.Mutex
	settings settings.Settings
}

// NewEngine finds FFmpeg and picks a working H.264 encoder (this can take a few seconds once).
func NewEngine(platform Platform, s settings.Settings) (*Engine, error) {
	ffmpeg, err := FFmpegPath()
	if err != nil {
		return nil, err
	}
	encoder, err := PickEncoder(ffmpeg)
	if err != nil {
		return nil, err
	}
	api, err := newAPI()
	if err != nil {
		return nil, fmt.Errorf("webrtc: %w", err)
	}
	return &Engine{platform: platform, api: api, ffmpeg: ffmpeg, encoder: encoder, settings: s}, nil
}

// EncoderName is shown in the UI ("h264_nvenc", "libx264", …).
func (e *Engine) EncoderName() string { return e.encoder.Name }

func (e *Engine) SetSettings(s settings.Settings) {
	e.mu.Lock()
	e.settings = s
	e.mu.Unlock()
}

func (e *Engine) allowInput() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.settings.AllowInput
}

func (e *Engine) invertScroll() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.settings.InvertScroll
}

func (e *Engine) MakeSession(req server.SessionRequest, delegate server.SessionDelegate) (server.Session, error) {
	s := &session{engine: e, req: req, delegate: delegate}
	go s.start()
	return s, nil
}
