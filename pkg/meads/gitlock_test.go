package meads

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// Tests for git mode's two-stage, opt-in lock (task 57 "Locking (two-stage,
// opt-in)"; built as TASKS #64): GitStore.Acquire/Lock.Release, implemented
// in gitlock.go/gitlock_unix.go/gitlock_windows.go. Like gitstore_test.go
// and friends, these run against real temporary git repositories via
// ExecGit rather than a fake, since what's under test - real flock(2)
// semantics, real cross-process contention, and real git-ref CAS - is
// precisely what a fake would rubber-stamp without exercising.
//
// Acquire's in-process refcount guard (self-deadlock avoidance - see
// gitlock.go's doc comment on acquireLocal) deliberately makes a SECOND
// same-process Acquire on the same path succeed rather than contend, so a
// few of these tests (marked below) need a genuinely separate OS process to
// prove real contention/kernel-release behaviour - see the helper-process
// plumbing at the bottom of this file.

// --- helpers ---

// commonDirOf resolves dir's git COMMON directory exactly like cmd/md's
// gitCommonDir (push.go) and gitpush_test.go's
// TestGitStore_MarkPushed_NeverCreatesARef - duplicated here rather than
// imported since pkg/meads must not depend on cmd/md, and Acquire itself
// deliberately takes commonDir as a plain parameter rather than resolving
// it (see gitlock.go's doc comment, reusing gitpush.go's pattern).
func commonDirOf(t *testing.T, dir string) string {
	t.Helper()
	commonDir := runGit(t, dir, "rev-parse", "--git-common-dir")
	if !filepath.IsAbs(commonDir) {
		commonDir = filepath.Join(dir, commonDir)
	}
	return commonDir
}

// --- 1. happy path: local-only round trip ---

func TestGitLock_Acquire_Release_HappyPath_LocalOnly(t *testing.T) {
	gs, _, dir := newGitStoreRepo(t)
	commonDir := commonDirOf(t, dir)

	lock, err := gs.Acquire(commonDir, time.Minute)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if _, err := os.Stat(filepath.Join(commonDir, LockFileName)); err != nil {
		t.Errorf("lock file missing at %s after Acquire: %v", filepath.Join(commonDir, LockFileName), err)
	}
	if err := lock.Release(); err != nil {
		t.Fatalf("Release: %v", err)
	}

	// A completed Release must actually free the path: a fresh Acquire
	// right after must succeed rather than observing a leaked hold.
	lock2, err := gs.Acquire(commonDir, time.Minute)
	if err != nil {
		t.Fatalf("Acquire after Release: %v, want success (Release must actually free the local lock)", err)
	}
	if err := lock2.Release(); err != nil {
		t.Fatalf("second Release: %v", err)
	}
}

func TestGitLock_Release_CalledTwice_ReturnsError(t *testing.T) {
	gs, _, dir := newGitStoreRepo(t)
	commonDir := commonDirOf(t, dir)

	lock, err := gs.Acquire(commonDir, time.Minute)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if err := lock.Release(); err != nil {
		t.Fatalf("first Release: %v", err)
	}
	if err := lock.Release(); err == nil {
		t.Fatal("second Release on the same Lock succeeded, want an error (double-release must not double-decrement/double-CAS-delete)")
	}
}

// --- 2. nested acquisition within one process: no self-deadlock ---

// TestGitLock_LocalLock_NestedAcquisitionDoesNotSelfDeadlock proves the
// self-deadlock guard task 64 calls out: on unix, flock(2) treats separate
// file descriptors as independent holders EVEN WITHIN ONE PROCESS, so a
// naive "open+flock every Acquire call" implementation would make this
// nested call block/fail against itself. Run in a goroutine with a bounded
// timeout so a regression fails this test cleanly instead of hanging the
// whole suite.
func TestGitLock_LocalLock_NestedAcquisitionDoesNotSelfDeadlock(t *testing.T) {
	gs, _, dir := newGitStoreRepo(t)
	commonDir := commonDirOf(t, dir)

	done := make(chan error, 1)
	go func() {
		outer, err := gs.Acquire(commonDir, time.Minute)
		if err != nil {
			done <- fmt.Errorf("outer Acquire: %w", err)
			return
		}
		inner, err := gs.Acquire(commonDir, time.Minute) // nested, same process
		if err != nil {
			done <- fmt.Errorf("nested Acquire: %w", err)
			return
		}
		if err := inner.Release(); err != nil {
			done <- fmt.Errorf("inner Release: %w", err)
			return
		}
		done <- outer.Release()
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("nested acquisition within one process: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("nested Acquire within one process deadlocked")
	}

	// Both holds are now released: the path must be fully free.
	lock, err := gs.Acquire(commonDir, time.Minute)
	if err != nil {
		t.Fatalf("Acquire after both nested holds released: %v, want success", err)
	}
	if err := lock.Release(); err != nil {
		t.Fatalf("Release: %v", err)
	}
}

// --- 3 & 4 & 5: cross-process tests (real contention / real kernel release) ---
//
// See the helper-process plumbing below for why these specifically need a
// genuinely separate OS process rather than a second same-process Acquire
// call.

// TestGitLock_LocalLock_SecondAcquirerFailsWhileFirstHolds proves real,
// cross-process mutual exclusion: while this process holds the lock, a
// SEPARATE process trying to acquire the identical path must fail (not
// silently succeed the way a second same-process call would, by design -
// see acquireLocal's doc comment).
func TestGitLock_LocalLock_SecondAcquirerFailsWhileFirstHolds(t *testing.T) {
	if testing.Short() {
		t.Skip("spawns a subprocess")
	}
	gs, _, dir := newGitStoreRepo(t)
	commonDir := commonDirOf(t, dir)

	lock, err := gs.Acquire(commonDir, time.Minute)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	defer lock.Release()

	out, err := runLockHelperOnce(t, dir, "tryonce")
	if err == nil {
		t.Fatalf("a second, separate process acquired the lock while the first still held it; helper output:\n%s", out)
	}
	if !strings.Contains(out, "FAILED") {
		t.Fatalf("helper output = %q, want a FAILED line (contention should be a clean, reported failure)", out)
	}
}

// TestGitLock_LocalLock_LinkedWorktreesContendOnSameLock proves linked
// worktrees share the SAME lock resource, not two independent ones that
// happen to live under differently-named directories: a lock held via the
// MAIN worktree's commonDir must contend against an acquisition attempted
// via the LINKED worktree's commonDir, from a separate process.
func TestGitLock_LocalLock_LinkedWorktreesContendOnSameLock(t *testing.T) {
	if testing.Short() {
		t.Skip("spawns a subprocess")
	}
	gs, _, dir := newGitStoreRepo(t)
	runGit(t, dir, "commit", "--allow-empty", "-m", "seed")
	worktreeDir := filepath.Join(t.TempDir(), "wt")
	runGit(t, dir, "worktree", "add", "-b", "wt-branch", worktreeDir)

	commonDirMain := commonDirOf(t, dir)
	commonDirWt := commonDirOf(t, worktreeDir)
	if commonDirMain != commonDirWt {
		t.Fatalf("common dirs differ: main=%s linked=%s, want identical (one ref store -> one lock)", commonDirMain, commonDirWt)
	}

	lock, err := gs.Acquire(commonDirMain, time.Minute)
	if err != nil {
		t.Fatalf("Acquire (main worktree): %v", err)
	}
	defer lock.Release()

	// A separate process, pointed at the LINKED worktree, must fail to
	// acquire - proving the two worktrees contend on the exact same lock.
	out, err := runLockHelperOnce(t, worktreeDir, "tryonce")
	if err == nil {
		t.Fatalf("helper acquired the lock via the linked worktree while the main worktree held it; output:\n%s", out)
	}
	if !strings.Contains(out, "FAILED") {
		t.Fatalf("helper output = %q, want a FAILED line", out)
	}
}

// TestGitLock_LocalLock_ReleasedAfterHolderSIGKilled proves the entire
// reason task 64 requires flock(2) rather than a data-recorded lock (see
// gitlock_unix.go's doc comment on platformTryLock): the kernel releases
// the lock when the holding process dies, for ANY reason, including
// SIGKILL - which cannot be caught, so nothing in meads' own code ever
// gets a chance to clean up. Proven with a REAL child process killed with a
// REAL SIGKILL, not a simulation: only the kernel's own behaviour can prove
// this property.
func TestGitLock_LocalLock_ReleasedAfterHolderSIGKilled(t *testing.T) {
	if testing.Short() {
		t.Skip("spawns a subprocess")
	}
	gs, _, dir := newGitStoreRepo(t)
	commonDir := commonDirOf(t, dir)

	cmd := exec.Command(os.Args[0], "-test.run=^TestGitLock_HelperProcess$")
	cmd.Env = append(os.Environ(), lockHelperModeEnv+"=hold", lockHelperDirEnv+"="+dir)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("StdoutPipe: %v", err)
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("start helper: %v", err)
	}

	line, err := readHelperLine(stdout, 10*time.Second)
	if err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		t.Fatalf("waiting for helper to confirm it holds the lock: %v", err)
	}
	if line != "LOCKED" {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		t.Fatalf("helper's first output line = %q, want LOCKED", line)
	}

	// Prove contention WHILE the helper is alive and holding the lock,
	// before killing it - otherwise a bug that made Acquire always succeed
	// would go unnoticed and this test would "pass" for the wrong reason.
	if badLock, err := gs.Acquire(commonDir, time.Minute); err == nil {
		_ = badLock.Release()
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		t.Fatal("Acquire succeeded while the helper process was still alive and holding the lock")
	}

	if err := cmd.Process.Kill(); err != nil { // SIGKILL
		t.Fatalf("killing helper: %v", err)
	}
	_ = cmd.Wait() // reap; a killed process reports a non-nil Wait error, which is expected and ignored here

	// The kernel must have released the flock the instant the process
	// died - prove it by acquiring for real, from THIS process, against
	// the exact same commonDir.
	lock, err := gs.Acquire(commonDir, time.Minute)
	if err != nil {
		t.Fatalf("Acquire after SIGKILLing the holder: %v, want success (the kernel must release flock on process death)", err)
	}
	if err := lock.Release(); err != nil {
		t.Fatalf("Release: %v", err)
	}
}

// --- 6. remote lock off by default ---

func TestGitLock_RemoteLock_OffByDefault_NoRefCreated(t *testing.T) {
	gs, rs, dir := newGitStoreRepo(t)
	commonDir := commonDirOf(t, dir)

	lock, err := gs.Acquire(commonDir, time.Minute)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	defer lock.Release()

	if _, err := rs.ResolveRef(LockRef); !errors.Is(err, ErrRefNotFound) {
		t.Fatalf("ResolveRef(%s) after Acquire with RemoteLocking unset (default off) = err %v, want ErrRefNotFound (no remote ref should exist)", LockRef, err)
	}
}

// --- 7. remoteLocking on: acquire creates the ref; release deletes it ---

func TestGitLock_RemoteLock_OnCreatesRefAndReleaseDeletesIt(t *testing.T) {
	gs, rs, dir := newGitStoreRepo(t)
	commonDir := commonDirOf(t, dir)
	if err := gs.SetConfig(Config{RemoteLocking: true}); err != nil {
		t.Fatalf("SetConfig: %v", err)
	}

	lock, err := gs.Acquire(commonDir, time.Minute)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if _, err := rs.ResolveRef(LockRef); err != nil {
		t.Fatalf("ResolveRef(%s) after Acquire with RemoteLocking=true: %v, want the ref to exist", LockRef, err)
	}

	if err := lock.Release(); err != nil {
		t.Fatalf("Release: %v", err)
	}
	if _, err := rs.ResolveRef(LockRef); !errors.Is(err, ErrRefNotFound) {
		t.Fatalf("ResolveRef(%s) after Release = err %v, want ErrRefNotFound (release must delete the ref)", LockRef, err)
	}
}

// --- 8. releasing someone else's remote lock is impossible ---

func TestGitLock_RemoteLock_CannotReleaseSomeoneElsesLock(t *testing.T) {
	gs, rs, _ := newGitStoreRepo(t)

	// A legitimate holder's currently-recorded oid.
	real := lockPayload{Holder: "holder-A", Expires: time.Now().Add(time.Hour)}
	data, err := json.Marshal(real)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	realOID, err := rs.WriteBlob(data)
	if err != nil {
		t.Fatalf("WriteBlob: %v", err)
	}
	if err := rs.CompareAndSwap(LockRef, realOID, ZeroOID); err != nil {
		t.Fatalf("seeding %s: %v", LockRef, err)
	}

	// A different oid - what a buggy or malicious caller trying to release
	// a lock it never actually won would have to guess.
	fakeOID, err := rs.WriteBlob([]byte(`{"holder":"attacker","expires":"2099-01-01T00:00:00Z"}`))
	if err != nil {
		t.Fatalf("WriteBlob (fake): %v", err)
	}

	err = gs.releaseRemote(fakeOID)
	if !errors.Is(err, ErrCASConflict) {
		t.Fatalf("releaseRemote with the wrong oid: err = %v, want errors.Is(_, ErrCASConflict)", err)
	}

	got, err := rs.ResolveRef(LockRef)
	if err != nil {
		t.Fatalf("ResolveRef after rejected release: %v", err)
	}
	if got != realOID {
		t.Fatalf("lock ref changed despite a rejected release: got %s, want unchanged %s", got, realOID)
	}
}

// --- 9. expired lease is stealable; N concurrent stealers -> exactly one wins ---

func TestGitLock_RemoteLock_ExpiredLease_ConcurrentStealers_ExactlyOneWins(t *testing.T) {
	gs, rs, _ := newGitStoreRepo(t)

	stale := lockPayload{Holder: "dead-holder", Expires: time.Now().Add(-time.Hour)}
	data, err := json.Marshal(stale)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	staleOID, err := rs.WriteBlob(data)
	if err != nil {
		t.Fatalf("WriteBlob: %v", err)
	}
	if err := rs.CompareAndSwap(LockRef, staleOID, ZeroOID); err != nil {
		t.Fatalf("seeding stale lock: %v", err)
	}

	const n = 10
	start := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(n)
	results := make([]error, n)
	oids := make([]OID, n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			<-start
			oid, err := gs.acquireRemote(fmt.Sprintf("stealer-%d", i), time.Minute)
			results[i] = err
			oids[i] = oid
		}(i)
	}
	close(start)
	wg.Wait()

	var wins int
	var winnerOID OID
	for i, err := range results {
		if err == nil {
			wins++
			winnerOID = oids[i]
		}
	}
	if wins != 1 {
		t.Fatalf("concurrent stealers against one expired lease: %d succeeded, want exactly 1 (results=%v)", wins, results)
	}

	finalOID, err := rs.ResolveRef(LockRef)
	if err != nil {
		t.Fatalf("ResolveRef after stealing: %v", err)
	}
	if finalOID == staleOID {
		t.Fatal("lock ref still points at the stale payload after a successful steal")
	}
	if finalOID != winnerOID {
		t.Fatalf("lock ref = %s, want it to match the one successful stealer's oid %s", finalOID, winnerOID)
	}
}

// --- 10. unexpired lease is NOT stealable ---

func TestGitLock_RemoteLock_UnexpiredLease_NotStealable(t *testing.T) {
	gs, rs, _ := newGitStoreRepo(t)

	active := lockPayload{Holder: "current-holder", Expires: time.Now().Add(time.Hour)}
	data, err := json.Marshal(active)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	activeOID, err := rs.WriteBlob(data)
	if err != nil {
		t.Fatalf("WriteBlob: %v", err)
	}
	if err := rs.CompareAndSwap(LockRef, activeOID, ZeroOID); err != nil {
		t.Fatalf("seeding active lock: %v", err)
	}

	_, err = gs.acquireRemote("would-be-thief", time.Minute)
	if err == nil {
		t.Fatal("acquireRemote succeeded against an unexpired lease, want failure")
	}
	if !errors.Is(err, ErrLockHeld) {
		t.Errorf("err = %v, want errors.Is(_, ErrLockHeld)", err)
	}

	got, err := rs.ResolveRef(LockRef)
	if err != nil {
		t.Fatalf("ResolveRef: %v", err)
	}
	if got != activeOID {
		t.Fatalf("lock ref changed despite an unexpired, non-stealable lease: got %s, want unchanged %s", got, activeOID)
	}
}

// --- 11. remote acquisition failure releases the local lock ---

func TestGitLock_Acquire_RemoteFailureReleasesLocalLock(t *testing.T) {
	gs, rs, dir := newGitStoreRepo(t)
	commonDir := commonDirOf(t, dir)

	// Seed an unexpired remote lock held by someone else, so the remote
	// stage is guaranteed to fail deterministically - no retries or timing
	// involved, see acquireRemote's non-stealable-unexpired-lease path.
	active := lockPayload{Holder: "someone-else", Expires: time.Now().Add(time.Hour)}
	data, err := json.Marshal(active)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	activeOID, err := rs.WriteBlob(data)
	if err != nil {
		t.Fatalf("WriteBlob: %v", err)
	}
	if err := rs.CompareAndSwap(LockRef, activeOID, ZeroOID); err != nil {
		t.Fatalf("seeding active lock: %v", err)
	}
	if err := gs.SetConfig(Config{RemoteLocking: true}); err != nil {
		t.Fatalf("SetConfig: %v", err)
	}

	if lock, err := gs.Acquire(commonDir, time.Minute); err == nil {
		_ = lock.Release()
		t.Fatal("Acquire succeeded despite an unexpired remote lock held by someone else")
	}

	// Prove the LOCAL lock did not leak: disable remote locking (so this
	// second attempt only needs the local stage) and confirm it succeeds
	// immediately - if the first, failed attempt had leaked the local
	// flock, this would fail too.
	if err := gs.SetConfig(Config{RemoteLocking: false}); err != nil {
		t.Fatalf("SetConfig: %v", err)
	}
	lock, err := gs.Acquire(commonDir, time.Minute)
	if err != nil {
		t.Fatalf("Acquire after a failed remote acquisition: %v, want success (the local lock must have been released, not leaked)", err)
	}
	if err := lock.Release(); err != nil {
		t.Fatalf("Release: %v", err)
	}
}

// --- 12. fail closed: unusable remote -> error, not silent success ---

// brokenLockGit wraps a real Git but fails every operation whose arguments
// mention LockRef, simulating an unreachable/broken backing store for the
// remote lock stage specifically - without needing an actual network. Every
// other ref (tasks, config) passes through untouched.
type brokenLockGit struct {
	Git
}

func (b *brokenLockGit) Output(args ...string) (string, error) {
	if containsLockRef(args) {
		return "", errors.New("simulated: remote unreachable")
	}
	return b.Git.Output(args...)
}

func (b *brokenLockGit) OutputWithInput(stdin string, args ...string) (string, error) {
	if containsLockRef(args) {
		return "", errors.New("simulated: remote unreachable")
	}
	return b.Git.OutputWithInput(stdin, args...)
}

func (b *brokenLockGit) OutputRaw(args ...string) ([]byte, error) {
	if containsLockRef(args) {
		return nil, errors.New("simulated: remote unreachable")
	}
	return b.Git.OutputRaw(args...)
}

func containsLockRef(args []string) bool {
	for _, a := range args {
		if strings.Contains(a, LockRef) {
			return true
		}
	}
	return false
}

// TestGitLock_Acquire_FailClosed_UnusableRemoteErrorsNotSilentSuccess proves
// task 64's fail-closed requirement: RemoteLocking=true commits every
// participant to honoring the remote lock, so if the ref-store operation
// the remote stage depends on is broken/unreachable, Acquire must return an
// error - never silently fall back to "well, the local flock will do".
// Acquire's remote stage is pure local git-ref CAS (task 57's storage
// model: refs/meads/lock lives in the SAME local object/ref store as
// refs/meads/tasks/* and refs/meads/config, synced to an actual remote only
// by the separate push/fetch machinery of phases 5/6 - see gitpush.go) - so
// "the remote is unreachable" is faithfully modeled here as "the ref-store
// operation LockRef depends on fails for a reason that isn't a CAS
// conflict", via brokenLockGit, rather than a real network failure this
// architecture never actually depends on synchronously.
func TestGitLock_Acquire_FailClosed_UnusableRemoteErrorsNotSilentSuccess(t *testing.T) {
	_, _, dir := newGitStoreRepo(t)
	commonDir := commonDirOf(t, dir)

	real := &ExecGit{Dir: dir}
	working := NewGitStore(real)
	if err := working.SetConfig(Config{RemoteLocking: true}); err != nil {
		t.Fatalf("SetConfig: %v", err)
	}

	broken := NewGitStore(&brokenLockGit{Git: real})
	lock, err := broken.Acquire(commonDir, time.Minute)
	if err == nil {
		_ = lock.Release()
		t.Fatal("Acquire succeeded despite a broken remote ref store with RemoteLocking=true; want a hard failure (fail closed), not a silent local-only success")
	}
	if errors.Is(err, ErrLockHeld) {
		t.Errorf("err = %v, want a plain ref-store failure, not ErrLockHeld (this must be classified as broken, not contended)", err)
	}
}

// --- helper process plumbing for cross-process lock tests ---
//
// Acquire's in-process refcount guard (self-deadlock avoidance, see
// gitlock.go's doc comment on acquireLocal) deliberately makes a SECOND
// same-process Acquire on a given path succeed rather than contend - so
// proving real cross-process contention, and proving the KERNEL (not any
// bookkeeping of meads' own) releases the lock on a killed holder, both
// need a genuinely separate OS process. Standard Go technique: re-exec the
// already-built test binary (os.Args[0]) with -test.run pinned to exactly
// TestGitLock_HelperProcess and an env var carrying its instructions,
// mirroring net/http's and os/exec's own TestHelperProcess convention.

const (
	// lockHelperModeEnv is "hold" or "tryonce"; unset means "not a
	// re-exec'd helper invocation", the ordinary `go test` case.
	lockHelperModeEnv = "MEADS_GITLOCK_HELPER_MODE"
	// lockHelperDirEnv is the git repo/worktree directory the helper
	// should operate in - its commonDir is re-derived from this inside the
	// helper (via commonDirOf) exactly the way the parent derives its own,
	// so a linked worktree's helper naturally resolves the SAME commonDir
	// as its main worktree without needing to pass it separately.
	lockHelperDirEnv = "MEADS_GITLOCK_HELPER_DIR"
)

// TestGitLock_HelperProcess is a no-op under a normal `go test` run
// (lockHelperModeEnv unset). Re-exec'd with that env var set, it becomes
// the child process body for the cross-process tests above: it acquires
// its commonDir's lock via the exact same GitStore.Acquire path production
// code uses, prints "LOCKED" to stdout once it succeeds (or "FAILED: ..."
// to stderr and exits 1 if it doesn't - e.g. on contention), and then
// either exits immediately ("tryonce") or blocks ("hold", for the SIGKILL
// test) - bounded by a generous sleep as a safety net in case a test fails
// to kill it, so a broken test can't leave a process running forever.
func TestGitLock_HelperProcess(t *testing.T) {
	mode := os.Getenv(lockHelperModeEnv)
	if mode == "" {
		return
	}
	dir := os.Getenv(lockHelperDirEnv)
	gs := NewGitStore(&ExecGit{Dir: dir})
	commonDir := commonDirOf(t, dir)

	lock, err := gs.Acquire(commonDir, time.Minute)
	if err != nil {
		fmt.Fprintf(os.Stderr, "FAILED: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("LOCKED")
	switch mode {
	case "hold":
		time.Sleep(10 * time.Second) // safety net only; the parent kills us long before this
		_ = lock.Release()
	case "tryonce":
		_ = lock.Release()
	default:
		fmt.Fprintf(os.Stderr, "unknown helper mode %q\n", mode)
		os.Exit(2)
	}
}

// runLockHelperOnce re-execs the test binary as TestGitLock_HelperProcess
// in the given mode, pointed at dir, waits for it to exit, and returns its
// combined output and exit error. Used by the "tryonce" contention tests,
// where the helper is expected to run to completion quickly either way.
func runLockHelperOnce(t *testing.T, dir, mode string) (string, error) {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestGitLock_HelperProcess$")
	cmd.Env = append(os.Environ(), lockHelperModeEnv+"="+mode, lockHelperDirEnv+"="+dir)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// readHelperLine reads one line from r (a helper process's stdout),
// bounded by timeout - mirrors cmd/md/webui_test.go's readStartLine, needed
// here too since the SIGKILL test must not act (by killing the process)
// until it has genuine confirmation the helper actually holds the lock, not
// just "enough time probably passed".
func readHelperLine(r io.Reader, timeout time.Duration) (string, error) {
	done := make(chan struct{})
	var line string
	var err error
	go func() {
		defer close(done)
		s := bufio.NewScanner(r)
		if s.Scan() {
			line = strings.TrimSpace(s.Text())
			return
		}
		err = s.Err()
		if err == nil {
			err = io.EOF
		}
	}()
	select {
	case <-done:
		return line, err
	case <-time.After(timeout):
		return "", fmt.Errorf("timed out after %s waiting for helper output", timeout)
	}
}
