//go:build !windows

package main

import (
	"errors"

	"github.com/jvesiluoma/overseer/WindowsHost/internal/media"
)

func nativePlatform() (media.Platform, error) {
	return nil, errors.New("--media native needs Windows; use --media test")
}
