package main

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/jpillora/meads/pkg/meads"
)

// Tests proving the taskStore seam (cmd/md/taskstore.go) actually wires the
// CLI command layer to GitStore correctly, end to end through each wired
// command's Run() method - not just that GitStore itself works (that's
// pkg/meads/gitstore_test.go and gitmutate_test.go's job) and not just that
// mode() resolves correctly (that's mode_test.go's job).

// gitModeHarness returns a harness whose globals is forced into git mode via
// the harness's already-configured real git repo (see harness_test.go),
// ready to exercise the wired commands against GitStore.
//
// harness_test.go's newHarness always sets TasksFile to an absolute path,
// which explicitTasksFile (main.go) correctly treats as "an explicit tasks
// file" - and that beats GitMode in mode()'s precedence, same as --file
// would. So GitMode alone is not enough here: TasksFile must also be reset
// to the bare relative default, exactly like mode_test.go's modeHarness,
// or GitMode is silently ignored and e.g. webuiCmd/mcpCmd would start their
// real (blocking) servers instead of hitting the git-mode guard.
func gitModeHarness(t *testing.T) *testHarness {
	t.Helper()
	h := newHarness(t)
	t.Chdir(h.dir)
	h.globals.TasksFile = "TASKS.md"
	h.globals.GitMode = true
	return h
}

// TestIntegration_GitMode_FullCommandWiring drives add, get, list, ready,
// add-dep, set-status, rm-dep, update, and del through their real Run()
// methods in git mode, checking the result against GitStore directly (the
// ground truth) rather than parsing stdout.
func TestIntegration_GitMode_FullCommandWiring(t *testing.T) {
	h := gitModeHarness(t)
	g := h.globals
	gs := meads.NewGitStore(g.git())

	// --- add: two tasks ---
	if err := (&addCmd{globals: g, Args: []string{"first git task"}}).Run(); err != nil {
		t.Fatalf("add (1st): %v", err)
	}
	if err := (&addCmd{globals: g, Args: []string{"second git task"}}).Run(); err != nil {
		t.Fatalf("add (2nd): %v", err)
	}
	all, err := gs.Get(nil)
	if err != nil || len(all) != 2 {
		t.Fatalf("after two adds: tasks=%v err=%v, want 2 tasks", all, err)
	}
	id1, id2 := all[0].ID, all[1].ID // Get(nil) sorts ascending by id

	// --- get: resolves what add created ---
	if err := (&getCmd{globals: g, IDs: []string{strconv.Itoa(id1)}}).Run(); err != nil {
		t.Fatalf("get: %v", err)
	}

	// --- list / ready: succeed with no error (also exercises warnCycles
	// through the same seam) ---
	if err := (&listCmd{globals: g}).Run(); err != nil {
		t.Fatalf("list: %v", err)
	}
	if err := (&readyCmd{globals: g}).Run(); err != nil {
		t.Fatalf("ready: %v", err)
	}

	// --- add-dep: id2 depends on id1 ---
	if err := (&addDepCmd{globals: g, Child: strconv.Itoa(id2), Parent: strconv.Itoa(id1)}).Run(); err != nil {
		t.Fatalf("add-dep: %v", err)
	}
	got, err := gs.Get([]int{id2})
	if err != nil || len(got[0].DependsOn) != 1 || got[0].DependsOn[0] != id1 {
		t.Fatalf("after add-dep, task %d DependsOn = %v (err=%v), want [%d]", id2, got, err, id1)
	}

	// id2 is blocked (id1 is still open), so Ready() must exclude it.
	ready, err := gs.Ready()
	if err != nil {
		t.Fatalf("Ready: %v", err)
	}
	if readyContains(ready, id2) {
		t.Fatalf("task %d should be blocked by open task %d, but Ready() includes it: %v", id2, id1, ready)
	}

	// --- set-status: close id1 ---
	if err := (&setStatusCmd{globals: g, ID: strconv.Itoa(id1), Status: "closed"}).Run(); err != nil {
		t.Fatalf("set-status: %v", err)
	}
	got, err = gs.Get([]int{id1})
	if err != nil || got[0].Status != "closed" {
		t.Fatalf("after set-status, task %d = %v (err=%v), want status=closed", id1, got, err)
	}

	// id2's dependency is now closed, so it must be ready.
	ready, err = gs.Ready()
	if err != nil {
		t.Fatalf("Ready: %v", err)
	}
	if !readyContains(ready, id2) {
		t.Fatalf("task %d should now be ready (dependency closed), but Ready() = %v", id2, ready)
	}

	// --- rm-dep: id2 no longer depends on id1 ---
	if err := (&rmDepCmd{globals: g, Child: strconv.Itoa(id2), Parent: strconv.Itoa(id1)}).Run(); err != nil {
		t.Fatalf("rm-dep: %v", err)
	}
	got, err = gs.Get([]int{id2})
	if err != nil || len(got[0].DependsOn) != 0 {
		t.Fatalf("after rm-dep, task %d DependsOn = %v (err=%v), want none", id2, got, err)
	}

	// --- update: rename id2 ---
	if err := (&updateCmd{globals: g, ID: strconv.Itoa(id2), Title: "renamed git task"}).Run(); err != nil {
		t.Fatalf("update: %v", err)
	}
	got, err = gs.Get([]int{id2})
	if err != nil || got[0].Title != "renamed git task" {
		t.Fatalf("after update, task %d = %v (err=%v), want title \"renamed git task\"", id2, got, err)
	}

	// --- del: soft-delete id2 ---
	if err := (&delCmd{globals: g, ID: strconv.Itoa(id2)}).Run(); err != nil {
		t.Fatalf("del: %v", err)
	}
	if _, err := gs.Get([]int{id2}); err == nil {
		t.Fatalf("task %d should be excluded from Get after del (soft-deleted)", id2)
	}
	// `md get` uses GetWithHistory, which must still resolve a deleted task
	// (see taskStore's doc comment on GetWithHistory vs Get).
	if err := (&getCmd{globals: g, IDs: []string{strconv.Itoa(id2)}}).Run(); err != nil {
		t.Fatalf("get on a deleted task (via GetWithHistory) should still succeed: %v", err)
	}
	gwh, err := gs.GetWithHistory([]int{id2})
	if err != nil || len(gwh) != 1 || !gwh[0].Deleted {
		t.Fatalf("GetWithHistory(%d) after del = %v (err=%v), want one deleted task", id2, gwh, err)
	}
}

func readyContains(tasks []meads.Task, id int) bool {
	for _, t := range tasks {
		if t.ID == id {
			return true
		}
	}
	return false
}

// --- deferred commands: clear "not supported" errors in git mode, not
// silent misbehaviour against an unrelated/nonexistent TasksFile ---

// TestIntegration_GitMode_DoctorSupported is doctor's un-gating regression
// guard (task 65 phase 8): unlike beads-import/mcp/webui below, doctor now
// has a real GitStore-backed implementation, so it must run cleanly through
// its actual Run() method rather than erroring with "not supported in git
// mode yet".
func TestIntegration_GitMode_DoctorSupported(t *testing.T) {
	h := gitModeHarness(t)
	if err := (&addCmd{globals: h.globals, Args: []string{"a clean git-mode task"}}).Run(); err != nil {
		t.Fatalf("add: %v", err)
	}
	out, err := captureStdout(t, (&doctorCmd{globals: h.globals}).Run)
	if err != nil {
		t.Fatalf("doctor in git mode on a clean repo should succeed, got: %v", err)
	}
	if !strings.Contains(out, "no issues found") {
		t.Errorf("doctor output = %q, want it to report no issues found", out)
	}
}

func TestIntegration_GitMode_BeadsImportUnsupported(t *testing.T) {
	h := gitModeHarness(t)
	err := (&beadsImportCmd{globals: h.globals}).Run()
	assertGitModeUnsupported(t, err, "beads-import")
}

func TestIntegration_GitMode_McpUnsupported(t *testing.T) {
	h := gitModeHarness(t)
	err := (&mcpCmd{globals: h.globals}).Run()
	assertGitModeUnsupported(t, err, "mcp")
}

func TestIntegration_GitMode_WebuiUnsupported(t *testing.T) {
	h := gitModeHarness(t)
	err := (&webuiCmd{globals: h.globals}).Run()
	assertGitModeUnsupported(t, err, "webui")
}

func assertGitModeUnsupported(t *testing.T, err error, cmd string) {
	t.Helper()
	if err == nil {
		t.Fatalf("%s in git mode should error, got nil", cmd)
	}
	if !strings.Contains(err.Error(), "not supported in git mode yet") {
		t.Fatalf("%s in git mode error = %q, want it to mention \"not supported in git mode yet\"", cmd, err.Error())
	}
}

// A deferred command must report the --git/--file conflict too, not just
// the commands wired through tasks() (see TestTasks_ConflictingFlagsErrors
// in mode_test.go) - modeConflictErr is what makes that consistent.
func TestIntegration_GitMode_DeferredCommand_FlagConflictErrors(t *testing.T) {
	h := gitModeHarness(t)
	h.globals.FileMode = true // GitMode is already true from gitModeHarness
	err := (&doctorCmd{globals: h.globals}).Run()
	if err == nil {
		t.Fatal("doctor with both --git and --file should error, got nil")
	}
	if strings.Contains(err.Error(), "not supported in git mode yet") {
		t.Errorf("error = %q, want the flag-conflict error, not the git-mode-unsupported one", err.Error())
	}
}

// doctor's file-backend Doctor() calls ensureFile(), which would create a
// spurious TASKS.md in a git-mode repo if the mode dispatch in doctorCmd.Run
// ever regressed and fell through to runFile. Confirm the git-mode path
// actually prevents that side effect, not just that doctor happens to
// succeed.
func TestIntegration_GitMode_DoctorDoesNotCreateTasksFile(t *testing.T) {
	h := gitModeHarness(t)
	if err := (&doctorCmd{globals: h.globals}).Run(); err != nil {
		t.Fatalf("doctor in git mode: %v", err)
	}
	if _, err := os.Stat(filepath.Join(h.dir, "TASKS.md")); err == nil {
		t.Fatal("doctor must not create TASKS.md in git mode")
	}
}
