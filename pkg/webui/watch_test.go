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
// the pre-phase-9 version of this file), and periodic ref-oid polling in git
// mode (startRefWatcher), which exists specifically because a ref can move
// between a loose file and .git/packed-refs with no loose-file event at all
// - something fsnotify on .git/refs/meads/tasks/** would miss entirely (see
// pollInterval's doc comment in watch.go).

// --- git mode: ref-oid polling --------------------------------------------

// gitTaskStoreStub adapts *meads.GitStore to meads.TaskStore + refSnapshotter
// for these tests, mirroring cmd/md/webui.go's gitWatchStore (which lives in
// package main and so can't be imported from here). Only the methods these
// tests actually exercise (Add via Create, Update) are more than a direct
// pass-through; see cmd/md/taskstore.go's gitTaskStore for why the shapes
// differ from GitStore's own Create/Update.
type gitTaskStoreStub struct {
	gs *meads.GitStore
}

func (s gitTaskStoreStub) Get(ids []int) ([]meads.Task, error) { return s.gs.Get(ids) }
func (s gitTaskStoreStub) Ready() ([]meads.Task, error)        { return s.gs.Ready() }

func (s gitTaskStoreStub) Add(t meads.Task) (int, error) {
	created, err := s.gs.Create(t)
	if err != nil {
		return 0, err
	}
	return created.ID, nil
}

func (s gitTaskStoreStub) Update(id int, fn func(*meads.Task)) error {
	_, err := s.gs.Update(id, func(t *meads.Task) (bool, error) {
		fn(t)
		return true, nil
	})
	return err
}

func (s gitTaskStoreStub) Delete(id int) error {
	_, err := s.gs.SoftDelete(id)
	return err
}

func (s gitTaskStoreStub) TaskRefOIDs() (map[string]meads.OID, error) {
	return s.gs.TaskRefOIDs()
}

// newGitTaskStoreStub creates a real temporary git repository (t.TempDir())
// and returns a gitTaskStoreStub backed by it, plus the repo dir.
func newGitTaskStoreStub(t *testing.T) (gitTaskStoreStub, string) {
	t.Helper()
	dir := t.TempDir()
	runGitIn(t, dir, "init", "-q", "-b", "main")
	runGitIn(t, dir, "config", "user.name", "Test")
	runGitIn(t, dir, "config", "user.email", "test@test.com")
	return gitTaskStoreStub{gs: meads.NewGitStore(&meads.ExecGit{Dir: dir})}, dir
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
	store, _ := newGitTaskStoreStub(t)
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
		t.Fatal("startWatcher against a refSnapshotter store should not start an fsnotify watcher")
	}

	if _, err := store.gs.Create(meads.Task{Title: "a task"}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	waitForEvent(t, ch, 3*pollInterval)
}

// TestStartWatcher_GitMode_FiresAfterPackRefs is the critical regression
// guard task 66 phase 9 calls for: a ref change must still be detected once
// the ref has moved out of a loose file and into .git/packed-refs - the
// exact case plain fsnotify on .git/refs/meads/tasks/** would miss, since
// nothing under that directory changes when only packed-refs is rewritten.
func TestStartWatcher_GitMode_FiresAfterPackRefs(t *testing.T) {
	store, dir := newGitTaskStoreStub(t)
	created, err := store.gs.Create(meads.Task{Title: "packed task"})
	if err != nil {
		t.Fatalf("Create: %v", err)
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
	loosePath := filepath.Join(dir, ".git", "refs", "meads", "tasks", strconv.Itoa(created.ID))
	if _, err := os.Stat(loosePath); !os.IsNotExist(err) {
		t.Fatalf("precondition: loose ref file %s should be gone after pack-refs (stat err=%v)", loosePath, err)
	}

	// Change the now-packed ref and confirm the poller still notices.
	if _, err := store.gs.Update(created.ID, func(task *meads.Task) (bool, error) {
		task.Title = "renamed after packing"
		return true, nil
	}); err != nil {
		t.Fatalf("Update: %v", err)
	}

	waitForEvent(t, ch, 3*pollInterval)
}

func TestStartWatcher_GitMode_NoChangeNoEvent(t *testing.T) {
	store, _ := newGitTaskStoreStub(t)
	if _, err := store.gs.Create(meads.Task{Title: "steady state"}); err != nil {
		t.Fatalf("Create: %v", err)
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

func TestRefOIDsEqual(t *testing.T) {
	a := map[string]meads.OID{"refs/meads/tasks/1": "aaa", "refs/meads/tasks/2": "bbb"}
	same := map[string]meads.OID{"refs/meads/tasks/1": "aaa", "refs/meads/tasks/2": "bbb"}
	if !refOIDsEqual(a, same) {
		t.Error("identical maps should compare equal")
	}
	if !refOIDsEqual(nil, map[string]meads.OID{}) {
		t.Error("nil and empty should compare equal")
	}
	changedValue := map[string]meads.OID{"refs/meads/tasks/1": "aaa", "refs/meads/tasks/2": "ccc"}
	if refOIDsEqual(a, changedValue) {
		t.Error("a changed oid should compare unequal")
	}
	fewer := map[string]meads.OID{"refs/meads/tasks/1": "aaa"}
	if refOIDsEqual(a, fewer) {
		t.Error("a removed ref should compare unequal")
	}
	more := map[string]meads.OID{"refs/meads/tasks/1": "aaa", "refs/meads/tasks/2": "bbb", "refs/meads/tasks/3": "ccc"}
	if refOIDsEqual(a, more) {
		t.Error("an added ref should compare unequal")
	}
}

// --- file mode: fsnotify, still reachable through the same dispatcher -----

// TestStartWatcher_FileMode_StillFsnotify proves file mode is unaffected by
// the git-mode dispatch added in front of it: *meads.Store alone (no
// wrapping needed) satisfies both meads.TaskStore and fileLocator, so
// startWatcher must still pick the fsnotify path and fire on an ordinary
// write. fsnotify needs a real filesystem, so this uses meads.NewFileStore
// under t.TempDir() rather than memfs.
func TestStartWatcher_FileMode_StillFsnotify(t *testing.T) {
	dir := t.TempDir()
	store := meads.NewFileStore(filepath.Join(dir, "TASKS.md"))
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
		t.Fatal("startWatcher against a fileLocator store should start an fsnotify watcher")
	}

	if _, err := store.Add(meads.Task{Title: "second"}); err != nil {
		t.Fatalf("Add: %v", err)
	}

	waitForEvent(t, ch, 2*time.Second)
}

func TestStartWatcher_NeitherInterface_ReturnsNilNotError(t *testing.T) {
	bus := newEventBus()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w, err := startWatcher(ctx, noopStore{}, bus, os.Stderr)
	if err != nil {
		t.Fatalf("startWatcher with a store implementing neither optional interface should not error, got: %v", err)
	}
	if w != nil {
		t.Fatalf("startWatcher with a store implementing neither optional interface = %v, want nil watcher", w)
	}
	w.Close() // must be a safe no-op on a nil *watcher
}

// noopStore satisfies meads.TaskStore but neither fileLocator nor
// refSnapshotter, exercising startWatcher's final fallback branch.
type noopStore struct{}

func (noopStore) Get(ids []int) ([]meads.Task, error)       { return nil, nil }
func (noopStore) Ready() ([]meads.Task, error)              { return nil, nil }
func (noopStore) Add(t meads.Task) (int, error)             { return 0, nil }
func (noopStore) Update(id int, fn func(*meads.Task)) error { return nil }
func (noopStore) Delete(id int) error                       { return nil }
