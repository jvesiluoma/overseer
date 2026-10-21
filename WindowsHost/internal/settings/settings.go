// Package settings holds the Windows host's user settings, saved as JSON in %APPDATA%\Overscreen.
package settings

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
)

type Display struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
	// Fixed size in pixels; 0 x 0 follows the browser's viewport.
	Width  int `json:"width,omitempty"`
	Height int `json:"height,omitempty"`
}

const (
	// ModeExtend adds a virtual monitor (needs the Virtual Display Driver).
	ModeExtend = "extend"
	// ModeMirror streams an existing monitor; works without any driver.
	ModeMirror = "mirror"
)

type Settings struct {
	// Encrypted listener for the web client, pairing and signaling.
	HTTPSPort         int `json:"httpsPort"`
	HTTPSFallbackPort int `json:"httpsFallbackPort"`
	// Unencrypted listener. It only redirects to HTTPS unless AllowInsecureHTTP is on.
	Port         int `json:"port"`
	FallbackPort int `json:"fallbackPort"`
	// Serve the full client over plain HTTP too, for browsers that refuse self-signed
	// certificates. Anyone on the network can then read the pairing code and session token.
	AllowInsecureHTTP bool      `json:"allowInsecureHTTP"`
	RequirePairing    bool      `json:"requirePairing"`
	AllowInput        bool      `json:"allowInput"`
	InvertScroll      bool      `json:"invertScroll"`
	FPS               int       `json:"fps"`
	MaxBitrateMbps    float64   `json:"maxBitrateMbps"`
	Displays          []Display `json:"displays"`
	Mode              string    `json:"mode"`
	// GDI device name (\\.\DISPLAY2) of the monitor to mirror; empty means the primary one.
	MirrorMonitor string `json:"mirrorMonitor"`
	// Extra host names accepted in the Host header (besides IP literals, localhost and *.local).
	ExtraHostNames []string `json:"extraHostNames"`
}

var SupportedFPS = []int{24, 30, 60}

const MaxDisplays = 4

func Default() Settings {
	return Settings{
		HTTPSPort: 443, HTTPSFallbackPort: 8443, Port: 80, FallbackPort: 8080,
		RequirePairing: true, AllowInput: true, FPS: 30, MaxBitrateMbps: 8,
		Displays: []Display{{ID: 0, Name: "Display 1"}}, Mode: ModeExtend,
		ExtraHostNames: []string{"go.ss"},
	}
}

// Sanitized clamps values that may come from a hand-edited or older settings file.
func (s Settings) Sanitized() Settings {
	d := Default()
	port := func(v *int, def int) {
		if *v < 1 || *v > 65535 {
			*v = def
		}
	}
	port(&s.HTTPSPort, d.HTTPSPort)
	port(&s.HTTPSFallbackPort, d.HTTPSFallbackPort)
	port(&s.Port, d.Port)
	port(&s.FallbackPort, d.FallbackPort)
	if !slices.Contains(SupportedFPS, s.FPS) {
		s.FPS = d.FPS
	}
	s.MaxBitrateMbps = min(max(s.MaxBitrateMbps, 1), 50)
	s.Displays = slices.Clone(s.Displays)
	if len(s.Displays) == 0 {
		s.Displays = d.Displays
	}
	if len(s.Displays) > MaxDisplays {
		s.Displays = s.Displays[:MaxDisplays]
	}
	for i := range s.Displays {
		s.Displays[i].ID = i
		if s.Displays[i].Width <= 0 || s.Displays[i].Height <= 0 {
			s.Displays[i].Width, s.Displays[i].Height = 0, 0
		}
	}
	if s.Mode != ModeMirror {
		s.Mode = ModeExtend
	}
	if s.ExtraHostNames == nil {
		s.ExtraHostNames = d.ExtraHostNames
	}
	return s
}

// Load reads the settings; missing keys keep their defaults and a broken file gives defaults.
func Load(path string) Settings {
	s := Default()
	if data, err := os.ReadFile(path); err == nil {
		if json.Unmarshal(data, &s) != nil {
			s = Default()
		}
	}
	return s.Sanitized()
}

func Save(path string, s Settings) error {
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
