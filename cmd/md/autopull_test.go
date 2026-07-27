package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jpillora/meads/pkg/meads"
)

// Tests for task 86's auto-pull: once per pushInterval, a mutation PULLS
// (fetch + integrate) before it pushes - adopting other clones' tasks,
// fast-forwarding unmoved ones, and re-homing contended local tasks at
// fresh ids so the push converges. The pkg-level mechanics live in
// pkg/meads/gitpull_test.go and gitdoctor_test.go; these prove the CLI
// mutation commands actually drive the full pull-then-push round trip.

// setupSyncClones creates a bare origin and two git-mode clones of it,
// seeded with one shared task created by clone1 and adopted by clone2 (via
// `md init --git`'s adopt branch). Both clones get a zero push interval, so
// every mutation syncs immediately. Returns both clones' globals and the
// bare origin's dir.
func setupSyncClones(t *testing.T) (g1, g2 *globals, bareDir string) {
	t.Helper()
	bareDir = t.TempDir()
	runGit(t, bareDir, "init", "--bare", "-b", "main")

	mkClone := func(name string) *globals {
		dir := filepath.Join(t.TempDir(), name)
		runGit(t, "", "clone", bareDir, dir)
		runGit(t, dir, "config", "user.name", name)
		runGit(t, dir, "config", "user.email", name+"@test.com")
		return &globals{Git: &meads.ExecGit{Dir: dir}, Dir: dir, TasksFile: "TASKS.md", GitMode: true}
	}
	g1, g2 = mkClone("clone1"), mkClone("clone2")

	if err := (&initCmd{globals: g1, Git: true}).Run(); err != nil {
		t.Fatalf("init --git (clone1): %v", err)
	}
	if err := (&addCmd{globals: g1, Args: []string{"shared task"}}).Run(); err != nil {
		t.Fatalf("add (clone1): %v", err)
	}
	g1.git().Run("push", "origin", meads.RefNamespace+"*:"+meads.RefNamespace+"*")
	if err := (&initCmd{globals: g2, Git: true}).Run(); err != nil {
		t.Fatalf("init --git (clone2): %v", err)
	}
	// Sanity: clone2 adopted the shared task.
	gs2 := meads.NewGitStore(g2.git())
	if all, err := gs2.Get(nil); err != nil || len(all) != 1 {
		t.Fatalf("clone2 tasks after adopt = %v, %v; want the shared task", all, err)
	}
	// Zero-length interval: every mutation is due for a sync.
	for _, gs := range []*meads.GitStore{meads.NewGitStore(g1.git()), gs2} {
		if err := gs.SetConfig(meads.Config{PushInterval: "0s"}); err != nil {
			t.Fatalf("SetConfig: %v", err)
		}
	}
	g1.git().Run("push", "origin", meads.RefNamespace+"*:"+meads.RefNamespace+"*")
	return g1, g2, bareDir
}

// TestAutoPull_ImportsOtherClonesTasks: a mutation whose own changes don't
// collide still pulls first - tasks another clone pushed since the last
// sync arrive locally before the push goes out.
func TestAutoPull_ImportsOtherClonesTasks(t *testing.T) {
	g1, g2, bareDir := setupSyncClones(t)

	// clone1 creates and pushes task 2, unknown to clone2.
	if err := (&addCmd{globals: g1, Args: []string{"clone1's second"}}).Run(); err != nil {
		t.Fatalf("add (clone1): %v", err)
	}
	g1.git().Run("push", "origin", meads.RefNamespace+"*:"+meads.RefNamespace+"*")

	// clone2 mutates its own task 1 (no id allocation, no collision): the
	// auto-pull must import clone1's task 2 and then push clone2's update.
	stderr := captureStderr(t, func() {
		if err := (&updateCmd{globals: g2, ID: "1", Title: "shared task (clone2's edit)"}).Run(); err != nil {
			t.Fatalf("update (clone2): %v", err)
		}
	})
	if !strings.Contains(stderr, "pulled 1 new task(s) from origin (ids 2)") {
		t.Errorf("stderr = %q, want the import summary for task 2", stderr)
	}

	gs2 := meads.NewGitStore(g2.git())
	all, err := gs2.Get(nil)
	if err != nil || len(all) != 2 {
		t.Fatalf("clone2 tasks after the sync = %v, %v; want 2", all, err)
	}
	if all[1].ID != 2 || all[1].Title != "clone1's second" {
		t.Errorf("clone2 task 2 = %+v, want clone1's imported task", all[1])
	}
	// And clone2's own edit went out with the same sync (asserted on the
	// bare origin itself, not clone1's not-yet-synced copy).
	if out, _ := (&meads.ExecGit{Dir: bareDir}).Output("show", meads.TasksRefPrefix+"1:"+meads.TaskFileName); !strings.Contains(out, "clone2's edit") {
		t.Errorf("origin's task 1 = %q, want clone2's edit (pushed in the same sync)", out)
	}
}

// TestAutoPull_ContentionReHomedThenPushes is the headline case: clone2
// allocates the SAME id clone1 already pushed; the auto-pull detects the
// contention, re-homes clone2's task at a fresh id, resets the contended
// ref to origin's version, and the push then converges - reported on the
// very command that caused it.
func TestAutoPull_ContentionReHomedThenPushes(t *testing.T) {
	g1, g2, bareDir := setupSyncClones(t)

	// clone1 creates and pushes task 2, unknown to clone2.
	if err := (&addCmd{globals: g1, Args: []string{"clone1's second"}}).Run(); err != nil {
		t.Fatalf("add (clone1): %v", err)
	}
	g1.git().Run("push", "origin", meads.RefNamespace+"*:"+meads.RefNamespace+"*")

	// clone2's add computes NextID = 2 locally - the same id clone1 just
	// pushed. The auto-pull must re-home clone2's task at 3.
	stderr := captureStderr(t, func() {
		if err := (&addCmd{globals: g2, Args: []string{"clone2's second"}}).Run(); err != nil {
			t.Fatalf("add (clone2): %v", err)
		}
	})
	if !strings.Contains(stderr, "task 2 collided with a different task on origin; your version moved to task 3") {
		t.Errorf("stderr = %q, want the contention re-homing notice", stderr)
	}

	// clone2's final state: id 2 holds origin's version, id 3 holds clone2's.
	gs2 := meads.NewGitStore(g2.git())
	all, err := gs2.Get(nil)
	if err != nil || len(all) != 3 {
		t.Fatalf("clone2 tasks = %v, %v; want 3", all, err)
	}
	byID := map[int]string{}
	for _, task := range all {
		byID[task.ID] = task.Title
	}
	if byID[2] != "clone1's second" || byID[3] != "clone2's second" {
		t.Errorf("clone2 tasks = %v, want 2=clone1's second, 3=clone2's second", all)
	}

	// The push converged: origin holds all three tasks.
	out, _ := (&meads.ExecGit{Dir: bareDir}).Output("for-each-ref", "--format=%(refname)", meads.TasksRefPrefix)
	if got := len(strings.Split(strings.TrimSpace(out), "\n")); got != 3 {
		t.Errorf("origin task refs = %q, want 3", out)
	}

	// clone1's next sync adopts clone2's re-homed task - no duplication.
	if err := (&updateCmd{globals: g1, ID: "1", Title: "shared task (clone1's edit)"}).Run(); err != nil {
		t.Fatalf("update (clone1): %v", err)
	}
	gs1 := meads.NewGitStore(g1.git())
	all1, err := gs1.Get(nil)
	if err != nil || len(all1) != 3 {
		t.Fatalf("clone1 tasks after its sync = %v, %v; want 3 (no duplication)", all1, err)
	}
	byID1 := map[int]string{}
	for _, task := range all1 {
		byID1[task.ID] = task.Title
	}
	if byID1[3] != "clone2's second" {
		t.Errorf("clone1 task 3 = %q, want clone2's re-homed task", byID1[3])
	}
}

// TestAutoPull_NoOriginSkipsPull: the no-origin path is unchanged - a
// mutation succeeds and nothing is fetched or pushed.
func TestAutoPull_NoOriginSkipsPull(t *testing.T) {
	h := gitModeHarness(t)
	h.git("remote", "remove", "origin")
	if err := (&addCmd{globals: h.globals, Args: []string{"offline task"}}).Run(); err != nil {
		t.Fatalf("add with no origin should succeed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(h.dir, "TASKS.md")); !os.IsNotExist(err) {
		t.Errorf("TASKS.md must not appear in git mode (stat err=%v)", err)
	}
}
