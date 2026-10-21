package settings

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSanitized(t *testing.T) {
	s := Default()
	s.HTTPSPort, s.FPS, s.MaxBitrateMbps, s.Mode = 0, 25, 500, "weird"
	s.Displays = []Display{{ID: 7, Name: "a", Width: 100}, {}, {}, {}, {}, {}}
	got := s.Sanitized()
	if got.HTTPSPort != 443 || got.FPS != 30 || got.MaxBitrateMbps != 50 || got.Mode != ModeExtend {
		t.Fatalf("%+v", got)
	}
	if len(got.Displays) != MaxDisplays || got.Displays[0].ID != 0 || got.Displays[0].Width != 0 {
		t.Fatalf("displays: %+v", got.Displays)
	}
	if s.Displays[0].ID != 7 {
		t.Fatal("Sanitized changed its receiver's displays")
	}
}

func TestLoadKeepsDefaultsForMissingKeysAndSurvivesGarbage(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	_ = os.WriteFile(path, []byte(`{"allowInput":false,"mode":"mirror"}`), 0o600)
	s := Load(path)
	if s.AllowInput || s.Mode != ModeMirror || s.HTTPSPort != 443 || !s.RequirePairing {
		t.Fatalf("%+v", s)
	}
	_ = os.WriteFile(path, []byte(`{garbage`), 0o600)
	if Load(path).Mode != ModeExtend {
		t.Fatal("garbage not replaced by defaults")
	}
	s.FPS = 60
	if err := Save(path, s); err != nil || Load(path).FPS != 60 {
		t.Fatalf("round trip: %v", err)
	}
}
