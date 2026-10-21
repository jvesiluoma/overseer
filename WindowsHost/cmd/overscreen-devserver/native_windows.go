package main

import (
	"github.com/jvesiluoma/overseer/WindowsHost/internal/media"
	"github.com/jvesiluoma/overseer/WindowsHost/internal/winplat"
)

// nativePlatform is the real Windows platform: virtual display driver or mirror, Desktop
// Duplication capture and SendInput.
func nativePlatform() (media.Platform, error) {
	winplat.EnablePerMonitorDPIAwareness()
	return winplat.NewPlatform(), nil
}
