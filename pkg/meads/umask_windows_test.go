//go:build windows

package meads

import "testing"

// withUmask is a no-op on Windows, which has no umask and no POSIX permission
// bits for a chmod to preserve - see the unix build of this file for what it
// does elsewhere.
func withUmask(t *testing.T, mask int) {
	t.Helper()
}
