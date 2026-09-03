//go:build unix

package meads

import (
	"errors"
	"fmt"
	"os"
	"syscall"
)

// platformTryLock attempts to acquire an exclusive, non-blocking advisory
// lock on f using flock(2), returning (false, nil) - not an error - if
// another process already holds it, so acquireLocal can tell contention
// apart from a genuine failure.
//
// flock specifically - NOT a lock recorded as file CONTENT, which is what
// the file-mode Store.acquireLock in lock.go does, and what an earlier
// draft of git mode's design considered and rejected for this - because
// flock(2)'s lock is owned by the kernel's open-file-description table, not
// by anything written into the file. The kernel unconditionally drops it
// the moment the owning file descriptor closes, for ANY reason: a clean
// exit, an uncaught panic, SIGKILL, or the OOM killer. A lock recorded as
// data can never have that property - whatever process was supposed to
// clear it might die before doing so, and then every future acquirer has
// to guess whether the recorded holder is still alive or the record is
// just stale. That guesswork is precisely the expiry logic
// acquireLock/releaseLock (lock.go) has to carry for the file-mode
// advisory lock, and precisely what LOCAL locking in git mode does not
// need: a dead holder's flock is already gone by the time anyone else
// asks, with no expiry, no heartbeat, and no stale-lock detection required
// (contrast the REMOTE stage's lease/steal logic in gitlock.go's
// acquireRemote, which genuinely does need an expiry - because a remote ref
// has no kernel to clean it up on process death).
//
// LOCK_NB (non-blocking) rather than a blocking flock call is a deliberate
// choice, not an oversight: Acquire has no timeout/context parameter to
// bound a blocking wait by, and a lock that can hang a caller forever with
// no way to bound it is worse than one that fails fast and lets the caller
// decide whether/when to retry.
func platformTryLock(f *os.File) (bool, error) {
	err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, syscall.EWOULDBLOCK) {
		return false, nil
	}
	return false, fmt.Errorf("flock %s: %w", f.Name(), err)
}

// platformUnlock releases f's flock(2) lock. Closing f alone would also
// release it - flock(2)'s lock belongs to the open file description, and
// this package never dup(2)s the fd - but unlocking explicitly keeps this
// symmetric with the Windows implementation's LockFileEx/UnlockFileEx
// pairing (gitlock_windows.go), where the lock and the handle are more
// clearly distinct operations.
func platformUnlock(f *os.File) error {
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_UN); err != nil {
		return fmt.Errorf("unflock %s: %w", f.Name(), err)
	}
	return nil
}
