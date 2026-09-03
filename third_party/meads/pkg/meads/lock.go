package meads

import (
	"crypto/rand"
	"errors"
	"fmt"
	mrand "math/rand/v2"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/go-git/go-billy/v5"
	"github.com/go-git/go-billy/v5/util"
)

// ErrLockContention reports that another writer holds the file-mode lock.
// Callers only ever see it once acquireLock's retry budget is spent.
var ErrLockContention = errors.New("lock contention: another writer holds the lock")

// errLockLineLost reports that our own lock line vanished between appending it
// and reading it back. releaseLock rewrites the whole file, so an append that
// interleaves with the holder's release is simply erased. Like contention this
// is a lost race rather than a failure, so it retries - but it stays internal,
// since to a caller the two are the same "someone else got there first".
var errLockLineLost = errors.New("lock line not found after write")

// lockBackoff is the delay before each retry after losing the append race.
// These are ceilings, not the actual waits (see the jitter note below): the
// sequence gives up after at most ~1.6s, typically ~0.8s. Overridden in tests.
//
// Full jitter is applied on top - each sleep is drawn uniformly from (0, d].
// Unlike git's index.lock, which one short command holds (see cmd/md's
// stageBackoff), this lock is contended by every concurrent `md` process at
// once. A fixed table would wake all the losers of one race together to
// re-collide on the same append, which is precisely the many-way contention
// this retry exists to survive.
var lockBackoff = []time.Duration{
	2 * time.Millisecond,
	5 * time.Millisecond,
	10 * time.Millisecond,
	25 * time.Millisecond,
	50 * time.Millisecond,
	100 * time.Millisecond,
	200 * time.Millisecond,
	400 * time.Millisecond,
	400 * time.Millisecond,
	400 * time.Millisecond,
}

// acquireLock takes the file lock, retrying while another writer holds it.
// Returns the lock ID and file contents (without any lock lines) on success.
//
// A single attempt is not enough for the tool's headline use case - several
// agents running `md` against one TASKS.md - because tryAcquireLock's race has
// exactly one winner per round: at 25-way contention up to 40% of `md add`
// calls used to exit 1 rather than land. No write was ever lost (a loser never
// reaches releaseLock), so this is a liveness fix, not a safety one. Git mode
// already had the equivalent in gitmutate.go's maxCASRetries; the difference is
// that a CAS loser can re-read and retry immediately, whereas here the winner
// has to release first, so these attempts must sleep in between.
//
// A lock that outlives the backoff is not contention but a crashed writer that
// left its line behind, which no amount of waiting fixes - until the 60s
// expiry in tryAcquireLock steps over it.
func (s *Store) acquireLock() (string, string, error) {
	for attempt := 0; ; attempt++ {
		id, content, err := s.tryAcquireLock()
		if err == nil {
			return id, content, nil
		}
		lost := errors.Is(err, ErrLockContention) || errors.Is(err, errLockLineLost)
		if !lost {
			return "", "", err // a real I/O failure; retrying cannot help
		}
		if attempt >= len(lockBackoff) {
			return "", "", fmt.Errorf("%w (gave up after %d attempts)", ErrLockContention, attempt+1)
		}
		time.Sleep(jitter(lockBackoff[attempt]))
	}
}

// jitter returns a duration drawn uniformly from (0, d], the "full jitter"
// policy - see lockBackoff for why the spread matters here.
func jitter(d time.Duration) time.Duration {
	if d <= 0 {
		return 0
	}
	return time.Duration(mrand.Int64N(int64(d)) + 1)
}

// tryAcquireLock makes one attempt: it appends a lock line to the file and
// checks whether we won the race. Callers want acquireLock, which wraps this
// in the retry loop; this is separate so the race semantics stay directly
// testable without waiting out a backoff.
func (s *Store) tryAcquireLock() (string, string, error) {
	id, err := randomID()
	if err != nil {
		return "", "", fmt.Errorf("generating lock id: %w", err)
	}
	now := time.Now().Unix()
	lockLine := fmt.Sprintf("\nlock:%s:%d\n", id, now)
	f, err := s.fs.OpenFile(s.file, os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return "", "", fmt.Errorf("opening %s for lock: %w", s.file, err)
	}
	_, err = f.Write([]byte(lockLine))
	f.Close()
	if err != nil {
		return "", "", fmt.Errorf("writing lock: %w", err)
	}
	// Read back and check if we hold the lock.
	data, err := util.ReadFile(s.fs, s.file)
	if err != nil {
		return "", "", fmt.Errorf("reading %s after lock: %w", s.file, err)
	}
	content := string(data)
	// Find the first non-expired lock line.
	for _, line := range strings.Split(content, "\n") {
		if !strings.HasPrefix(line, "lock:") {
			continue
		}
		// Parse timestamp from lock:<id>:<timestamp>
		parts := strings.SplitN(line, ":", 3)
		if len(parts) != 3 {
			continue // malformed lock line, skip
		}
		ts, err := strconv.ParseInt(parts[2], 10, 64)
		if err != nil {
			continue // malformed timestamp, skip
		}
		// Expired locks (>60s) are ignored
		if now-ts > 60 {
			continue
		}
		if parts[1] == id {
			// We won. Strip all lock lines and return clean content.
			// The lock line is appended as "\nlock:...\n"; after stripping,
			// the leading "\n" remains as a trailing blank line. Normalize
			// to a single trailing newline.
			clean := strings.TrimRight(stripLockLines(content), "\n") + "\n"
			return id, clean, nil
		}
		// Someone else won.
		return "", "", ErrLockContention
	}
	return "", "", errLockLineLost
}

// releaseLock writes the final content back to the file, removing all lock lines.
//
// The write must be atomic, because releasing is what ends the lock: our line
// is gone the instant the new content lands, so the next writer can win the
// moment it does. util.WriteFile is O_TRUNC-then-write, which opens a window
// where the file is EMPTY and holds no lock line at all - a writer that
// appends and reads back inside that window wins the lock and reads back
// nothing, then commits a task list built on empty content, silently dropping
// everything we were mid-way through writing. That is not theoretical: 25
// concurrent Adds under -race lost ~8 tasks a run and handed the same id to
// two of them (task 68).
//
// Writing a sibling and renaming closes it. A reader sees either the whole
// old file (lock line intact, so it waits) or the whole new one (no lock
// line, so it wins legitimately) - never a torn state in between.
func (s *Store) releaseLock(content string) error {
	clean := stripLockLines(content)
	return s.atomicWrite([]byte(clean))
}

// atomicWrite replaces s.file with data in one indivisible step.
//
// The temp name carries a random suffix rather than a fixed one: releases can
// overlap (a writer whose lock expired is still entitled to finish), and two
// of them sharing a scratch path would corrupt each other's payload.
//
// Atomic here means "no reader ever sees a torn file", not "durable": rename
// orders nothing against the data blocks, so a power loss right after one can
// in principle expose the new name with none of its content. billy.File has no
// Sync to fix that with, and the failure mode this exists to prevent is a
// concurrent reader, not a crash, so that gap is accepted rather than closed.
//
// Two file identities cannot survive a rename, because rename replaces the
// directory entry rather than the bytes behind it:
//
//   - A SYMLINKED tasks file would be replaced by a regular file, orphaning
//     the real target - so symlinks take the old non-atomic path instead. That
//     keeps the torn-read race for them, which is strictly what they had
//     before; silently forking someone's shared task list in two is worse.
//   - HARD LINKS to the tasks file are broken: the other name keeps the old
//     content. Detecting that needs a platform-specific stat billy does not
//     expose, so it is a documented limitation, not a guarded case.
func (s *Store) atomicWrite(data []byte) error {
	mode := os.FileMode(0644)
	if fi, err := s.lstat(s.file); err == nil {
		if fi.Mode()&os.ModeSymlink != 0 {
			return util.WriteFile(s.fs, s.file, data, mode)
		}
		// Carry the existing mode across. Without this, one write would
		// silently downgrade a group-writable 0664 checkout to 0644 and lock
		// every other user out of the shared file.
		mode = fi.Mode().Perm()
	}
	suffix, err := randomID()
	if err != nil {
		return fmt.Errorf("generating temp name: %w", err)
	}
	dir, base := filepath.Split(s.file)
	tmp := filepath.Join(dir, "."+base+".tmp-"+suffix)
	if err := util.WriteFile(s.fs, tmp, data, mode); err != nil {
		s.fs.Remove(tmp)
		return err
	}
	// util.WriteFile's mode only applies when it CREATES the file, and even
	// then umask masks it, so set it explicitly. Best-effort: a filesystem
	// with no way to chmod is not a reason to abandon an otherwise good write.
	_ = s.chmod(tmp, mode)
	if err := s.fs.Rename(tmp, s.file); err != nil {
		s.fs.Remove(tmp)
		return fmt.Errorf("replacing %s: %w", s.file, err)
	}
	return nil
}

// chmod sets name's mode within this Store's filesystem.
//
// go-billy offers no single working route for this. osfs.New returns a
// *chroot.ChrootHelper, which does NOT satisfy billy.Change, and whose own
// Chmod method fails outright with "underlying fs does not implement
// billy.Chmod". memfs, meanwhile, chmods fine. So try the filesystem first and
// fall back to the real one underneath it.
//
// Root() is the directory an osfs Store is chrooted to, so joining it back
// onto name recovers the real path. That fallback is only ever reached when
// the billy attempt failed, which an in-memory filesystem's never does - so a
// virtual path cannot escape into os.Chmod.
//
// Getting this wrong is silent: a skipped chmod leaves util.WriteFile's
// umask-masked create mode, so a group-writable 0664 tasks file quietly
// becomes 0644 and locks out every other user of a shared checkout. It only
// shows up under a umask that actually masks - which is why the test for it
// sets one rather than trusting the developer's.
func (s *Store) chmod(name string, mode os.FileMode) error {
	if ch, ok := s.fs.(interface {
		Chmod(string, os.FileMode) error
	}); ok {
		if err := ch.Chmod(name, mode); err == nil {
			return nil
		}
	}
	return os.Chmod(filepath.Join(s.fs.Root(), name), mode)
}

// lstat stats s.file without following a final symlink. Not every
// billy.Filesystem implements billy.Symlink (osfs and memfs both do); one that
// does not simply reports "no such file", which atomicWrite reads as "nothing
// to preserve" and handles correctly.
func (s *Store) lstat(name string) (os.FileInfo, error) {
	if sl, ok := s.fs.(billy.Symlink); ok {
		return sl.Lstat(name)
	}
	return s.fs.Stat(name)
}

func stripLockLines(s string) string {
	lines := strings.Split(s, "\n")
	var out []string
	for _, line := range lines {
		if !strings.HasPrefix(line, "lock:") {
			out = append(out, line)
		}
	}
	return strings.Join(out, "\n")
}

func randomID() (string, error) {
	b := make([]byte, 16)
	_, err := rand.Read(b)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", b), nil
}
