//go:build windows

package meads

import (
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/windows"
)

// This is a REAL implementation, not a stub - see the dependency note below
// for why that was possible within task 64's "no new module dependencies"
// constraint.
//
// platformTryLock attempts to acquire an exclusive, non-blocking advisory
// lock on f via LockFileEx - Windows's flock(2) analogue for this design's
// purposes: the lock belongs to the open HANDLE, not to anything written
// into the file, and Windows releases it automatically the instant that
// handle closes, including on an ungraceful process exit (crash, or a
// forceful TerminateProcess/taskkill). That is the same leak-proof
// guarantee unix's flock gives (see gitlock_unix.go's doc comment on
// platformTryLock for the full reasoning on why that property - not a lock
// recorded as file content - is the whole point of this design).
//
// Dependency note (task 64): golang.org/x/sys was, before this file,
// already present in go.mod as an INDIRECT dependency at v0.31.0 (pulled in
// transitively via github.com/go-git/go-billy/v5/osfs, which this package
// already imports directly - see store.go's import of go-billy/v5/osfs).
// go.sum already carried x/sys's full content hash, and BOTH its unix and
// windows subpackages were already present in the local module cache
// before this file imported either. Importing golang.org/x/sys/windows
// here therefore promotes an already-resolved, already-hashed,
// already-downloaded dependency from indirect to direct - go.sum gains no
// new module, and no network fetch is needed - rather than introducing a
// new one. That is why this is a real implementation rather than the
// documented-stub fallback task 64 allows for when a genuinely new
// dependency would otherwise be required.
//
// Locks byte 0 of the file (bytesLow=1, bytesHigh=0) as a whole-file
// stand-in, matching the common convention other Go file-lock wrappers use
// on Windows: LockFileEx locks byte RANGES, but this package never reads or
// writes the file's content, so a single fixed byte range is all "locked"
// ever needs to mean.
func platformTryLock(f *os.File) (bool, error) {
	ol := new(windows.Overlapped)
	err := windows.LockFileEx(
		windows.Handle(f.Fd()),
		windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY,
		0, 1, 0, ol,
	)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
		return false, nil
	}
	return false, fmt.Errorf("LockFileEx %s: %w", f.Name(), err)
}

// platformUnlock releases f's LockFileEx lock (see platformTryLock), over
// the identical byte range that was locked.
func platformUnlock(f *os.File) error {
	ol := new(windows.Overlapped)
	if err := windows.UnlockFileEx(windows.Handle(f.Fd()), 0, 1, 0, ol); err != nil {
		return fmt.Errorf("UnlockFileEx %s: %w", f.Name(), err)
	}
	return nil
}
