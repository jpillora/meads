package main

import (
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jpillora/meads/pkg/meads"
)

// Tests for git mode phase 6 (TASKS #63): synchronous, timeout-bounded
// auto-push of refs/meads/* to origin. Like git_mode_test.go and
// init_git_test.go, these run against real temporary git repositories (and,
// for the end-to-end/divergence tests, real local bare "remotes") under
// t.TempDir() rather than fakes - what's under test (a real non-fast-forward
// rejection, a real push landing on a real remote) is precisely what a fake
// would rubber-stamp without exercising. The one deliberate exception is
// pushFunc itself, which IS a seam (see push.go) specifically so the
// "bounded by pushTimeout" requirement can be proven without a real hung
// network.

// --- helpers ---

// captureStderr redirects os.Stderr for the duration of fn and returns
// everything written to it. Safe here because autoPush runs (and writes any
// warning) entirely synchronously and completes before Run() returns - no
// concurrent writer can race the swapped stderr.
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

	w.Close()
	var buf bytes.Buffer
	if _, err := io.Copy(&buf, r); err != nil {
		t.Fatalf("reading captured stderr: %v", err)
	}
	return buf.String()
}

// lastPushPath returns the path autoPush/GitStore use for the last-push
// timestamp under commonDir. Duplicated as a literal ("last-push") rather
// than importing an unexported pkg/meads constant, matching how other
// cross-package tests in this file already hardcode meads-internal paths
// (see harness_test.go's preCommitHookPath).
func lastPushPath(commonDir string) string {
	return filepath.Join(commonDir, meads.PushStateDir, "last-push")
}

// --- 1. bounded by pushTimeout, not blocked forever ---

// TestAutoPush_BoundedByTimeout proves autoPush's synchronous push is
// bounded by pushTimeout rather than able to hang indefinitely: the
// injected pusher simulates a genuinely hung push - exactly the way a real
// one bounded by exec.CommandContext behaves, by respecting ctx.Done()
// instead of ignoring it - so the only thing that can make autoPush return
// is pushTimeout's deadline firing. It also confirms the timeout path warns
// on stderr without failing the mutation.
func TestAutoPush_BoundedByTimeout(t *testing.T) {
	h := gitModeHarness(t)

	orig := pushFunc
	t.Cleanup(func() { pushFunc = orig })
	pushFunc = func(ctx context.Context, dir string) (string, error) {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(time.Hour): // far longer than pushTimeout; must never actually be reached
			return "unreachable: pushFunc should have been cancelled at the deadline", nil
		}
	}

	done := make(chan error, 1)
	start := time.Now()
	stderr := captureStderr(t, func() {
		go func() {
			done <- (&addCmd{globals: h.globals, Args: []string{"task while push hangs"}}).Run()
		}()
		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("add: %v", err)
			}
		case <-time.After(pushTimeout + 5*time.Second):
			t.Fatal("add did not return even 5s past pushTimeout - the injected pusher's ctx.Done() branch should have fired at the deadline")
		}
	})

	elapsed := time.Since(start)
	if elapsed < pushTimeout {
		t.Errorf("add returned after %v, want at least pushTimeout (%v) - it should have been cancelled AT the deadline, not before", elapsed, pushTimeout)
	}
	if elapsed > pushTimeout+3*time.Second {
		t.Errorf("add took %v to return, want close to pushTimeout (%v)", elapsed, pushTimeout)
	}
	if !strings.Contains(strings.ToLower(stderr), "timed out") {
		t.Errorf("stderr = %q, want it to mention the push timing out", stderr)
	}

	// The mutation itself must have gone through regardless.
	gs := meads.NewGitStore(h.globals.git())
	tasks, err := gs.Get(nil)
	if err != nil || len(tasks) != 1 || tasks[0].Title != "task while push hangs" {
		t.Fatalf("task should be committed locally despite the push timing out: tasks=%v err=%v", tasks, err)
	}
}

// --- 2. push fires only when the interval has elapsed ---

func TestAutoPush_FiresOnlyWhenIntervalElapsed(t *testing.T) {
	h := gitModeHarness(t)
	gs := meads.NewGitStore(h.globals.git())
	if err := gs.SetConfig(meads.Config{PushInterval: "1h"}); err != nil {
		t.Fatalf("SetConfig: %v", err)
	}

	var calls int32
	orig := pushFunc
	t.Cleanup(func() { pushFunc = orig })
	pushFunc = func(ctx context.Context, dir string) (string, error) {
		atomic.AddInt32(&calls, 1)
		return "", nil
	}

	// First-ever mutation: no last-push record at all yet -> due.
	if err := (&addCmd{globals: h.globals, Args: []string{"first"}}).Run(); err != nil {
		t.Fatalf("add (1st): %v", err)
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("pushFunc calls after 1st add = %d, want 1 (no prior record -> due)", got)
	}

	// Second mutation immediately after: last-push was just marked, interval
	// is 1h -> must NOT fire again.
	if err := (&addCmd{globals: h.globals, Args: []string{"second"}}).Run(); err != nil {
		t.Fatalf("add (2nd): %v", err)
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("pushFunc calls after 2nd add = %d, want still 1 (interval has not elapsed)", got)
	}

	// Manually backdate the last-push record beyond the interval: the next
	// mutation must fire again.
	commonDir, err := gitCommonDir(h.globals)
	if err != nil {
		t.Fatalf("gitCommonDir: %v", err)
	}
	backdated := time.Now().Add(-2 * time.Hour).UnixNano()
	if err := os.WriteFile(lastPushPath(commonDir), []byte(strconv.FormatInt(backdated, 10)), 0644); err != nil {
		t.Fatalf("backdating last-push: %v", err)
	}
	if err := (&addCmd{globals: h.globals, Args: []string{"third"}}).Run(); err != nil {
		t.Fatalf("add (3rd): %v", err)
	}
	if got := atomic.LoadInt32(&calls); got != 2 {
		t.Fatalf("pushFunc calls after 3rd add (post-backdate) = %d, want 2 (interval has now elapsed)", got)
	}
}

// --- 3. push failure never fails the mutation ---

func TestAutoPush_PushFailureDoesNotFailMutation(t *testing.T) {
	h := gitModeHarness(t)
	// A bad LOCAL path (not a network address) as origin: `git push` fails
	// fast and entirely locally, so this test cannot hang regardless of
	// sandboxing/network availability. TestAutoPush_BoundedByTimeout above
	// is what proves a genuinely SLOW/hung push doesn't block past
	// pushTimeout; this test is about FAILURE, not slowness.
	h.git("remote", "set-url", "origin", filepath.Join(t.TempDir(), "does-not-exist"))

	if err := (&addCmd{globals: h.globals, Args: []string{"survives bad remote"}}).Run(); err != nil {
		t.Fatalf("add with a broken origin should still succeed, got: %v", err)
	}

	gs := meads.NewGitStore(h.globals.git())
	tasks, err := gs.Get(nil)
	if err != nil || len(tasks) != 1 || tasks[0].Title != "survives bad remote" {
		t.Fatalf("task should be committed locally despite the push failure: tasks=%v err=%v", tasks, err)
	}
}

// --- 4. last-push state lives under the git common dir and is not a ref ---

func TestAutoPush_LastPushStateIsNotARef(t *testing.T) {
	h := gitModeHarness(t)

	if err := (&addCmd{globals: h.globals, Args: []string{"triggers a push"}}).Run(); err != nil {
		t.Fatalf("add: %v", err)
	}

	commonDir, err := gitCommonDir(h.globals)
	if err != nil {
		t.Fatalf("gitCommonDir: %v", err)
	}
	if _, err := os.Stat(lastPushPath(commonDir)); err != nil {
		t.Fatalf("last-push file missing at %s: %v", lastPushPath(commonDir), err)
	}

	rs := meads.NewRefStore(h.globals.git())
	refs, err := rs.ListRefs(meads.RefNamespace)
	if err != nil {
		t.Fatalf("ListRefs: %v", err)
	}
	for name := range refs {
		if strings.Contains(strings.ToLower(name), "push") {
			t.Errorf("refs/meads/* unexpectedly contains a push-related ref: %s", name)
		}
	}
}

// --- 5. linked worktrees share one push cadence ---

func TestAutoPush_LinkedWorktreesShareLastPushState(t *testing.T) {
	h := gitModeHarness(t)
	worktreeDir := filepath.Join(t.TempDir(), "wt")
	h.git("worktree", "add", "-b", "wt-branch", worktreeDir)

	commonDirMain, err := gitCommonDir(h.globals)
	if err != nil {
		t.Fatalf("gitCommonDir (main worktree): %v", err)
	}
	wtGlobals := &globals{Git: &meads.ExecGit{Dir: worktreeDir}, Dir: worktreeDir}
	commonDirWt, err := gitCommonDir(wtGlobals)
	if err != nil {
		t.Fatalf("gitCommonDir (linked worktree): %v", err)
	}
	if commonDirMain != commonDirWt {
		t.Fatalf("common dirs differ: main=%s linked=%s, want identical (one ref store -> one push cadence)", commonDirMain, commonDirWt)
	}

	gs := meads.NewGitStore(h.globals.git())
	if err := gs.MarkPushed(commonDirMain); err != nil {
		t.Fatalf("MarkPushed from main worktree: %v", err)
	}
	should, err := gs.ShouldPush(commonDirWt, time.Hour)
	if err != nil {
		t.Fatalf("ShouldPush from linked worktree: %v", err)
	}
	if should {
		t.Error("linked worktree should see the main worktree's just-marked push state, but ShouldPush = true")
	}
	if _, err := os.Stat(lastPushPath(commonDirMain)); err != nil {
		t.Fatalf("shared last-push file missing at %s: %v", lastPushPath(commonDirMain), err)
	}
}

// --- 6. divergence: a clear, actionable message, surfaced immediately ---

// setupDivergedGitModeClones creates a bare "remote" repo under /tmp and two
// independent git-mode clones of it. It deliberately diverges task 1
// between them: clone1 (g1) updates and pushes it, then clone2 (g2) -
// without ever fetching that change - also updates it from its own,
// now-stale, locally-known parent. clone2's next push of refs/meads/* is
// therefore guaranteed to be rejected as non-fast-forward.
func setupDivergedGitModeClones(t *testing.T) (g1, g2 *globals, clone1, clone2, bareDir string) {
	t.Helper()

	// Every add/update below runs through the real, now-synchronous
	// autoPush as a side effect. Left alone, that would reveal (and
	// interfere with) the divergence WHILE this helper is still
	// constructing it - e.g. g2's own diverging update would immediately
	// attempt, and get rejected by, the exact push a caller wants to
	// trigger and observe itself. Suppress the actual push for the
	// duration of setup (autoPush's ShouldPush/MarkPushed bookkeeping still
	// runs normally either way - see the cleanup loop below), restoring the
	// real pushFunc via a plain defer - NOT t.Cleanup, which would only
	// restore at the whole test's end - so callers get the real pipeline
	// for anything they do AFTER this helper returns.
	realPushFunc := pushFunc
	pushFunc = func(ctx context.Context, dir string) (string, error) { return "", nil }
	defer func() { pushFunc = realPushFunc }()

	bareDir = t.TempDir()
	runGit(t, bareDir, "init", "--bare", "-b", "main")

	clone1 = t.TempDir()
	runGit(t, "", "clone", bareDir, clone1)
	runGit(t, clone1, "config", "user.name", "Clone1")
	runGit(t, clone1, "config", "user.email", "clone1@test.com")
	runGit(t, clone1, "commit", "--allow-empty", "-m", "root")
	runGit(t, clone1, "push", "origin", "main")

	g1 = &globals{Git: &meads.ExecGit{Dir: clone1}, Dir: clone1, TasksFile: "TASKS.md", GitMode: true}
	if err := (&initCmd{globals: g1, Git: true}).Run(); err != nil {
		t.Fatalf("init --git (clone1): %v", err)
	}
	if err := (&addCmd{globals: g1, Args: []string{"shared task"}}).Run(); err != nil {
		t.Fatalf("add (clone1): %v", err)
	}
	runGit(t, clone1, "push", "origin", "refs/meads/*:refs/meads/*")

	clone2 = t.TempDir()
	runGit(t, "", "clone", bareDir, clone2)
	runGit(t, clone2, "config", "user.name", "Clone2")
	runGit(t, clone2, "config", "user.email", "clone2@test.com")
	runGit(t, clone2, "fetch", "origin", "+refs/meads/*:refs/meads/*")
	g2 = &globals{Git: &meads.ExecGit{Dir: clone2}, Dir: clone2, TasksFile: "TASKS.md", GitMode: true}

	// clone1 changes task 1 again and pushes: this becomes the state the
	// remote actually ends up with.
	if err := (&updateCmd{globals: g1, ID: "1", Title: "clone1's update"}).Run(); err != nil {
		t.Fatalf("update (clone1): %v", err)
	}
	runGit(t, clone1, "push", "origin", "refs/meads/*:refs/meads/*")

	// clone2 changes task 1 too, WITHOUT ever fetching clone1's update - its
	// locally-known parent for task 1 is now stale relative to the remote.
	if err := (&updateCmd{globals: g2, ID: "1", Title: "clone2's update"}).Run(); err != nil {
		t.Fatalf("update (clone2): %v", err)
	}

	// autoPush's decision logic (ShouldPush/MarkPushed) ran on every
	// add/update above even with pushFunc suppressed - clear both clones'
	// push state so a caller's own next mutation is correctly seen as due,
	// rather than silently skipped as "too recent to push".
	for _, g := range []*globals{g1, g2} {
		commonDir, err := gitCommonDir(g)
		if err != nil {
			t.Fatalf("gitCommonDir: %v", err)
		}
		if err := os.RemoveAll(filepath.Join(commonDir, meads.PushStateDir)); err != nil {
			t.Fatalf("clearing push state for %s: %v", g.Dir, err)
		}
	}

	return g1, g2, clone1, clone2, bareDir
}

// TestDivergenceMessage_RealDivergedPushIsRecognized runs an ACTUAL `git
// push` against the manufactured divergence (not a canned/fabricated
// string) and confirms divergenceMessage correctly classifies its real
// output - see TestDivergenceMessage_TableDriven below for the fast,
// fabricated-input coverage of the same function.
func TestDivergenceMessage_RealDivergedPushIsRecognized(t *testing.T) {
	_, _, _, clone2, _ := setupDivergedGitModeClones(t)

	cmd := exec.Command("git", "push", "--porcelain", "origin", "refs/meads/*:refs/meads/*")
	cmd.Dir = clone2
	output, pushErr := cmd.CombinedOutput()
	if pushErr == nil {
		t.Fatalf("expected clone2's push to be rejected (diverged), but it succeeded:\n%s", output)
	}
	t.Logf("real diverged push output:\n%s", output)

	msg := divergenceMessage(string(output))
	if msg == "" {
		t.Fatalf("divergenceMessage did not recognize a real diverged-push rejection; raw output:\n%s", output)
	}
	if !strings.Contains(strings.ToLower(msg), "diverg") {
		t.Errorf("message = %q, want it to mention divergence", msg)
	}
	if !strings.Contains(msg, "NOT force-push") {
		t.Errorf("message = %q, want it to explicitly rule out force-pushing", msg)
	}
}

// TestAutoPush_DivergenceWarningSurfacesOnSameCommand drives the REAL,
// synchronous pipeline (autoPush -> runPush -> a real `git push`) against
// the manufactured divergence and proves the warning appears on stderr of
// THE SAME COMMAND that triggered the rejected push - not a later one. This
// is the whole point of going synchronous: the earlier async design could
// only ever surface this one command late, which was confusing UX.
func TestAutoPush_DivergenceWarningSurfacesOnSameCommand(t *testing.T) {
	_, g2, _, _, _ := setupDivergedGitModeClones(t)

	// clone2 (g2) has a cleared push-state record (see the helper) and a
	// locally-diverged task 1, so this mutation's autoPush is due
	// immediately and will attempt - and be rejected by - the diverged push
	// for real, synchronously, within this very call.
	stderr := captureStderr(t, func() {
		if err := (&updateCmd{globals: g2, ID: "1", Title: "clone2 tries to push its divergence"}).Run(); err != nil {
			t.Fatalf("update: %v", err)
		}
	})
	if !strings.Contains(strings.ToLower(stderr), "diverg") {
		t.Errorf("stderr on the triggering command = %q, want it to mention divergence", stderr)
	}
}

// TestDivergenceMessage_TableDriven covers divergenceMessage's classification
// rules directly against fabricated (but format-accurate, cross-checked
// against TestDivergenceMessage_RealDivergedPushIsRecognized's real output)
// `git push --porcelain` text, including a mixed batch where only ONE ref
// among several is rejected - exactly what happens when refs/meads/*
// expands to many refs and only one has actually diverged.
func TestDivergenceMessage_TableDriven(t *testing.T) {
	tests := []struct {
		name        string
		output      string
		wantMessage bool
	}{
		{"empty", "", false},
		{"new reference", "To /tmp/bare\n*\trefs/meads/tasks/1:refs/meads/tasks/1\t[new reference]\nDone\n", false},
		{"up to date", "To /tmp/bare\n=\trefs/meads/tasks/1:refs/meads/tasks/1\t[up to date]\nDone\n", false},
		{"non-fast-forward", "To /tmp/bare\n!\trefs/meads/tasks/1:refs/meads/tasks/1\t[rejected] (non-fast-forward)\nDone\n", true},
		{"fetch first", "To /tmp/bare\n!\trefs/meads/tasks/1:refs/meads/tasks/1\t[rejected] (fetch first)\nDone\n", true},
		{"stale info", "To /tmp/bare\n!\trefs/meads/tasks/1:refs/meads/tasks/1\t[rejected] (stale info)\nDone\n", true},
		{"unrelated rejection", "To /tmp/bare\n!\trefs/meads/tasks/1:refs/meads/tasks/1\t[remote rejected] (permission denied)\nDone\n", false},
		{"mixed batch: one ok, one diverged", "To /tmp/bare\n*\trefs/meads/tasks/2:refs/meads/tasks/2\t[new reference]\n!\trefs/meads/tasks/1:refs/meads/tasks/1\t[rejected] (fetch first)\nDone\n", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := divergenceMessage(tt.output)
			if (got != "") != tt.wantMessage {
				t.Errorf("divergenceMessage(%q) = %q, want non-empty=%v", tt.output, got, tt.wantMessage)
			}
			if tt.wantMessage && !strings.Contains(strings.ToLower(got), "diverg") {
				t.Errorf("message = %q, want it to mention divergence", got)
			}
		})
	}
}

// --- 7. end to end: a real local bare remote sees the push ---

// TestAutoPush_EndToEnd_MutationPushesToRemote proves a real mutation
// against a real local bare remote results in refs/meads/* landing there -
// asserted directly, not polled: the push is synchronous now, so by the
// time Run() returns it has already completed (or failed).
func TestAutoPush_EndToEnd_MutationPushesToRemote(t *testing.T) {
	h := gitModeHarness(t)
	originDir := h.git("remote", "get-url", "origin")

	if err := (&addCmd{globals: h.globals, Args: []string{"push me"}}).Run(); err != nil {
		t.Fatalf("add: %v", err)
	}

	found := false
	for _, r := range remoteRefNames(t, originDir) {
		if strings.HasPrefix(r, meads.TasksRefPrefix) {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("refs/meads/tasks/* did not appear on the remote after a synchronous auto-push")
	}
}

// --- 8. file mode is unaffected ---

func TestAutoPush_FileModeUnaffected(t *testing.T) {
	h := newHarness(t) // absolute TasksFile -> explicit -> file mode (see mode_test.go)

	var calls int32
	orig := pushFunc
	t.Cleanup(func() { pushFunc = orig })
	pushFunc = func(ctx context.Context, dir string) (string, error) {
		atomic.AddInt32(&calls, 1)
		return "", nil
	}

	if err := (&addCmd{globals: h.globals, Args: []string{"file mode task"}}).Run(); err != nil {
		t.Fatalf("add: %v", err)
	}
	if got := atomic.LoadInt32(&calls); got != 0 {
		t.Fatalf("pushFunc called %d times in file mode, want 0", got)
	}

	commonDir, err := gitCommonDir(h.globals)
	if err != nil {
		t.Fatalf("gitCommonDir: %v", err)
	}
	if _, err := os.Stat(filepath.Join(commonDir, meads.PushStateDir)); !os.IsNotExist(err) {
		t.Fatalf("meads push-state dir should not exist in file mode (stat err=%v)", err)
	}
}
