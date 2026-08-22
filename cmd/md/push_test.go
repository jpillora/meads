package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jpillora/meads/pkg/meads"
)

func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	orig := os.Stderr
	os.Stderr = w
	defer func() { os.Stderr = orig }()
	fn()
	_ = w.Close()
	var buf bytes.Buffer
	if _, err := io.Copy(&buf, r); err != nil {
		t.Fatalf("reading captured stderr: %v", err)
	}
	return buf.String()
}

func TestSyncForegroundPushesAndReportsSuccess(t *testing.T) {
	h := gitModeHarness(t)
	originDir := h.git("remote", "get-url", "origin")
	if err := (&addCmd{globals: h.globals, Args: []string{"sync me"}}).Run(); err != nil {
		t.Fatalf("add: %v", err)
	}
	if refs := remoteRefNames(t, originDir); hasTaskRef(refs) {
		t.Fatalf("background scheduling is disabled in command tests; origin unexpectedly has a task ref: %v", refs)
	}

	if err := (&syncCmd{globals: h.globals}).Run(); err != nil {
		t.Fatalf("sync: %v", err)
	}
	if refs := remoteRefNames(t, originDir); !hasTaskRef(refs) {
		t.Fatalf("origin refs after explicit sync = %v, want %s*", refs, meads.TasksRefPrefix)
	}
}

func hasTaskRef(refs []string) bool {
	for _, ref := range refs {
		if strings.HasPrefix(ref, meads.TasksRefPrefix) {
			return true
		}
	}
	return false
}

func TestSyncForegroundFailureIsReturned(t *testing.T) {
	h := gitModeHarness(t)
	want := errors.New("remote unavailable")
	original := syncFunc
	t.Cleanup(func() { syncFunc = original })
	syncFunc = func(context.Context, *globals) (*meads.SyncReport, error) {
		return &meads.SyncReport{Integrate: &meads.IntegrateReport{}}, want
	}
	if err := (&syncCmd{globals: h.globals}).Run(); !errors.Is(err, want) {
		t.Fatalf("sync error = %v, want %v", err, want)
	}
}

func TestSyncForegroundHonorsOptionalTimeout(t *testing.T) {
	h := gitModeHarness(t)
	t.Setenv("MEADS_SYNC_TIMEOUT", "20ms")
	original := syncFunc
	t.Cleanup(func() { syncFunc = original })
	syncFunc = func(ctx context.Context, _ *globals) (*meads.SyncReport, error) {
		<-ctx.Done()
		return &meads.SyncReport{Integrate: &meads.IntegrateReport{}}, ctx.Err()
	}
	start := time.Now()
	err := (&syncCmd{globals: h.globals}).Run()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("sync error = %v, want deadline exceeded", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("20ms timeout returned after %s", elapsed)
	}
}

func TestScheduleSyncIsBestEffortAndFileModeSkips(t *testing.T) {
	original := enqueueSyncFunc
	t.Cleanup(func() { enqueueSyncFunc = original })
	var calls atomic.Int32
	enqueueSyncFunc = func(*globals, string, time.Duration, time.Duration) error {
		calls.Add(1)
		return errors.New("cannot launch")
	}

	file := newHarness(t)
	scheduleSync(file.globals)
	if got := calls.Load(); got != 0 {
		t.Fatalf("file-mode enqueue calls = %d, want 0", got)
	}

	git := gitModeHarness(t)
	// Scheduling failure is deliberately swallowed; the mutation remains the
	// authoritative local success.
	scheduleSync(git.globals)
	if got := calls.Load(); got != 1 {
		t.Fatalf("git-mode enqueue calls = %d, want 1", got)
	}
	t.Setenv("MEADS_SYNC_DISABLE", "true")
	scheduleSync(git.globals)
	if got := calls.Load(); got != 1 {
		t.Fatalf("disabled enqueue calls = %d, want still 1", got)
	}
}

func TestSyncDelayUsesEnvironmentThenRepositoryConfig(t *testing.T) {
	h := gitModeHarness(t)
	if err := h.globals.gitStore().SetConfig(meads.Config{PushInterval: "7s"}); err != nil {
		t.Fatal(err)
	}
	if got, err := syncDelay(h.globals); err != nil || got != 7*time.Second {
		t.Fatalf("repository delay = %s, %v; want 7s", got, err)
	}
	t.Setenv("MEADS_SYNC_DELAY", "25ms")
	if got, err := syncDelay(h.globals); err != nil || got != 25*time.Millisecond {
		t.Fatalf("environment delay = %s, %v; want 25ms", got, err)
	}
}

func TestWriteSyncRequestAdvancesGenerationAtomically(t *testing.T) {
	pidPath := filepath.Join(t.TempDir(), "meads-sync.pid")
	req := syncRequest{RepoDir: "/repo", CommonDir: "/repo/.git", Generation: 10, Delay: "1s"}
	if err := writeSyncRequest(pidPath, req); err != nil {
		t.Fatal(err)
	}
	if err := writeSyncRequest(pidPath, req); err != nil {
		t.Fatal(err)
	}
	got, err := readSyncRequest(syncRequestPath(pidPath, req.CommonDir))
	if err != nil {
		t.Fatal(err)
	}
	if got.Generation != 11 {
		t.Fatalf("generation = %d, want 11", got.Generation)
	}
}

func TestDivergenceMessage(t *testing.T) {
	if got := divergenceMessage(&meads.SyncReport{}); got != "" {
		t.Fatalf("clean report message = %q", got)
	}
	got := divergenceMessage(&meads.SyncReport{Rejected: true})
	for _, want := range []string{"diverged", "md sync", "NOT force-push"} {
		if !strings.Contains(got, want) {
			t.Errorf("message %q missing %q", got, want)
		}
	}
}
