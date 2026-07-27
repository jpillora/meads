package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/jpillora/meads/pkg/meads"
)

// captureStdout runs fn with os.Stdout redirected and returns what it printed.
func captureStdout(t *testing.T, fn func() error) (string, error) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	old := os.Stdout
	os.Stdout = w
	runErr := fn()
	w.Close()
	os.Stdout = old
	buf := make([]byte, 0, 1024)
	tmp := make([]byte, 512)
	for {
		n, readErr := r.Read(tmp)
		buf = append(buf, tmp[:n]...)
		if readErr != nil {
			break
		}
	}
	return string(buf), runErr
}

// TestDoctor_DetectsExistingCycle covers the "warn on existing circular graphs"
// case: a cycle that prevention can't catch because each edit was valid alone
// (here simulated by editing the file directly, as a git merge would produce).
func TestDoctor_DetectsExistingCycle(t *testing.T) {
	h := newHarness(t)
	h.addTask("A") // id 1
	h.addTask("B") // id 2
	h.addDep(2, 1) // 2 → 1 (valid)

	// Inject the back-edge 1 → 2 directly, forming a 1 ↔ 2 cycle that no single
	// md command could have created. The first status line belongs to task 1.
	content := h.tasksFileContent()
	content = strings.Replace(content, "* status: open\n", "* status: open\n* depends-on: 2\n", 1)
	if err := os.WriteFile(h.globals.TasksFile, []byte(content), 0644); err != nil {
		t.Fatalf("inject cycle: %v", err)
	}

	out, err := captureStdout(t, (&doctorCmd{globals: h.globals}).Run)
	if err == nil {
		t.Fatal("doctor should exit non-zero when an unfixable cycle remains")
	}
	if !strings.Contains(err.Error(), "circular") {
		t.Errorf("error should mention the circular dependency, got %v", err)
	}
	if !strings.Contains(out, "Circular dependency detected") {
		t.Errorf("doctor should report the cycle on stdout, got %q", out)
	}
	if !strings.Contains(out, "1") || !strings.Contains(out, "2") {
		t.Errorf("reported cycle should name tasks 1 and 2, got %q", out)
	}
}

func TestDoctor_NoCycle_NoIssues(t *testing.T) {
	h := newHarness(t)
	h.addTask("A") // id 1
	h.addTask("B") // id 2
	h.addDep(2, 1) // valid chain, no cycle

	out, err := captureStdout(t, (&doctorCmd{globals: h.globals}).Run)
	if err != nil {
		t.Fatalf("doctor on a clean file should not error, got %v", err)
	}
	if !strings.Contains(out, "no issues found") {
		t.Errorf("expected 'no issues found', got %q", out)
	}
}

// --- git mode: `md doctor` resolving a divergence (task 65 phase 8, task 86) ---

// setupDoctorDivergenceClones creates a bare "remote" under t.TempDir() and
// two independent git-mode clones of it, BOTH running `md init --git` - so
// both configure the safe fetch refspec (meads.FetchRefspec). That is the
// one important difference from push_test.go's setupDivergedGitModeClones,
// which only needs clone1's fetch refspec since it exists to test PUSH
// rejection, never refs/meads-remote/*; a fresh, self-contained helper here
// avoids coupling this test's needs onto that one (and risking its
// already-passing phase 6 coverage).
//
// It diverges one shared task between the two clones exactly like that
// helper does, then has clone2 run a REAL plain `git fetch origin` (relying
// entirely on the configured refspec, exactly like an actual user) so
// refs/meads-remote/* is populated for `md doctor` to read. Returns
// clone2's globals (the one with a genuine divergence to resolve) and the
// shared task's id.
func setupDoctorDivergenceClones(t *testing.T) (g2 *globals, sharedID int) {
	t.Helper()
	// Suppress BOTH network halves of autoPush for setup: the push (see
	// setupDivergedGitModeClones's identical comment) AND the fetch - since
	// task 86, autoPush pulls first, and a real fetch+Integrate here would
	// resolve the divergence at update time, before `md doctor` ever runs
	// (that auto-resolution is the feature; this test needs the raw
	// divergence to survive until doctor).
	realPushFunc, realFetchFunc := pushFunc, fetchFunc
	pushFunc = func(ctx context.Context, dir string) (string, error) { return "", nil }
	fetchFunc = func(ctx context.Context, dir string) (string, error) { return "", errFakeOffline }
	defer func() { pushFunc, fetchFunc = realPushFunc, realFetchFunc }()

	bareDir := t.TempDir()
	runGit(t, bareDir, "init", "--bare", "-b", "main")

	clone1 := t.TempDir()
	runGit(t, "", "clone", bareDir, clone1)
	runGit(t, clone1, "config", "user.name", "Clone1")
	runGit(t, clone1, "config", "user.email", "clone1@test.com")
	runGit(t, clone1, "commit", "--allow-empty", "-m", "root")
	runGit(t, clone1, "push", "origin", "main")

	g1 := &globals{Git: &meads.ExecGit{Dir: clone1}, Dir: clone1, TasksFile: "TASKS.md", GitMode: true}
	if err := (&initCmd{globals: g1, Git: true}).Run(); err != nil {
		t.Fatalf("init --git (clone1): %v", err)
	}
	if err := (&addCmd{globals: g1, Args: []string{"shared task"}}).Run(); err != nil {
		t.Fatalf("add (clone1): %v", err)
	}
	runGit(t, clone1, "push", "origin", meads.RefNamespace+"*:"+meads.RefNamespace+"*")

	gs1 := meads.NewGitStore(g1.git())
	all, err := gs1.Get(nil)
	if err != nil || len(all) != 1 {
		t.Fatalf("clone1 tasks after add = %v, err=%v, want exactly one", all, err)
	}
	sharedID = all[0].ID

	clone2 := t.TempDir()
	runGit(t, "", "clone", bareDir, clone2)
	runGit(t, clone2, "config", "user.name", "Clone2")
	runGit(t, clone2, "config", "user.email", "clone2@test.com")
	g2 = &globals{Git: &meads.ExecGit{Dir: clone2}, Dir: clone2, TasksFile: "TASKS.md", GitMode: true}
	if err := (&initCmd{globals: g2, Git: true}).Run(); err != nil {
		t.Fatalf("init --git (clone2): %v", err)
	}

	// Bootstrap clone2's own local copy of the shared task: a one-time,
	// explicit, non-wildcard fetch, safe because clone2 has nothing local at
	// this ref yet (see pkg/meads/gitdiverge_test.go's bootstrapLocalTask for
	// the identical technique and rationale).
	ref := meads.TasksRefPrefix + strconv.Itoa(sharedID)
	runGit(t, clone2, "fetch", "origin", ref+":"+ref)

	if err := (&updateCmd{globals: g1, ID: strconv.Itoa(sharedID), Title: "clone1's update"}).Run(); err != nil {
		t.Fatalf("update (clone1): %v", err)
	}
	runGit(t, clone1, "push", "origin", meads.RefNamespace+"*:"+meads.RefNamespace+"*")

	if err := (&updateCmd{globals: g2, ID: strconv.Itoa(sharedID), Title: "clone2's update"}).Run(); err != nil {
		t.Fatalf("update (clone2): %v", err)
	}

	// The real thing under test: a plain `git fetch origin`, relying
	// entirely on the fetch refspec `md init --git` configured above -
	// exactly what a real user would run, and exactly what must land in
	// refs/meads-remote/* rather than clobbering clone2's own local update.
	runGit(t, clone2, "fetch", "origin")

	// autoPush's decision logic ran on every add/update above even with
	// pushFunc suppressed; clear both clones' push state so it can't
	// interfere with anything a caller does after this helper returns (see
	// setupDivergedGitModeClones's identical cleanup in push_test.go).
	for _, g := range []*globals{g1, g2} {
		commonDir, err := gitCommonDir(g)
		if err != nil {
			t.Fatalf("gitCommonDir: %v", err)
		}
		if err := os.RemoveAll(filepath.Join(commonDir, meads.PushStateDir)); err != nil {
			t.Fatalf("clearing push state for %s: %v", g.Dir, err)
		}
	}

	return g2, sharedID
}

// errFakeOffline makes a stubbed fetchFunc fail as if the network were
// down, so autoPush skips the pull's Integrate (see push.go: a failed
// fetch leaves remote-tracking stale and integrating against it is
// deliberately skipped).
var errFakeOffline = errors.New("fake offline")

// TestIntegration_GitMode_DoctorResolvesDivergence proves `md doctor`'s
// git-mode path resolves an edit/edit divergence end to end (task 86): it
// reports the re-homing, exits ZERO (a divergence is auto-fixed now, not
// manual-attention), the id takes the fetched-remote version, and the local
// version is preserved as a new task - no merge, no force-push, no data
// loss.
func TestIntegration_GitMode_DoctorResolvesDivergence(t *testing.T) {
	g2, sharedID := setupDoctorDivergenceClones(t)

	out, err := captureStdout(t, (&doctorCmd{globals: g2}).Run)
	if err != nil {
		t.Fatalf("doctor should succeed (a divergence is auto-resolved now): %v", err)
	}
	wantMsg := "Task " + strconv.Itoa(sharedID) + " diverged with the fetched remote. Local version renumbered to " + strconv.Itoa(sharedID+1)
	if !strings.Contains(out, wantMsg) {
		t.Errorf("doctor output = %q, want it to contain %q", out, wantMsg)
	}

	gs2 := meads.NewGitStore(g2.git())
	// The id holds the fetched-remote version (so the next push converges).
	kept, err := gs2.Get([]int{sharedID})
	if err != nil || kept[0].Title != "clone1's update" {
		t.Fatalf("task %d after doctor = %+v (err=%v), want clone1's update (the id follows the remote)", sharedID, kept, err)
	}
	// The local version is preserved as a new task - no data loss.
	moved, err := gs2.Get([]int{sharedID + 1})
	if err != nil || len(moved) != 1 || moved[0].Title != "clone2's update" {
		t.Fatalf("task %d after doctor = %+v (err=%v), want clone2's update re-homed there", sharedID+1, moved, err)
	}

	// Fully resolved: a second doctor reports nothing.
	out2, err := captureStdout(t, (&doctorCmd{globals: g2}).Run)
	if err != nil {
		t.Fatalf("second doctor: %v", err)
	}
	if !strings.Contains(out2, "no issues found") {
		t.Errorf("second doctor output = %q, want \"no issues found\"", out2)
	}
}

// TestIntegration_GitMode_DoctorRenumbersCrossClonedDuplicate is the
// duplicate-id counterpart of TestIntegration_GitMode_DoctorResolvesDivergence
// above: two fully independent clones (never sharing any bootstrap state,
// unlike the divergence scenario) each create a FIRST task while
// disconnected, so create-if-absent's NextID computation gives both id 1 for
// two entirely unrelated tasks - exactly task 65's "two clones both create
// task 58" scenario. Proves `md doctor` renumbers it end to end through the
// real CLI and a real fetch: origin's task keeps the id, the local one is
// re-homed at a fresh id (task 86's convergent policy), so the next push
// succeeds.
func TestIntegration_GitMode_DoctorRenumbersCrossClonedDuplicate(t *testing.T) {
	realPushFunc := pushFunc
	pushFunc = func(ctx context.Context, dir string) (string, error) { return "", nil }
	defer func() { pushFunc = realPushFunc }()

	bareDir := t.TempDir()
	runGit(t, bareDir, "init", "--bare", "-b", "main")

	clone1 := t.TempDir()
	runGit(t, "", "clone", bareDir, clone1)
	runGit(t, clone1, "config", "user.name", "Clone1")
	runGit(t, clone1, "config", "user.email", "clone1@test.com")
	g1 := &globals{Git: &meads.ExecGit{Dir: clone1}, Dir: clone1, TasksFile: "TASKS.md", GitMode: true}
	if err := (&initCmd{globals: g1, Git: true}).Run(); err != nil {
		t.Fatalf("init --git (clone1): %v", err)
	}
	if err := (&addCmd{globals: g1, Args: []string{"clone1's task"}}).Run(); err != nil {
		t.Fatalf("add (clone1): %v", err)
	}

	// clone2 is fully independent: it is cloned and creates its OWN first
	// task BEFORE clone1 pushes, so its init --git sees an empty origin
	// (seeds fresh rather than adopting - see meads.InitTasks' adopt branch)
	// and it also computes id 1 - create-if-absent cannot coordinate across
	// a partition (gitmutate.go's Create doc comment).
	clone2 := t.TempDir()
	runGit(t, "", "clone", bareDir, clone2)
	runGit(t, clone2, "config", "user.name", "Clone2")
	runGit(t, clone2, "config", "user.email", "clone2@test.com")
	g2 := &globals{Git: &meads.ExecGit{Dir: clone2}, Dir: clone2, TasksFile: "TASKS.md", GitMode: true}
	if err := (&initCmd{globals: g2, Git: true}).Run(); err != nil {
		t.Fatalf("init --git (clone2): %v", err)
	}
	if err := (&addCmd{globals: g2, Args: []string{"clone2's task"}}).Run(); err != nil {
		t.Fatalf("add (clone2): %v", err)
	}

	runGit(t, clone1, "push", "origin", meads.RefNamespace+"*:"+meads.RefNamespace+"*")

	// A real plain `git fetch origin`, relying on the fetch refspec `md init
	// --git` configured - lands clone1's conflicting id-1 task in
	// refs/meads-remote/*, alongside clone2's own id 1.
	runGit(t, clone2, "fetch", "origin")

	out, err := captureStdout(t, (&doctorCmd{globals: g2}).Run)
	if err != nil {
		t.Fatalf("doctor should succeed (a duplicate-id fix is auto-applied, like a divergence): %v", err)
	}
	if !strings.Contains(out, "Duplicate ID 1 detected. Renumbered to 2") {
		t.Errorf("doctor output = %q, want it to report renumbering the duplicate", out)
	}

	gs2 := meads.NewGitStore(g2.git())
	// Origin's task keeps the id (so the next push converges)...
	kept, err := gs2.Get([]int{1})
	if err != nil || kept[0].Title != "clone1's task" {
		t.Fatalf("task 1 after doctor = %+v (err=%v), want clone1's task (the id follows origin)", kept, err)
	}
	// ...and the local task is re-homed at the fresh id, content preserved.
	moved, err := gs2.Get([]int{2})
	if err != nil || moved[0].Title != "clone2's task" {
		t.Fatalf("task 2 after doctor = %+v (err=%v), want clone2's re-homed task", moved, err)
	}
}
