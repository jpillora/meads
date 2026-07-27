package webui

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/jpillora/meads/pkg/meads"
)

// Tests for the two watcher implementations startWatcher dispatches between
// (git mode phase 9, TASKS #66): fsnotify on a single tasks file in file
// mode (startFileWatcher, unchanged from before this phase - see git log for
// the pre-phase-9 version of this file), and periodic revision polling in
// git mode (startRevWatcher), which exists specifically because a ref can move
// between a loose file and .git/packed-refs with no loose-file event at all
// - something fsnotify on .git/refs/meads/tasks/** would miss entirely (see
// pollInterval's doc comment in watch.go).

// --- git mode: revision polling -------------------------------------------

// newGitTasks creates a real temporary git repository (t.TempDir()) and
// returns a meads.GitTasks backed by it (the same shape cmd/md/webui.go
// serves), plus the repo dir.
func newGitTasks(t *testing.T) (meads.GitTasks, string) {
	t.Helper()
	dir := t.TempDir()
	runGitIn(t, dir, "init", "-q", "-b", "main")
	runGitIn(t, dir, "config", "user.name", "Test")
	runGitIn(t, dir, "config", "user.email", "test@test.com")
	return meads.NewGitTasks(meads.NewGitStore(&meads.ExecGit{Dir: dir})), dir
}

func runGitIn(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v (in %s): %v\n%s", args, dir, err, out)
	}
}

func waitForEvent(t *testing.T, ch chan event, timeout time.Duration) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(timeout):
		t.Fatal("timed out waiting for a watcher event")
	}
}

func assertNoEvent(t *testing.T, ch chan event, wait time.Duration) {
	t.Helper()
	select {
	case e := <-ch:
		t.Fatalf("unexpected event: %+v", e)
	case <-time.After(wait):
	}
}

func TestStartWatcher_GitMode_FiresOnRefChange(t *testing.T) {
	store, _ := newGitTasks(t)
	bus := newEventBus()
	ch := bus.subscribe()
	defer bus.unsubscribe(ch)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w, err := startWatcher(ctx, store, bus, os.Stderr)
	if err != nil {
		t.Fatalf("startWatcher: %v", err)
	}
	defer w.Close()
	if w.fs != nil {
		t.Fatal("startWatcher in git mode should poll revisions, not start an fsnotify watcher")
	}

	if _, err := store.Add(meads.Task{Title: "a task"}); err != nil {
		t.Fatalf("Add: %v", err)
	}

	waitForEvent(t, ch, 3*pollInterval)
}

// TestStartWatcher_GitMode_FiresAfterPackRefs is the critical regression
// guard task 66 phase 9 calls for: a ref change must still be detected once
// the ref has moved out of a loose file and into .git/packed-refs - the
// exact case plain fsnotify on .git/refs/meads/tasks/** would miss, since
// nothing under that directory changes when only packed-refs is rewritten.
func TestStartWatcher_GitMode_FiresAfterPackRefs(t *testing.T) {
	store, dir := newGitTasks(t)
	created, err := store.Add(meads.Task{Title: "packed task"})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}

	bus := newEventBus()
	ch := bus.subscribe()
	defer bus.unsubscribe(ch)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w, err := startWatcher(ctx, store, bus, os.Stderr)
	if err != nil {
		t.Fatalf("startWatcher: %v", err)
	}
	defer w.Close()

	// Let the watcher observe its baseline before packing, so the pack
	// itself (no ref VALUE change, only its storage location) is not what
	// the assertion below ends up detecting.
	time.Sleep(pollInterval + 200*time.Millisecond)

	runGitIn(t, dir, "pack-refs", "--all")
	loosePath := filepath.Join(dir, ".git", "refs", "meads", "tasks", strconv.Itoa(created))
	if _, err := os.Stat(loosePath); !os.IsNotExist(err) {
		t.Fatalf("precondition: loose ref file %s should be gone after pack-refs (stat err=%v)", loosePath, err)
	}

	// Change the now-packed ref and confirm the poller still notices.
	if err := store.Update(created, func(task *meads.Task) {
		task.Title = "renamed after packing"
	}); err != nil {
		t.Fatalf("Update: %v", err)
	}

	waitForEvent(t, ch, 3*pollInterval)
}

func TestStartWatcher_GitMode_NoChangeNoEvent(t *testing.T) {
	store, _ := newGitTasks(t)
	if _, err := store.Add(meads.Task{Title: "steady state"}); err != nil {
		t.Fatalf("Add: %v", err)
	}

	bus := newEventBus()
	ch := bus.subscribe()
	defer bus.unsubscribe(ch)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w, err := startWatcher(ctx, store, bus, os.Stderr)
	if err != nil {
		t.Fatalf("startWatcher: %v", err)
	}
	defer w.Close()

	assertNoEvent(t, ch, 2*pollInterval)
}

// --- file mode: fsnotify, still reachable through the same dispatcher -----

// TestStartWatcher_FileMode_StillFsnotify proves file mode is unaffected by
// the git-mode dispatch in front of it: a markdown/csv Backend() must still
// pick the fsnotify path and fire on an ordinary write. fsnotify
// needs a real filesystem, so this uses meads.NewFileStore under t.TempDir()
// rather than memfs.
func TestStartWatcher_FileMode_StillFsnotify(t *testing.T) {
	dir := t.TempDir()
	store := meads.NewFileTasks(meads.NewFileStore(filepath.Join(dir, "TASKS.md")), nil)
	if _, err := store.Add(meads.Task{Title: "seed"}); err != nil {
		t.Fatalf("Add: %v", err)
	}

	bus := newEventBus()
	ch := bus.subscribe()
	defer bus.unsubscribe(ch)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w, err := startWatcher(ctx, store, bus, os.Stderr)
	if err != nil {
		t.Fatalf("startWatcher: %v", err)
	}
	defer w.Close()
	if w.fs == nil {
		t.Fatal("startWatcher in file mode should start an fsnotify watcher")
	}

	if _, err := store.Add(meads.Task{Title: "second"}); err != nil {
		t.Fatalf("Add: %v", err)
	}

	waitForEvent(t, ch, 2*time.Second)
}
