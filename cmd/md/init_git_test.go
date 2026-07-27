package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jpillora/meads/pkg/meads"
)

// Tests for `md init --git` (git mode phase 5, TASKS #62): initializing git
// mode in the current repo, the fetch refspec it configures on origin, and -
// critically - that it never disturbs ordinary `git push` behaviour. Like
// harness_test.go's other integration tests, these run against real
// temporary git repos under t.TempDir() rather than fakes.

// --- helpers ---

// gitConfigGetAll runs `git config --get-all key` in dir and reports whether
// the key is set at all, along with its values if so. Used to assert
// remote.origin.push is never configured by init.
func gitConfigGetAll(t *testing.T, dir, key string) (values []string, isSet bool) {
	t.Helper()
	cmd := exec.Command("git", "config", "--get-all", key)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return nil, false
	}
	trimmed := strings.TrimSpace(string(out))
	if trimmed == "" {
		return nil, true
	}
	return strings.Split(trimmed, "\n"), true
}

// runGit runs a git command in dir and fails the test on error - a
// free-function equivalent of testHarness.git for use where a harness isn't
// (yet) available, e.g. constructing a bare repo before any globals exist.
func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s (in %s) failed: %v\n%s", strings.Join(args, " "), dir, err, out)
	}
	return strings.TrimSpace(string(out))
}

// writeFile writes content to name under dir, failing the test on error.
func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0644); err != nil {
		t.Fatalf("writing %s: %v", name, err)
	}
}

// remoteRefNames lists every ref name present in the repo at dir (used
// against the bare "remote" repo to see exactly what a push updated).
func remoteRefNames(t *testing.T, dir string) []string {
	t.Helper()
	cmd := exec.Command("git", "for-each-ref", "--format=%(refname)")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("for-each-ref in %s: %v", dir, err)
	}
	trimmed := strings.TrimSpace(string(out))
	if trimmed == "" {
		return nil
	}
	return strings.Split(trimmed, "\n")
}

// --- init --git: basic lifecycle ---

func TestIntegration_InitGit_FreshRepo(t *testing.T) {
	h := newHarness(t)

	if err := (&initCmd{globals: h.globals, Git: true}).Run(); err != nil {
		t.Fatalf("init --git: %v", err)
	}

	// A default config ref must exist (see init.go's runGit doc comment):
	// it's what makes a second init detectably "already initialized" even
	// before any task is created.
	gs := meads.NewGitStore(h.globals.git())
	cfg, err := gs.Config()
	if err != nil {
		t.Fatalf("Config() after init --git: %v", err)
	}
	if want := meads.DefaultConfig(); cfg != want {
		t.Errorf("Config() after init --git = %+v, want defaults %+v", cfg, want)
	}

	// No placeholder task: an empty task set is just "no task refs".
	tasks, err := gs.Get(nil)
	if err != nil {
		t.Fatalf("Get(nil) after init --git: %v", err)
	}
	if len(tasks) != 0 {
		t.Errorf("Get(nil) after init --git = %v, want none (init must not seed a placeholder task)", tasks)
	}
}

func TestIntegration_InitGit_TwiceErrors(t *testing.T) {
	h := newHarness(t)

	if err := (&initCmd{globals: h.globals, Git: true}).Run(); err != nil {
		t.Fatalf("first init --git: %v", err)
	}
	err := (&initCmd{globals: h.globals, Git: true}).Run()
	if err == nil {
		t.Fatal("second init --git should error, got nil")
	}
	if !strings.Contains(err.Error(), "already initialized") {
		t.Errorf("second init --git error = %q, want it to mention \"already initialized\"", err.Error())
	}
}

// A repo with a config ref but zero tasks must still be detected as
// "already initialized" - this is the whole reason init writes a default
// config ref rather than nothing (see init.go's runGit doc comment): the
// task-only detection rule (globals.gitTaskRefsExist) alone can't tell this
// state apart from "never initialized".
func TestIntegration_InitGit_TwiceErrors_EvenWithZeroTasks(t *testing.T) {
	h := newHarness(t)
	if err := (&initCmd{globals: h.globals, Git: true}).Run(); err != nil {
		t.Fatalf("init --git: %v", err)
	}
	gs := meads.NewGitStore(h.globals.git())
	if tasks, err := gs.Get(nil); err != nil || len(tasks) != 0 {
		t.Fatalf("precondition: expected zero tasks after init, got %v, err=%v", tasks, err)
	}
	err := (&initCmd{globals: h.globals, Git: true}).Run()
	if err == nil {
		t.Fatal("second init --git with zero tasks created should still error, got nil")
	}
}

func TestIntegration_InitGit_OutsideGitRepoErrors(t *testing.T) {
	dir := t.TempDir() // deliberately never `git init`-ed
	g := &globals{
		Git: &meads.ExecGit{Dir: dir},
		Dir: dir,
	}
	err := (&initCmd{globals: g, Git: true}).Run()
	if err == nil {
		t.Fatal("init --git outside a git repository should error, got nil")
	}
	if !strings.Contains(err.Error(), "not in a git repository") {
		t.Errorf("error = %q, want it to mention \"not in a git repository\"", err.Error())
	}
}

func TestIntegration_InitGit_NoOriginRemote_SkipsRefspecCleanly(t *testing.T) {
	dir := t.TempDir()
	runGit(t, dir, "init", "-b", "main")
	runGit(t, dir, "config", "user.name", "Test")
	runGit(t, dir, "config", "user.email", "test@test.com")
	g := &globals{
		Git: &meads.ExecGit{Dir: dir},
		Dir: dir,
	}
	// No `git remote add origin` at all - init must not fail because of it.
	if err := (&initCmd{globals: g, Git: true}).Run(); err != nil {
		t.Fatalf("init --git with no origin remote should succeed, got: %v", err)
	}
}

// --- fetch refspec: set, additive, idempotent ---

func TestIntegration_InitGit_SetsFetchRefspec(t *testing.T) {
	h := newHarness(t) // origin is already configured (bare clone, see harness_test.go)

	// Baseline: the default fetch refspec git wrote when origin was added.
	before, ok := gitConfigGetAll(t, h.dir, "remote.origin.fetch")
	if !ok || len(before) == 0 {
		t.Fatalf("precondition: expected an existing default fetch refspec, got %v", before)
	}

	if err := (&initCmd{globals: h.globals, Git: true}).Run(); err != nil {
		t.Fatalf("init --git: %v", err)
	}

	after, ok := gitConfigGetAll(t, h.dir, "remote.origin.fetch")
	if !ok {
		t.Fatal("remote.origin.fetch should still be set after init --git")
	}
	if len(after) != len(before)+1 {
		t.Fatalf("remote.origin.fetch entries = %v, want the original %v plus exactly one more", after, before)
	}
	found := false
	for _, line := range after {
		if line == meads.FetchRefspec {
			found = true
		}
	}
	if !found {
		t.Errorf("remote.origin.fetch = %v, want it to include %q", after, meads.FetchRefspec)
	}
	// The pre-existing default refspec must survive untouched (additive,
	// never replacing).
	for _, line := range before {
		if !strings.Contains(strings.Join(after, "\n"), line) {
			t.Errorf("original fetch refspec %q was lost; after = %v", line, after)
		}
	}
}

func TestIntegration_InitGit_FetchRefspecNotDuplicated(t *testing.T) {
	h := newHarness(t)

	// Call the idempotent helper directly, twice - a second full `md init
	// --git` would be rejected by the already-initialized check before ever
	// reaching this step, so this is the right granularity to prove
	// idempotency at.
	outcome, err := meads.EnsureFetchRefspec(h.globals.git())
	if err != nil {
		t.Fatalf("EnsureFetchRefspec (1st): %v", err)
	}
	if outcome != meads.FetchRefspecAdded {
		t.Errorf("EnsureFetchRefspec (1st) = %v, want FetchRefspecAdded", outcome)
	}
	firstRun, _ := gitConfigGetAll(t, h.dir, "remote.origin.fetch")
	countMeads := func(lines []string) int {
		n := 0
		for _, l := range lines {
			if l == meads.FetchRefspec {
				n++
			}
		}
		return n
	}
	if n := countMeads(firstRun); n != 1 {
		t.Fatalf("after first EnsureFetchRefspec, meads refspec appears %d times, want 1 (%v)", n, firstRun)
	}

	outcome, err = meads.EnsureFetchRefspec(h.globals.git())
	if err != nil {
		t.Fatalf("EnsureFetchRefspec (2nd): %v", err)
	}
	if outcome != meads.FetchRefspecAlreadyPresent {
		t.Errorf("EnsureFetchRefspec (2nd) = %v, want FetchRefspecAlreadyPresent", outcome)
	}
	secondRun, _ := gitConfigGetAll(t, h.dir, "remote.origin.fetch")
	if n := countMeads(secondRun); n != 1 {
		t.Fatalf("after a second EnsureFetchRefspec, meads refspec appears %d times, want still 1 (%v)", n, secondRun)
	}
	if len(secondRun) != len(firstRun) {
		t.Fatalf("remote.origin.fetch grew from %v to %v on a repeat call, want unchanged", firstRun, secondRun)
	}
}

// --- THE regression test ---
//
// This is the single most important test in this phase. Configuring ANY
// push refspec on a remote (e.g. accidentally via `git config
// remote.origin.push ...`) replaces git's default matching/simple push
// behaviour and silently breaks ordinary `git push` for the user - for
// every branch, forever, not just refs/meads/*. `md init --git` must never
// do this. Proven two ways: (1) directly, remote.origin.push must remain
// completely unset after init; (2) behaviourally, a plain `git push` of a
// branch - both an explicit `git push origin <branch>` and a bare `git
// push` relying on upstream tracking - must update exactly the one ref it
// would have updated before init, on a real bare "remote" repo under
// t.TempDir(), before and after init --git.
func TestIntegration_InitGit_DoesNotBreakNormalPush(t *testing.T) {
	h := newHarness(t) // working clone + bare "origin" remote, both under t.TempDir()
	originDir := h.git("remote", "get-url", "origin")

	writeAndCommit := func(name, content, msg string) {
		t.Helper()
		writeFile(t, h.dir, name, content)
		h.git("add", name)
		h.git("commit", "-m", msg)
	}

	// --- Baseline: push behaviour BEFORE md init --git ---

	refsBefore := remoteRefNames(t, originDir)

	h.branch("feature/before")
	h.checkout("feature/before")
	writeAndCommit("before.txt", "before", "before commit")
	h.git("push", "origin", "feature/before") // explicit refspec, new branch

	refsAfterExplicitPushBefore := remoteRefNames(t, originDir)

	h.checkout("main")
	writeAndCommit("main-before.txt", "main before", "main commit before init")
	h.git("push") // bare push, relying on upstream tracking (push.default)

	refsAfterBarePushBefore := remoteRefNames(t, originDir)
	// Not just that refs/heads/main is still present (it was, already, before
	// this push) but that the bare push actually moved it to the new commit -
	// a leaked push refspec that replaces main's ref with something else
	// entirely (e.g. only refs/meads/*) would leave the ref name listed but
	// stale, which a ref-name-set diff alone would not catch.
	assertRemoteMainMatchesLocal(t, h.dir, originDir, "after the pre-init bare push")

	// --- Initialize git mode ---

	if err := (&initCmd{globals: h.globals, Git: true}).Run(); err != nil {
		t.Fatalf("init --git: %v", err)
	}

	// The critical invariant, checked directly: init must never configure a
	// push refspec.
	if values, isSet := gitConfigGetAll(t, h.dir, "remote.origin.push"); isSet {
		t.Fatalf("remote.origin.push must never be set by md init --git, but found %v", values)
	}

	// --- Same two push forms AFTER init --git ---

	h.checkout("feature/before")
	h.branch("feature/after")
	h.checkout("feature/after")
	writeAndCommit("after.txt", "after", "after commit")
	h.git("push", "origin", "feature/after") // explicit refspec, new branch

	refsAfterExplicitPushAfter := remoteRefNames(t, originDir)

	h.checkout("main")
	writeAndCommit("main-after.txt", "main after", "main commit after init")
	h.git("push") // bare push

	refsAfterBarePushAfter := remoteRefNames(t, originDir)
	assertRemoteMainMatchesLocal(t, h.dir, originDir, "after the post-init bare push")

	// --- Assertions: each push updates exactly the ref it targeted, both
	// before and after init, and refs/meads/* never appears on the remote
	// (a plain push has no reason to touch it: no push refspec is ever
	// configured, and pushing is not part of what init does). ---

	// wantNewRef == "" means the push must add NO new ref name (it only
	// moves an existing one, e.g. main's second commit).
	assertOnlyNewRef := func(before, after []string, wantNewRef string) {
		t.Helper()
		beforeSet := map[string]bool{}
		for _, r := range before {
			beforeSet[r] = true
		}
		var added []string
		for _, r := range after {
			if !beforeSet[r] {
				added = append(added, r)
			}
		}
		if wantNewRef == "" {
			if len(added) != 0 {
				t.Errorf("push added unexpected new refs %v, want none", added)
			}
			return
		}
		if len(added) != 1 || added[0] != wantNewRef {
			t.Errorf("push added refs %v, want exactly [%s]", added, wantNewRef)
		}
	}

	assertOnlyNewRef(refsBefore, refsAfterExplicitPushBefore, "refs/heads/feature/before")
	assertOnlyNewRef(refsAfterExplicitPushBefore, refsAfterBarePushBefore, "" /* main already existed */)
	assertOnlyNewRef(refsAfterBarePushBefore, refsAfterExplicitPushAfter, "refs/heads/feature/after")
	assertOnlyNewRef(refsAfterExplicitPushAfter, refsAfterBarePushAfter, "" /* main already existed */)

	for _, refs := range [][]string{refsAfterExplicitPushBefore, refsAfterBarePushBefore, refsAfterExplicitPushAfter, refsAfterBarePushAfter} {
		for _, r := range refs {
			if strings.HasPrefix(r, "refs/meads/") {
				t.Fatalf("remote unexpectedly has a refs/meads/* ref (%s) after a plain git push - a push refspec must have leaked in: %v", r, refs)
			}
		}
	}

	// main's bare `git push` must have moved the SAME ref both times (a
	// push refspec bug could instead silently push every branch, or none).
	if !containsRef(refsAfterBarePushBefore, "refs/heads/main") || !containsRef(refsAfterBarePushAfter, "refs/heads/main") {
		t.Fatalf("refs/heads/main missing from remote: before=%v after=%v", refsAfterBarePushBefore, refsAfterBarePushAfter)
	}
}

// assertRemoteMainMatchesLocal fails the test unless the remote's
// refs/heads/main points at the same commit as the local repo's current
// HEAD - i.e. the most recent bare `git push` actually landed, not just
// that the ref name happens to still be present on the remote.
func assertRemoteMainMatchesLocal(t *testing.T, localDir, remoteDir, when string) {
	t.Helper()
	local := runGit(t, localDir, "rev-parse", "HEAD")
	remote := runGit(t, remoteDir, "rev-parse", "refs/heads/main")
	if local != remote {
		t.Fatalf("remote refs/heads/main = %s, local HEAD = %s (%s) - the push did not land as expected", remote, local, when)
	}
}

func containsRef(refs []string, want string) bool {
	for _, r := range refs {
		if r == want {
			return true
		}
	}
	return false
}

// The bootstrap path: `md init --git` then `md add`, with NO forced mode and
// no pre-seeded task refs — exactly what a real user does.
//
// This is the regression guard for a bug that every other test missed:
// detection originally keyed on refs/meads/tasks/* alone, but a freshly
// initialised repo has no tasks yet, so the first add resolved to file mode
// and wrote TASKS.md into the working tree. Git mode could never bootstrap.
// Detection therefore keys on the whole refs/meads/ namespace, which
// init --git populates via the config ref.
//
// Every assertion here is on observable end state, deliberately not on
// mode() — testing the resolver directly is what let the bug through.
func TestIntegration_InitGit_ThenAdd_BootstrapsIntoGitMode(t *testing.T) {
	h := newHarness(t)
	t.Chdir(h.dir)
	// bare relative default + no --git: force nothing, detect everything
	h.globals.TasksFile = "TASKS.md"
	h.globals.GitMode = false

	if err := (&initCmd{globals: h.globals, Git: true}).Run(); err != nil {
		t.Fatalf("init --git: %v", err)
	}
	// drop any cached mode resolved before init created the namespace
	h.globals.Store = nil

	if err := (&addCmd{globals: h.globals, Args: []string{"first task"}}).Run(); err != nil {
		t.Fatalf("add: %v", err)
	}

	// the task must exist as a ref...
	gs := meads.NewGitStore(h.globals.git())
	tasks, err := gs.Get(nil)
	if err != nil {
		t.Fatalf("GitStore.Get: %v", err)
	}
	if len(tasks) != 1 || tasks[0].Title != "first task" {
		t.Fatalf("git store tasks = %+v, want exactly the added task", tasks)
	}
	// ...and NOT as a working-tree file
	if _, err := os.Stat(filepath.Join(h.dir, "TASKS.md")); !os.IsNotExist(err) {
		t.Fatalf("TASKS.md exists after add in git mode (err=%v); the add fell back to the file backend", err)
	}
}
