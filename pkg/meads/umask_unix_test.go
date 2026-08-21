//go:build !windows

package meads

import (
	"syscall"
	"testing"
)

// withUmask pins the process umask for the duration of a test, restoring it
// afterwards.
//
// Mode-preservation tests are otherwise a coin flip on whoever runs them: a
// developer with umask 002 sees 0664 survive a create, CI with umask 022 sees
// it masked to 0644. That is exactly how a silently-skipped chmod reached CI
// green-on-my-machine (see Store.chmod). Setting a masking umask here makes
// the test prove the chmod actually ran, everywhere.
//
// The umask is process-global and this is not safe alongside t.Parallel; no
// test in this package uses it.
func withUmask(t *testing.T, mask int) {
	t.Helper()
	prev := syscall.Umask(mask)
	t.Cleanup(func() { syscall.Umask(prev) })
}
