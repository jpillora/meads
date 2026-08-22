package main

import "time"

// Most command tests call Run methods in-process and are about task semantics,
// not process management. Never let those tests leak detached workers. The
// daemon-specific tests invoke enqueueBackgroundSync directly.
func init() {
	enqueueSyncFunc = func(*globals, string, time.Duration, time.Duration) error { return nil }
}
