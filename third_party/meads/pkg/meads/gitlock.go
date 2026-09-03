package meads

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// This file implements git mode's two-stage, opt-in lock (design of record:
// TASKS.md task 57 "Locking (two-stage, opt-in)"; built here as task 64).
//
// Per-ref compare-and-swap (refstore.go, gitmutate.go) already prevents lost
// updates, so THIS lock is not what makes an ordinary `md update` safe. It
// exists only for rare multi-step operations that cannot be expressed as one
// atomic batch (a format migration, long maintenance). It is off by default
// (Config.RemoteLocking, gitconfig.go), which is also what preserves offline
// operation. Nothing in this package calls Acquire on any mutation's behalf;
// wiring it into a specific call site is deliberately left to later work.
//
// Stage 1 (mandatory, always taken) is a local flock(2)/LockFileEx advisory
// lock on a file under the git COMMON directory - see gitlock_unix.go's doc
// comment on platformTryLock for why flock specifically, rather than a lock
// recorded as file content, is the whole point of this design: the kernel
// releases it on ANY process death, so it cannot leak the way a data-based
// lock can.
//
// Stage 2 (opt-in, only when Config.RemoteLocking is true) is a lease
// recorded at LockRef, arbitrated by the same server/local-enforced
// compare-and-swap every other ref in this package relies on.

// LockFileName is the local flock(2) target, joined directly onto the git
// COMMON directory by Acquire - i.e. "<commonDir>/meads.lock", NOT nested
// under PushStateDir ("<commonDir>/meads/", gitpush.go's local push-cadence
// state). The two are deliberately separate files under the same common
// dir: one is a lock, the other a timestamp, and neither should be mistaken
// for the other.
const LockFileName = "meads.lock"

// LockRef is the ref CAS-created to represent the optional remote lock
// (Config.RemoteLocking, gitconfig.go). Unlike TaskRef/ConfigRef, it points
// DIRECTLY at a blob - no commit, no tree, no history - matching task 57's
// storage model table ("refs/meads/lock → blob"): a lock's only ever-useful
// state is its current holder and expiry, so there is nothing versioning
// would buy here that CAS itself doesn't already provide.
const LockRef = "refs/meads/lock"

// ErrLockHeld is returned by Acquire when either stage is currently held by
// someone else: the local flock by another process, or the remote lease by
// a holder whose lease has not yet expired. Always wrapped with context
// (which stage; for the remote stage, by whom and until when) rather than
// returned bare.
var ErrLockHeld = errors.New("lock held")

// lockPayload is LockRef's blob content: `{"holder": "...", "expires":
// "..."}` per task 64. Expires is marshaled by encoding/json's standard
// time.Time support, which already produces an RFC3339 (RFC3339Nano)
// timestamp - exactly what task 64 specifies - with no custom formatting
// needed.
type lockPayload struct {
	Holder  string    `json:"holder"`
	Expires time.Time `json:"expires"`
}

// Lock represents a held two-stage lock: always the local flock, plus -
// only when Config.RemoteLocking was true at the moment Acquire ran - the
// remote lease at LockRef. Always release with Release exactly once. A Lock
// that is never released leaks the local flock until this process exits
// (the kernel cleans it up even then - see gitlock_unix.go/
// gitlock_windows.go) and leaks the remote lease until its lease expires
// and someone else steals it - neither is a graceful way to end a
// multi-step operation.
type Lock struct {
	store *GitStore
	local *localHold

	mu        sync.Mutex
	released  bool
	remoteOID OID // "" iff no remote lock was taken (RemoteLocking was off)
}

// Acquire takes git mode's two-stage lock: first the mandatory local flock
// at "<commonDir>/LockFileName", then - only if Config.RemoteLocking is
// currently true - the opt-in remote lease at LockRef, valid for lease
// before it becomes stealable (see acquireRemote).
//
// commonDir MUST be the git COMMON directory (`git rev-parse
// --git-common-dir`), never a per-worktree --git-dir - exactly like
// ShouldPush/MarkPushed (gitpush.go), and for the identical reason: every
// linked worktree of one repository shares a single common dir, and
// therefore must share this single lock too, the same way they already
// share one refs/meads/* ref store. Acquire never resolves commonDir
// itself, reusing gitpush.go's pattern of leaving that to the caller (see
// cmd/md/push.go's gitCommonDir).
//
// Ordering is fixed and always local-THEN-remote, never the reverse, so two
// callers can never deadlock ABBA-style against each other across the two
// stages. If the remote stage fails for ANY reason - another holder's
// unexpired lease, or an outright error from the ref store - the local lock
// is released before Acquire returns, so a failed call never leaks the
// local flock (see TestGitLock_Acquire_RemoteFailureReleasesLocalLock).
//
// lease bounds ONLY the remote lock: an absolute expiry after which it
// becomes stealable (see acquireRemote). The local flock has no lease and
// no expiry at all, by design - the kernel releases it the instant this
// process ends, for any reason, so nothing needs to time it out.
func (g *GitStore) Acquire(commonDir string, lease time.Duration) (*Lock, error) {
	if _, err := g.EnsureGitRefProtocolVersion(); err != nil {
		return nil, err
	}
	path := filepath.Join(commonDir, LockFileName)
	local, err := acquireLocal(path)
	if err != nil {
		return nil, fmt.Errorf("acquire local lock: %w", err)
	}

	cfg, err := g.Config()
	if err != nil {
		_ = local.release()
		return nil, fmt.Errorf("acquire lock: reading config: %w", err)
	}
	if !cfg.RemoteLocking {
		return &Lock{store: g, local: local}, nil
	}

	// Fail closed. Config.RemoteLocking=true commits every participant in
	// this repo to honoring the remote lock (see Config's doc comment in
	// gitconfig.go: it deliberately has no per-clone override precisely so
	// this promise can be relied on). If THIS acquisition cannot confirm it
	// actually holds that remote lock too - for any reason, including the
	// backing ref store being unreachable or broken - silently proceeding
	// with only the local flock would be wrong in the worst possible way:
	// invisibly. Every other agent still believes the remote lock is the
	// arbiter, so this caller would run its multi-step critical section
	// fully unserialized against them with no signal anything was amiss.
	// An explicit error here is always preferable to a mutual-exclusion
	// guarantee that quietly stopped holding.
	remoteOID, err := g.acquireRemote(lockHolderID(), lease)
	if err != nil {
		_ = local.release()
		return nil, fmt.Errorf("acquire remote lock: %w", err)
	}
	return &Lock{store: g, local: local, remoteOID: remoteOID}, nil
}

// Release releases l, in the reverse order Acquire took its stages: the
// remote lease first (if one was taken), then the local flock - see
// Acquire's doc comment on ordering. Calling Release more than once on the
// same Lock returns an error rather than double-releasing.
func (l *Lock) Release() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.released {
		return errors.New("lock already released")
	}
	l.released = true

	var remoteErr error
	if l.remoteOID != "" {
		remoteErr = l.store.releaseRemote(l.remoteOID)
	}
	localErr := l.local.release()

	switch {
	case remoteErr != nil:
		return fmt.Errorf("release remote lock: %w", remoteErr)
	case localErr != nil:
		return fmt.Errorf("release local lock: %w", localErr)
	default:
		return nil
	}
}

// lockHolderID identifies this process for a remote lock's "holder" field:
// hostname+pid, per task 64 - enough to be useful in a "who's holding this"
// message without threading a more specific identity (e.g. a Claude session
// id) all the way through Acquire. A caller that wants a more specific
// holder string can build on this later; task 64 is deliberately just the
// primitive (see this file's opening comment).
func lockHolderID() string {
	host, err := os.Hostname()
	if err != nil || host == "" {
		host = "unknown-host"
	}
	return fmt.Sprintf("%s:%d", host, os.Getpid())
}

// --- stage 2: remote lock (refs/meads/lock) ---

// readRemoteLock reads LockRef's current payload and oid. exists is false
// (with a zero payload and ZeroOID) if the ref does not exist yet - a
// legitimate starting point for acquireRemote's create-only CAS, not an
// error - mirroring readConfigRaw's absent-ref handling (gitconfig.go).
func (g *GitStore) readRemoteLock() (payload lockPayload, oid OID, exists bool, err error) {
	oid, err = g.refs.ResolveRef(LockRef)
	if err != nil {
		if errors.Is(err, ErrRefNotFound) {
			return lockPayload{}, ZeroOID, false, nil
		}
		return lockPayload{}, "", false, err
	}
	data, err := g.refs.ReadBlob(oid)
	if err != nil {
		return lockPayload{}, "", false, fmt.Errorf("reading %s at %s: %w", LockRef, oid, err)
	}
	var p lockPayload
	if err := json.Unmarshal(data, &p); err != nil {
		return lockPayload{}, "", false, fmt.Errorf("parsing %s blob %s: %w", LockRef, oid, err)
	}
	return p, oid, true, nil
}

// acquireRemote CAS-creates LockRef: winning the create-if-absent CAS
// against ZeroOID IS winning the lock, exactly like a task ref's Create
// (gitmutate.go). An existing, UNEXPIRED lock fails immediately with
// ErrLockHeld - acquireRemote does not block or poll waiting for a natural
// release, matching the local stage's non-blocking philosophy (see
// gitlock_unix.go). An existing but EXPIRED lock is stolen: retried as a
// CAS from the stale oid to a freshly-written payload, so of any number of
// simultaneous stealers exactly one's CAS lands (git's CAS is the sole
// arbiter - see refstore.go's CompareAndSwap) and the rest observe
// ErrCASConflict and loop back to re-read, bounded by maxCASRetries exactly
// like every other mutating GitStore method (gitmutate.go).
//
// "Lock is held" is decided ENTIRELY by reading the payload's own Expires
// field on a fresh read every attempt - NEVER by inspecting a CAS
// rejection's text, which refstore.go's CompareAndSwap doc comment already
// establishes isn't stable across git versions/hosts.
func (g *GitStore) acquireRemote(holder string, lease time.Duration) (OID, error) {
	var lastErr error
	for attempt := 0; attempt < maxCASRetries; attempt++ {
		payload, curOID, exists, err := g.readRemoteLock()
		if err != nil {
			return "", err
		}

		var prev OID = ZeroOID
		if exists {
			if time.Now().Before(payload.Expires) {
				return "", fmt.Errorf("remote lock held by %s until %s: %w",
					payload.Holder, payload.Expires.Format(time.RFC3339), ErrLockHeld)
			}
			prev = curOID // expired: steal via CAS from the stale oid to our own
		}

		data, err := json.Marshal(lockPayload{Holder: holder, Expires: time.Now().Add(lease)})
		if err != nil {
			return "", fmt.Errorf("marshaling lock payload: %w", err)
		}
		blob, err := g.refs.WriteBlob(data)
		if err != nil {
			return "", err
		}
		if err := g.refs.CompareAndSwap(LockRef, blob, prev); err != nil {
			if !errors.Is(err, ErrCASConflict) {
				return "", err
			}
			lastErr = err // lost the race (create or steal): loop and re-read
			continue
		}
		return blob, nil
	}
	return "", fmt.Errorf("acquire remote lock: exhausted %d attempts: %w", maxCASRetries, lastErr)
}

// releaseRemote CAS-deletes LockRef against oid - the exact oid THIS holder
// received from acquireRemote - which is what makes releasing someone
// else's lock structurally impossible: the delete only succeeds if oid is
// still LockRef's CURRENT value, and only the process that most recently
// won acquireRemote's create-or-steal CAS can possess that oid. If oid is
// stale (e.g. this lease itself already expired and someone else stole it
// first), CompareAndDelete fails with ErrCASConflict - correctly, since
// this caller no longer actually holds the lock it thinks it does.
func (g *GitStore) releaseRemote(oid OID) error {
	return g.refs.CompareAndDelete(LockRef, oid)
}

// --- stage 1: local lock (flock/LockFileEx on commonDir/LockFileName) ---

// localLock is the process-WIDE handle for one lock file path: at most one
// real, kernel-level, non-blocking lock per (process, path), shared by
// every in-process acquirer of that same path via refCount - see
// acquireLocal's doc comment for why sharing, rather than a second real
// lock attempt, is required.
type localLock struct {
	f        *os.File
	refCount int
}

// localLocksMu guards localLocks. A single global mutex, rather than one
// per path, is deliberate: this lock is for rare multi-step maintenance
// operations (see this file's opening comment), never a hot path, so there
// is no meaningful contention cost to serializing the small bookkeeping
// step of "is this path already held in-process" across every path at
// once - and a single mutex is a much smaller surface to get right than a
// map of per-path mutexes guarding themselves.
var (
	localLocksMu sync.Mutex
	localLocks   = map[string]*localLock{}
)

// localHold is one caller's claim on path's localLock; Lock wraps exactly
// one of these per Acquire call.
type localHold struct {
	path string
}

// acquireLocal takes the process-wide lock on path, guarding against the
// self-deadlock a real flock(2)/LockFileEx call would otherwise risk within
// a single process: on unix, flock(2) treats every open file descriptor as
// an independent lock holder EVEN WITHIN ONE PROCESS - a second flock(2)
// call on a second fd for the same path, from the very same process that
// already holds the first, is denied (or blocks) exactly as if it came from
// a different process entirely (see flock(2)'s manual: "If a process uses
// open(2) ... to obtain more than one file descriptor for the same file,
// these file descriptors are treated independently"). A naive
// implementation that opened+locked a fresh fd on every Acquire call would
// therefore make ANY nested or repeated in-process acquisition of the same
// path self-contend or deadlock - the exact bug task 64 calls out.
//
// The fix: a process-global mutex (localLocksMu) plus a refcounted map
// (localLocks), keyed by ABSOLUTE path (path is resolved via filepath.Abs
// up front - callers are documented to already pass an absolute commonDir,
// exactly like ShouldPush/MarkPushed's contract in gitpush.go, but the
// guard below only works if two different spellings of the same file are
// never treated as two different map keys, so this normalizes defensively
// rather than trusting that contract). The FIRST in-process acquirer of a
// given path performs the one real, kernel-level, non-blocking lock
// (platformTryLock) and keeps that single fd/handle open for as long as
// ANY in-process holder still needs it. Every SUBSEQUENT in-process
// acquirer of the SAME path - nested or concurrent, same goroutine or not -
// observes that hold already exists and simply increments the refcount,
// never issuing a second platformTryLock call that could contend with
// itself. Cross-process contention is completely untouched by any of this:
// a different process's platformTryLock on the same path still fails
// exactly as the kernel defines, which is the real guarantee this whole
// lock exists to provide (see TestGitLock_LocalLock_SecondAcquirerFailsWhileFirstHolds
// and TestGitLock_LocalLock_ReleasedAfterHolderSIGKilled, both of which
// therefore use a genuinely separate OS process rather than a second
// same-process call).
func acquireLocal(rawPath string) (*localHold, error) {
	path, err := filepath.Abs(rawPath)
	if err != nil {
		return nil, fmt.Errorf("resolving absolute path for %s: %w", rawPath, err)
	}

	localLocksMu.Lock()
	defer localLocksMu.Unlock()

	if ll, ok := localLocks[path]; ok {
		ll.refCount++
		return &localHold{path: path}, nil
	}

	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return nil, fmt.Errorf("creating %s: %w", filepath.Dir(path), err)
	}
	// O_CREATE|O_RDWR, never O_TRUNC: the lock file's content is never used
	// for anything (unlike the file-mode Store.acquireLock in lock.go,
	// whose lock IS the file's content) and NEVER removed on release either
	// - see localHold.release - so there is nothing to preserve or reset
	// here, only an inode for flock/LockFileEx to attach to.
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		return nil, fmt.Errorf("opening %s: %w", path, err)
	}
	ok, err := platformTryLock(f)
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	if !ok {
		_ = f.Close()
		return nil, fmt.Errorf("%s: %w", path, ErrLockHeld)
	}
	localLocks[path] = &localLock{f: f, refCount: 1}
	return &localHold{path: path}, nil
}

// release drops one in-process claim on h's path, actually unlocking and
// closing the underlying file only once the LAST in-process holder of that
// path releases (refCount reaches zero) - see acquireLocal's doc comment.
// The lock file itself is never removed: deleting it on release would let a
// brand-new file (a different inode) reappear at the same path while this
// process might still - briefly - hold a lock on the OLD, now-unlinked
// inode, which is exactly the kind of TOCTOU hazard flock was chosen to
// avoid in the first place (see gitlock_unix.go's doc comment).
func (h *localHold) release() error {
	localLocksMu.Lock()
	defer localLocksMu.Unlock()

	ll, ok := localLocks[h.path]
	if !ok {
		return fmt.Errorf("local lock %s: already released", h.path)
	}
	ll.refCount--
	if ll.refCount > 0 {
		return nil
	}
	delete(localLocks, h.path)
	unlockErr := platformUnlock(ll.f)
	closeErr := ll.f.Close()
	if unlockErr != nil {
		return unlockErr
	}
	return closeErr
}
