package meads

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// Tests for the one-shot clone resolution (clone.go) and its wiring into
// OpenTasks (open.go): a fresh clone of a git-mode repo is adopted (origin's
// refs fetched) rather than silently falling back to file mode, and the
// InitCheckRef marker caches "origin has no refs/meads/*" so the ls-remote
// happens at most once per clone, ever.

// newGitModeOrigin seeds a bare origin repo holding a git-mode task store
// (the default config ref plus the given task titles) and returns its path.
// The refs are pushed from a throwaway source repo, mirroring how a real
// origin gets them.
func newGitModeOrigin(t *testing.T, titles ...string) string {
	t.Helper()
	src := newDetectRepo(t)
	gs := NewGitStore(&ExecGit{Dir: src})
	if err := gs.SetConfig(DefaultConfig()); err != nil {
		t.Fatalf("SetConfig: %v", err)
	}
	for _, title := range titles {
		if _, err := gs.Create(Task{Title: title, Status: "open"}); err != nil {
			t.Fatalf("Create(%q): %v", title, err)
		}
	}
	bare := t.TempDir()
	runGit(t, bare, "init", "--bare", "-b", "main")
	runGit(t, src, "remote", "add", "origin", bare)
	runGit(t, src, "push", "origin", RefNamespace+"*:"+RefNamespace+"*")
	return bare
}

// cloneDir git-clones origin into a fresh dir and returns the clone path
// (named unlike gitdiverge_test.go's cloneRepo, which is a store-pair
// struct).
func cloneDir(t *testing.T, origin string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "clone")
	runGit(t, t.TempDir(), "clone", origin, dir)
	runGit(t, dir, "config", "user.name", "Test")
	runGit(t, dir, "config", "user.email", "test@test.com")
	return dir
}

// lsRemoteCounter wraps a Git and counts ls-remote invocations, proving the
// resolution's one-shot property.
type lsRemoteCounter struct {
	Git
	mu sync.Mutex
	n  int
}

func (c *lsRemoteCounter) Output(args ...string) (string, error) {
	if len(args) > 0 && args[0] == "ls-remote" {
		c.mu.Lock()
		c.n++
		c.mu.Unlock()
	}
	return c.Git.Output(args...)
}

func (c *lsRemoteCounter) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.n
}

// TestOpenTasks_CloneAdopts is the headline acceptance case: clone a
// git-mode repo, open the store (no bootstrap command), and see the real
// tasks - with the ls-remote issued exactly once across repeated opens.
func TestOpenTasks_CloneAdopts(t *testing.T) {
	origin := newGitModeOrigin(t, "lives in git mode", "a second task")
	dir := cloneDir(t, origin)
	if refs, _ := probeInitState(&ExecGit{Dir: dir}); len(refs) != 0 {
		t.Fatalf("precondition: a fresh clone should have no local refs/meads/*, got %v", refs)
	}

	git := &lsRemoteCounter{Git: &ExecGit{Dir: dir}}
	tasks, err := OpenTasksGit(dir, git)
	if err != nil {
		t.Fatalf("OpenTasksGit: %v", err)
	}
	if tasks.Backend() != BackendGit {
		t.Fatalf("Backend() after adopt = %v, want BackendGit", tasks.Backend())
	}
	got, err := tasks.Get(nil)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("Get(nil) after adopt = %v, want the origin's 2 tasks", got)
	}
	if git.count() != 1 {
		t.Errorf("ls-remote calls on the adopting open = %d, want exactly 1", git.count())
	}
	// The marker must NOT be written on the adopt branch: the local refs
	// now existing is itself the terminal state.
	if _, initChecked := probeInitState(&ExecGit{Dir: dir}); initChecked {
		t.Errorf("%s must not exist after an adopt", InitCheckRef)
	}
	// The ordinary fetch refspec is configured for later fetches.
	if out := runGit(t, dir, "config", "--get-all", "remote.origin.fetch"); !strings.Contains(out, FetchRefspec) {
		t.Errorf("remote.origin.fetch = %q, want it to include %q", out, FetchRefspec)
	}

	// Second open: local refs exist, so no ls-remote repeats.
	tasks2, err := OpenTasksGit(dir, git)
	if err != nil {
		t.Fatalf("OpenTasksGit (2nd): %v", err)
	}
	if tasks2.Backend() != BackendGit {
		t.Errorf("Backend() on 2nd open = %v, want BackendGit", tasks2.Backend())
	}
	if git.count() != 1 {
		t.Errorf("ls-remote calls after 2nd open = %d, want still 1 (one-shot)", git.count())
	}

	// A new task continues from the origin's ids (no collision), and pushes
	// cleanly to the shared namespace.
	id, err := tasks2.Add(Task{Title: "teammate adds one", Status: "open"})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if id != 3 {
		t.Errorf("Add id in an adopted clone = %d, want 3 (continuing origin's ids)", id)
	}
	if err := tasks2.Sync(t.Context()); err != nil {
		t.Fatalf("Sync (push refs/meads/*) after adopt: %v", err)
	}
	if out, _ := (&ExecGit{Dir: origin}).Output("for-each-ref", "--format=%(refname)", TasksRefPrefix); strings.Count(out, "\n")+1 != 3 {
		t.Errorf("origin task refs after push = %q, want 3", out)
	}
	// The marker is never pushed along with the namespace.
	if out, _ := (&ExecGit{Dir: origin}).Output("for-each-ref", "--format=%(refname)", InitCheckRef); out != "" {
		t.Errorf("origin must never hold %s, got %q", InitCheckRef, out)
	}
}

// TestOpenTasks_CloneOfFileModeRepo: origin has no refs/meads/* at all, so
// the clone resolves to file mode, writes the marker, and never asks again.
func TestOpenTasks_CloneOfFileModeRepo(t *testing.T) {
	// A plain repo with an ordinary commit and no meads state anywhere.
	src := newDetectRepo(t)
	if err := os.WriteFile(filepath.Join(src, "f.txt"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	runGit(t, src, "add", ".")
	runGit(t, src, "commit", "-m", "initial")
	bare := t.TempDir()
	runGit(t, bare, "init", "--bare", "-b", "main")
	runGit(t, src, "remote", "add", "origin", bare)
	runGit(t, src, "push", "origin", "main")
	dir := cloneDir(t, bare)

	git := &lsRemoteCounter{Git: &ExecGit{Dir: dir}}
	tasks, err := OpenTasksGit(dir, git)
	if err != nil {
		t.Fatalf("OpenTasksGit: %v", err)
	}
	if tasks.Backend() != BackendMarkdown {
		t.Errorf("Backend() = %v, want BackendMarkdown", tasks.Backend())
	}
	if git.count() != 1 {
		t.Errorf("ls-remote calls = %d, want exactly 1", git.count())
	}
	if _, initChecked := probeInitState(&ExecGit{Dir: dir}); !initChecked {
		t.Errorf("%s should have been written after origin answered empty", InitCheckRef)
	}
	// The marker records the origin URL that was checked.
	if out := runGit(t, dir, "cat-file", "-p", InitCheckRef); strings.TrimSpace(out) != bare {
		t.Errorf("marker content = %q, want the origin URL %q", out, bare)
	}

	// Second open: the marker short-circuits before any network call.
	if _, err := OpenTasksGit(dir, git); err != nil {
		t.Fatalf("OpenTasksGit (2nd): %v", err)
	}
	if git.count() != 1 {
		t.Errorf("ls-remote calls after 2nd open = %d, want still 1 (marker short-circuit)", git.count())
	}

	// Add works in file mode, creating TASKS.md as today.
	if _, err := tasks.Add(Task{Title: "ordinary file task"}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "TASKS.md")); err != nil {
		t.Errorf("TASKS.md should exist after Add in file mode: %v", err)
	}
}

// TestOpenTasks_NoOrigin: a repo with no remote behaves exactly as today -
// file mode, no network call possible, marker written so even the (local)
// get-url check short-circuits to a plain probe next time.
func TestOpenTasks_NoOrigin(t *testing.T) {
	dir := newDetectRepo(t) // no remote at all
	git := &lsRemoteCounter{Git: &ExecGit{Dir: dir}}
	tasks, err := OpenTasksGit(dir, git)
	if err != nil {
		t.Fatalf("OpenTasksGit: %v", err)
	}
	if tasks.Backend() != BackendMarkdown {
		t.Errorf("Backend() = %v, want BackendMarkdown", tasks.Backend())
	}
	if git.count() != 0 {
		t.Errorf("ls-remote calls with no origin = %d, want 0", git.count())
	}
	if _, initChecked := probeInitState(&ExecGit{Dir: dir}); !initChecked {
		t.Errorf("%s should have been written for the no-origin case", InitCheckRef)
	}
}

// TestOpenTasks_ExistingTasksFileNeverAsks: an existing TASKS.md is
// unambiguous file mode - even when origin HAS refs/meads/* - so no
// ls-remote is issued.
func TestOpenTasks_ExistingTasksFileNeverAsks(t *testing.T) {
	origin := newGitModeOrigin(t, "git task")
	dir := cloneDir(t, origin)
	if err := os.WriteFile(filepath.Join(dir, "TASKS.md"), []byte("# Tasks\n"), 0644); err != nil {
		t.Fatal(err)
	}
	git := &lsRemoteCounter{Git: &ExecGit{Dir: dir}}
	tasks, err := OpenTasksGit(dir, git)
	if err != nil {
		t.Fatalf("OpenTasksGit: %v", err)
	}
	if tasks.Backend() != BackendMarkdown {
		t.Errorf("Backend() = %v, want BackendMarkdown (TASKS.md is unambiguous)", tasks.Backend())
	}
	if git.count() != 0 {
		t.Errorf("ls-remote calls with an existing TASKS.md = %d, want 0", git.count())
	}
}

// TestDetect_DoesNotAdopt pins the design's load-bearing split: Detect must
// stay pure and offline (rais calls it on a ticker), so it reports
// markdown in a fresh clone and - critically - fetches nothing.
func TestDetect_DoesNotAdopt(t *testing.T) {
	origin := newGitModeOrigin(t, "git task")
	dir := cloneDir(t, origin)
	b, err := Detect(dir)
	if err != nil || b != BackendMarkdown {
		t.Errorf("Detect(fresh clone of git-mode repo) = %v, %v; want BackendMarkdown, nil (no adoption here)", b, err)
	}
	if refs, _ := probeInitState(&ExecGit{Dir: dir}); len(refs) != 0 {
		t.Errorf("Detect fetched refs it must never fetch: %v", refs)
	}
}

// TestInitTasks_CloneAdopts: `md init --git` in a fresh clone of a git-mode
// repo must adopt origin's refs, not seed an unrelated fresh config ref
// (which the next push would reject non-fast-forward).
func TestInitTasks_CloneAdopts(t *testing.T) {
	origin := newGitModeOrigin(t, "one", "two", "three")
	dir := cloneDir(t, origin)

	res, err := InitTasks(dir, BackendGit)
	if err != nil {
		t.Fatalf("InitTasks: %v", err)
	}
	if res.AdoptedTasks != 3 {
		t.Errorf("AdoptedTasks = %d, want 3", res.AdoptedTasks)
	}
	if res.FetchRefspec != FetchRefspecAdded {
		t.Errorf("FetchRefspec = %v, want FetchRefspecAdded", res.FetchRefspec)
	}
	got, err := res.Tasks.Get(nil)
	if err != nil || len(got) != 3 {
		t.Fatalf("Get(nil) after adopt = %v, %v; want the origin's 3 tasks", got, err)
	}
	// The adopted config came from origin; a second init still refuses.
	if _, err := InitTasks(dir, BackendGit); err == nil {
		t.Error("second InitTasks after adopt should refuse (already initialized), got nil")
	}
	// And the push path is clean: no non-fast-forward rejection.
	if err := res.Tasks.Sync(t.Context()); err != nil {
		t.Errorf("Sync after adopt should push cleanly, got: %v", err)
	}
}

// TestProbeInitState_Combined pins the single-process probe shape: one
// for-each-ref sees both namespaces, and the marker is not confused for a
// meads ref (git matches wildcard-free patterns only at "/" boundaries).
func TestProbeInitState_Combined(t *testing.T) {
	dir := newDetectRepo(t)
	git := &ExecGit{Dir: dir}
	gs := NewGitStore(git)
	if err := gs.SetConfig(DefaultConfig()); err != nil {
		t.Fatalf("SetConfig: %v", err)
	}
	writeInitCheck(git, "https://example.invalid/repo.git")

	refs, initChecked := probeInitState(git)
	if !initChecked {
		t.Errorf("probeInitState should see %s", InitCheckRef)
	}
	if len(refs) != 1 || refs[0] != ConfigRef {
		t.Errorf("probeInitState meadsRefs = %v, want exactly [%s] (marker excluded)", refs, ConfigRef)
	}
}
