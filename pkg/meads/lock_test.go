package meads

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-git/go-billy/v5/memfs"
	"github.com/go-git/go-billy/v5/util"
)

func newTestStore(t *testing.T, content string) *Store {
	t.Helper()
	fs := memfs.New()
	if content != "" {
		if err := util.WriteFile(fs, "TASKS.md", []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}
	return NewStore(fs, "TASKS.md")
}

// newOSTestStore is newTestStore on a real filesystem, for the tests that run
// writers concurrently - see TestConcurrentWriters_OneWins for why memfs
// cannot be used there.
func newOSTestStore(t *testing.T, content string) *Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "TASKS.md")
	if content != "" {
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}
	return NewFileStore(path)
}

func TestAcquireLock_Basic(t *testing.T) {
	s := newTestStore(t, "# Tasks\n")
	id, content, err := s.acquireLock()
	if err != nil {
		t.Fatalf("acquireLock failed: %v", err)
	}
	if id == "" {
		t.Fatal("expected non-empty lock id")
	}
	if !strings.HasPrefix(content, "# Tasks\n") {
		t.Errorf("content = %q, want prefix %q", content, "# Tasks\n")
	}
	if strings.Contains(content, "lock:") {
		t.Errorf("content should not contain lock lines: %q", content)
	}
}

func TestReleaseLock_StripsLockLines(t *testing.T) {
	s := newTestStore(t, "")
	original := "# Tasks\n\n## 1 Do stuff\n"
	withLocks := original + "\nlock:abc123:1234567890\nlock:def456:1234567890\n"
	if err := s.releaseLock(withLocks); err != nil {
		t.Fatalf("releaseLock failed: %v", err)
	}
	data, err := util.ReadFile(s.fs, s.file)
	if err != nil {
		t.Fatal(err)
	}
	got := string(data)
	if strings.Contains(got, "lock:") {
		t.Errorf("lock lines remain in file: %q", got)
	}
}

func TestStripLockLines(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"no locks", "hello\nworld", "hello\nworld"},
		{"one lock", "hello\nlock:abc:123\nworld", "hello\nworld"},
		{"multiple locks", "a\nlock:x:1\nb\nlock:y:2\nc", "a\nb\nc"},
		{"only locks", "lock:a:1\nlock:b:2", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := stripLockLines(tt.input)
			if got != tt.want {
				t.Errorf("stripLockLines(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestAcquireLock_ExpiredLockIgnored(t *testing.T) {
	expired := fmt.Sprintf("lock:oldwriter:%d", time.Now().Unix()-120)
	s := newTestStore(t, "# Tasks\n"+expired+"\n")

	id, content, err := s.acquireLock()
	if err != nil {
		t.Fatalf("should succeed past expired lock, got: %v", err)
	}
	if id == "" {
		t.Fatal("expected non-empty lock id")
	}
	if strings.Contains(content, "lock:") {
		t.Errorf("content should not contain lock lines: %q", content)
	}
}

func TestAcquireLock_ActiveLockBlocks(t *testing.T) {
	active := fmt.Sprintf("lock:otherwriter:%d", time.Now().Unix())
	s := newTestStore(t, "# Tasks\n"+active+"\n")

	_, _, err := s.tryAcquireLock()
	if err == nil {
		t.Fatal("expected lock contention error")
	}
	if !errors.Is(err, ErrLockContention) {
		t.Errorf("unexpected error: %v", err)
	}
}

// withLockBackoff shrinks the retry table so contention tests spend
// microseconds rather than the ~1.6s the real policy budgets.
func withLockBackoff(t *testing.T, delays ...time.Duration) {
	t.Helper()
	prev := lockBackoff
	lockBackoff = delays
	t.Cleanup(func() { lockBackoff = prev })
}

// TestAcquireLock_RetriesUntilReleased is the liveness fix from task 68: a
// writer that loses the append race must wait for the holder rather than exit.
func TestAcquireLock_RetriesUntilReleased(t *testing.T) {
	// The hold is short and the retry budget is 40x longer, because the delays
	// are ceilings that jitter draws down from: an earlier version held for
	// 25ms against five 20ms delays and failed ~2% of runs - a 1-in-52 CI
	// flake - whenever every draw came in short. At this ratio the same
	// coincidence needs all eight draws inside a fifth of their range, which
	// is ~1e-10. The hold also has to outlast the first attempt, which happens
	// immediately with no sleep, so it cannot be zero either.
	const hold = 5 * time.Millisecond
	withLockBackoff(t, 25*time.Millisecond, 25*time.Millisecond, 25*time.Millisecond, 25*time.Millisecond,
		25*time.Millisecond, 25*time.Millisecond, 25*time.Millisecond, 25*time.Millisecond)
	s := newOSTestStore(t, "# Tasks\n")

	held, content, err := s.tryAcquireLock()
	if err != nil {
		t.Fatalf("holder failed to acquire: %v", err)
	}
	if held == "" {
		t.Fatal("holder got an empty lock id")
	}
	// Joined via Cleanup rather than inline, so a t.Fatal below still waits
	// for this goroutine: Fatal is a Goexit, and an unjoined writer outlives
	// the test to race t.TempDir's removal.
	released := make(chan struct{})
	t.Cleanup(func() { <-released })
	go func() {
		defer close(released)
		time.Sleep(hold)
		if err := s.releaseLock(content + "\n## 1. Held\n"); err != nil {
			t.Error(err)
		}
	}()

	start := time.Now()
	id, got, err := s.acquireLock()
	if err != nil {
		t.Fatalf("acquireLock should have waited out the holder, got: %v", err)
	}
	// It cannot have won before the holder released, so it must have gone
	// round the loop at least once. Without any retry this returns in
	// microseconds with a contention error instead.
	if elapsed := time.Since(start); elapsed < hold {
		t.Errorf("acquireLock returned in %s, before the %s hold elapsed", elapsed, hold)
	}
	if id == held {
		t.Error("second acquire reused the holder's lock id")
	}
	// It must see what the holder committed, not a pre-release snapshot.
	if !strings.Contains(got, "## 1. Held") {
		t.Errorf("content is stale, missing the holder's write: %q", got)
	}
	if strings.Contains(got, "lock:") {
		t.Errorf("content should not contain lock lines: %q", got)
	}
}

// TestAcquireLock_GivesUpOnHeldLock covers the other side: a lock nobody ever
// releases still surfaces, rather than retrying forever.
func TestAcquireLock_GivesUpOnHeldLock(t *testing.T) {
	withLockBackoff(t, time.Millisecond, time.Millisecond)
	active := fmt.Sprintf("lock:otherwriter:%d", time.Now().Unix())
	s := newTestStore(t, "# Tasks\n"+active+"\n")

	_, _, err := s.acquireLock()
	if !errors.Is(err, ErrLockContention) {
		t.Fatalf("expected ErrLockContention after exhausting retries, got: %v", err)
	}
	if !strings.Contains(err.Error(), "3 attempts") {
		t.Errorf("error should report the attempt count, got: %v", err)
	}
}

// TestAcquireLock_DoesNotRetryRealFailures mirrors stageFile's contract: only
// a lost race is worth waiting on, an I/O error must surface immediately.
func TestAcquireLock_DoesNotRetryRealFailures(t *testing.T) {
	withLockBackoff(t, time.Second, time.Second, time.Second)
	s := newTestStore(t, "") // no file at all: OpenFile fails outright

	start := time.Now()
	_, _, err := s.acquireLock()
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected an error locking a nonexistent file")
	}
	if errors.Is(err, ErrLockContention) {
		t.Fatalf("a missing file is not contention, got: %v", err)
	}
	if elapsed > 500*time.Millisecond {
		t.Fatalf("non-contention failure retried; returned after %s", elapsed)
	}
}

// TestAtomicWrite_PreservesFileIdentity covers what a rename-based write can
// break that an in-place one cannot: it swaps the directory entry, so file
// mode and symlinks are not carried across for free.
func TestAtomicWrite_PreservesFileIdentity(t *testing.T) {
	t.Run("mode", func(t *testing.T) {
		// A masking umask, deliberately: without one this passes on a
		// developer box (umask 002) while failing in CI (umask 022), which is
		// how a chmod that never ran shipped green. See Store.chmod.
		withUmask(t, 022)
		dir := t.TempDir()
		path := filepath.Join(dir, "TASKS.md")
		if err := os.WriteFile(path, []byte("# Tasks\n"), 0664); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(path, 0664); err != nil { // the umask above would mask the create
			t.Fatal(err)
		}
		s := NewFileStore(path)
		if _, err := s.Add(Task{Title: "One", Status: "open"}); err != nil {
			t.Fatal(err)
		}
		fi, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		// A group-writable shared checkout must stay group-writable; silently
		// dropping to 0644 locks every other user out of the tasks file.
		if got := fi.Mode().Perm(); got != 0664 {
			t.Errorf("mode = %v after write, want 0664", got)
		}
	})

	t.Run("symlink", func(t *testing.T) {
		dir := t.TempDir()
		target := filepath.Join(dir, "real-tasks.md")
		link := filepath.Join(dir, "TASKS.md")
		if err := os.WriteFile(target, []byte("# Tasks\n"), 0644); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, link); err != nil {
			t.Fatal(err)
		}
		s := NewFileStore(link)
		if _, err := s.Add(Task{Title: "Through the link", Status: "open"}); err != nil {
			t.Fatal(err)
		}
		fi, err := os.Lstat(link)
		if err != nil {
			t.Fatal(err)
		}
		if fi.Mode()&os.ModeSymlink == 0 {
			t.Error("the symlink was replaced by a regular file")
		}
		// The real point: the write has to reach the target, not orphan it.
		data, err := os.ReadFile(target)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(data), "Through the link") {
			t.Errorf("symlink target was not updated:\n%s", data)
		}
	})
}

// TestEnsureFile_ConcurrentCreateKeepsTasks pins the O_EXCL fix in
// tombstone.go: Add calls ensureFile before taking the lock, so on a fresh
// repo every writer runs it at once. Stat-then-write let a late writer
// truncate a task the winner had already committed.
// The race needs one writer descheduled between its check and its create for
// as long as another writer's whole Add - unlikely per attempt, so this runs
// many trials rather than one. It is a detector, not a proof: it never fails
// on correct code, and catches the Stat-then-write version most runs.
func TestEnsureFile_ConcurrentCreateKeepsTasks(t *testing.T) {
	const trials, n = 40, 16
	for trial := range trials {
		path := filepath.Join(t.TempDir(), "TASKS.md") // deliberately absent

		var wg sync.WaitGroup
		errs := make([]error, n)
		wg.Add(n)
		for i := range n {
			go func() {
				defer wg.Done()
				// A store each, as separate processes would have.
				s := NewFileStore(path)
				_, errs[i] = s.Add(Task{Title: fmt.Sprintf("Task %d", i), Status: "open"})
			}()
		}
		wg.Wait()

		for i, err := range errs {
			if err != nil {
				t.Fatalf("trial %d: Add %d failed: %v", trial, i, err)
			}
		}
		tasks, err := NewFileStore(path).GetAll()
		if err != nil {
			t.Fatal(err)
		}
		if len(tasks) != n {
			t.Fatalf("trial %d: expected %d tasks, got %d - a create raced a committed write", trial, n, len(tasks))
		}
	}
}

// TestConcurrentAdds_AllLand is the in-process half of task 68's acceptance
// check: with retries, concurrent Add calls all land instead of a slice of
// them exiting on contention. The process-level half is in
// cmd/md/concurrency_test.go.
func TestConcurrentAdds_AllLand(t *testing.T) {
	s := newOSTestStore(t, "# Tasks\n\na test log\n\n* created: 2026-01-01T00:00:00Z\n")

	const n = 25
	var wg sync.WaitGroup
	errs := make([]error, n)
	wg.Add(n)
	for i := range n {
		go func() {
			defer wg.Done()
			_, errs[i] = s.Add(Task{Title: fmt.Sprintf("Task %d", i), Status: "open"})
		}()
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Errorf("Add %d failed: %v", i, err)
		}
	}
	tasks, err := s.GetAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != n {
		t.Fatalf("expected %d tasks, got %d", n, len(tasks))
	}
	// Every title present exactly once, and every ID distinct: a lock that
	// merely fails loudly would pass the count check after retries, but a
	// lock that let two writers through would not pass these.
	titles := map[string]int{}
	ids := map[int]int{}
	for _, task := range tasks {
		titles[task.Title]++
		ids[task.ID]++
	}
	for i := range n {
		if got := titles[fmt.Sprintf("Task %d", i)]; got != 1 {
			t.Errorf("title %q appears %d times, want 1", fmt.Sprintf("Task %d", i), got)
		}
	}
	for id, count := range ids {
		if count != 1 {
			t.Errorf("id %d assigned to %d tasks", id, count)
		}
	}
}

func TestAcquireLock_MalformedLockIgnored(t *testing.T) {
	s := newTestStore(t, "# Tasks\nlock:nope\nlock:also:bad:extra\n")

	id, _, err := s.acquireLock()
	if err != nil {
		t.Fatalf("should succeed past malformed locks, got: %v", err)
	}
	if id == "" {
		t.Fatal("expected non-empty lock id")
	}
}

// TestConcurrentWriters_OneWins pins tryAcquireLock's core guarantee: one
// round of the append race has exactly one winner. It calls the single-attempt
// primitive deliberately - acquireLock retries, and since nothing here ever
// releases, every loser would just burn the full backoff to reach the same
// verdict.
//
// It also uses a real temp dir rather than the memfs the other tests share.
// memfs is an unsynchronised in-memory map, so concurrent access trips -race
// inside the dependency itself (go-billy memfs.content.WriteAt vs .Len, with
// no meads frame between them) - a fixture artifact that made this test look
// like a lock defect for months. Real writers use osfs anyway.
func TestConcurrentWriters_OneWins(t *testing.T) {
	s := newOSTestStore(t, "# Tasks\n")

	n := 10
	var mu sync.Mutex
	var winners []string
	var losers int
	var wg sync.WaitGroup
	wg.Add(n)

	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			id, _, err := s.tryAcquireLock()
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				losers++
			} else {
				winners = append(winners, id)
			}
		}()
	}
	wg.Wait()

	if len(winners) != 1 {
		t.Errorf("expected exactly 1 winner, got %d", len(winners))
	}
	if losers != n-1 {
		t.Errorf("expected %d losers, got %d", n-1, losers)
	}
}

func TestAcquireRelease_RoundTrip(t *testing.T) {
	original := "# Tasks\n\n## 1 My task\n\n* status: open\n"
	s := newTestStore(t, original)

	id, content, err := s.acquireLock()
	if err != nil {
		t.Fatalf("acquire failed: %v", err)
	}
	if id == "" {
		t.Fatal("empty lock id")
	}

	updated := content + "\n## 2 New task\n\n* status: open\n"
	if err := s.releaseLock(updated); err != nil {
		t.Fatalf("release failed: %v", err)
	}

	data, err := util.ReadFile(s.fs, s.file)
	if err != nil {
		t.Fatal(err)
	}
	got := string(data)
	if strings.Contains(got, "lock:") {
		t.Error("lock lines remain after release")
	}
	if !strings.Contains(got, "## 2 New task") {
		t.Error("new content missing after release")
	}
}
