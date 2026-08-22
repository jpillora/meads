//go:build windows

package main

import (
	"errors"
	"time"
)

func enqueueBackgroundSync(_ *globals, _ string, _, _ time.Duration) error {
	return errors.New("detached background sync is not supported on Windows; run 'md sync'")
}

func syncDaemonDispatch(_ *globals) (bool, error) { return false, nil }
