package main

import (
	"strings"
	"testing"
	"time"
)

// shortenStageBackoff keeps the retry shape (six attempts) but collapses the
// waits, so tests exercising the give-up path do not spend the real ~1.2s.
func shortenStageBackoff(t *testing.T) {
	t.Helper()
	original := stageBackoff
	stageBackoff = []time.Duration{
		time.Millisecond, time.Millisecond, time.Millisecond,
		time.Millisecond, time.Millisecond, time.Millisecond,
	}
	t.Cleanup(func() { stageBackoff = original })
}

// The task 67 failure: a concurrent git process holds index.lock for a moment
// and the hook's lone `git add` loses the race. Retrying rides it out.
func TestStageFile_RetriesUntilIndexLockClears(t *testing.T) {
	h := newHarness(t)
	h.addTask("A task")

	h.createIndexLock()
	go func() {
		time.Sleep(50 * time.Millisecond)
		h.removeIndexLock()
	}()

	if err := stageFile(h.globals.git(), h.globals.TasksFile); err != nil {
		t.Fatalf("stageFile should have waited out the lock, got: %v", err)
	}
	if !h.tasksFileStaged() {
		t.Fatal("tasks file was not staged")
	}
}

// A lock nobody releases is a stale one; waiting cannot fix it, so the error
// must reach the caller — and must name index.lock so the cause is obvious.
func TestStageFile_GivesUpOnStuckIndexLock(t *testing.T) {
	shortenStageBackoff(t)
	h := newHarness(t)
	h.addTask("A task")
	h.createIndexLock()
	defer h.removeIndexLock()

	err := stageFile(h.globals.git(), h.globals.TasksFile)
	if err == nil {
		t.Fatal("expected an error while index.lock is permanently held")
	}
	if !strings.Contains(err.Error(), "index.lock") {
		t.Fatalf("error should name index.lock, got: %v", err)
	}
	if !strings.Contains(err.Error(), "staging") {
		t.Fatalf("error should be wrapped with staging context, got: %v", err)
	}
}

// Only lock contention is worth waiting on. Anything else is a real failure and
// must surface immediately rather than after the full backoff.
func TestStageFile_DoesNotRetryOtherFailures(t *testing.T) {
	h := newHarness(t)

	start := time.Now()
	err := stageFile(h.globals.git(), h.dir+"/no-such-file.md")
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected an error adding a nonexistent path")
	}
	if !strings.Contains(err.Error(), "did not match any files") {
		t.Fatalf("git's message should survive, got: %v", err)
	}
	if elapsed > 500*time.Millisecond {
		t.Fatalf("non-lock failure retried; returned after %s", elapsed)
	}
}

// The hooks are the reason stageFile exists: closing a task and staging it must
// survive a brief lock, rather than aborting the commit the hook runs inside.
func TestAutoDelete_SurvivesTransientIndexLock(t *testing.T) {
	h := newHarness(t)
	id1 := h.addTask("Open task")
	id2 := h.addTask("Closed task")
	h.commit("add tasks")
	h.closeTask(id2)

	h.createIndexLock()
	go func() {
		time.Sleep(50 * time.Millisecond)
		h.removeIndexLock()
	}()

	if err := h.runAutoDelete(); err != nil {
		t.Fatalf("runAutoDelete should have ridden out the lock, got: %v", err)
	}
	h.assertTaskCount(1)
	h.assertTaskExists(id1)
	h.assertTaskNotExists(id2)
}

func TestAutoSave_SurvivesTransientIndexLock(t *testing.T) {
	h := newHarness(t)
	h.addTask("A task")

	h.createIndexLock()
	go func() {
		time.Sleep(50 * time.Millisecond)
		h.removeIndexLock()
	}()

	if err := h.runAutoSave(); err != nil {
		t.Fatalf("runAutoSave should have ridden out the lock, got: %v", err)
	}
	if !h.tasksFileStaged() {
		t.Fatal("tasks file was not staged")
	}
}
