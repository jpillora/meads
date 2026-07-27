package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jpillora/meads/pkg/meads"
)

// CLI-level acceptance tests for the one-shot clone resolution (task 76):
// the bar is `git clone` then `md list` - no bootstrap command - in a clone
// of a git-mode repo. The resolution itself (marker ref, one-shot caching,
// no-origin and existing-file paths) is covered in pkg/meads/clone_test.go;
// these tests prove the CLI commands actually route through it.

// seedGitModeOrigin creates a bare origin holding a git-mode store (config
// ref plus the given titles), mirroring pkg/meads/clone_test.go's
// newGitModeOrigin.
func seedGitModeOrigin(t *testing.T, titles ...string) string {
	t.Helper()
	src := t.TempDir()
	runGit(t, src, "init", "-b", "main")
	runGit(t, src, "config", "user.name", "Test")
	runGit(t, src, "config", "user.email", "test@test.com")
	gs := meads.NewGitStore(&meads.ExecGit{Dir: src})
	if err := gs.SetConfig(meads.DefaultConfig()); err != nil {
		t.Fatalf("SetConfig: %v", err)
	}
	for _, title := range titles {
		if _, err := gs.Create(meads.Task{Title: title, Status: "open"}); err != nil {
			t.Fatalf("Create(%q): %v", title, err)
		}
	}
	bare := t.TempDir()
	runGit(t, bare, "init", "--bare", "-b", "main")
	runGit(t, src, "remote", "add", "origin", bare)
	runGit(t, src, "push", "origin", meads.RefNamespace+"*:"+meads.RefNamespace+"*")
	return bare
}

// cloneGlobals git-clones origin and returns globals pointed at the clone
// with the bare relative TasksFile (nothing forced, exactly what a real
// teammate's first `md` invocation resolves).
func cloneGlobals(t *testing.T, origin string) (*globals, string) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "clone")
	runGit(t, t.TempDir(), "clone", origin, dir)
	runGit(t, dir, "config", "user.name", "Test")
	runGit(t, dir, "config", "user.email", "test@test.com")
	t.Chdir(dir)
	return &globals{
		Git:       &meads.ExecGit{Dir: dir},
		Dir:       dir,
		TasksFile: "TASKS.md",
	}, dir
}

// TestIntegration_Clone_ListShowsGitModeTasks is the acceptance bar itself:
// clone a git-mode repo, run `md list` with no bootstrap command, and see
// the repo's real tasks.
func TestIntegration_Clone_ListShowsGitModeTasks(t *testing.T) {
	origin := seedGitModeOrigin(t, "lives in git mode", "another git task")
	g, dir := cloneGlobals(t, origin)

	out, err := captureStdout(t, (&listCmd{globals: g}).Run)
	if err != nil {
		t.Fatalf("list in a fresh clone: %v", err)
	}
	if !strings.Contains(out, "lives in git mode") || !strings.Contains(out, "another git task") {
		t.Errorf("list output = %q, want the origin's real tasks", out)
	}
	// The clone was adopted into git mode: no TASKS.md may appear.
	if _, err := os.Stat(filepath.Join(dir, "TASKS.md")); !os.IsNotExist(err) {
		t.Errorf("TASKS.md must not be created in an adopted clone (stat err=%v)", err)
	}
}

// TestIntegration_Clone_AddContinuesOriginIDs: `md add` in a fresh clone
// never creates TASKS.md, and the new task continues origin's ids so the
// push is a clean fast-forward (no collision with the server's ids).
func TestIntegration_Clone_AddContinuesOriginIDs(t *testing.T) {
	origin := seedGitModeOrigin(t, "task one", "task two")
	g, dir := cloneGlobals(t, origin)

	if err := (&addCmd{globals: g, Args: []string{"teammate adds one"}}).Run(); err != nil {
		t.Fatalf("add in a fresh clone: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "TASKS.md")); !os.IsNotExist(err) {
		t.Fatalf("TASKS.md created by add in an adopted clone (stat err=%v)", err)
	}
	gs := meads.NewGitStore(g.git())
	all, err := gs.Get(nil)
	if err != nil || len(all) != 3 {
		t.Fatalf("tasks after add = %v, %v; want 3", all, err)
	}
	if all[2].ID != 3 || all[2].Title != "teammate adds one" {
		t.Errorf("new task = %+v, want id 3 continuing origin's ids", all[2])
	}
	// The push is clean (no non-fast-forward rejection over origin's refs).
	if err := g.git().Run("push", "origin", meads.RefNamespace+"*:"+meads.RefNamespace+"*"); err != nil {
		t.Errorf("pushing the new task's ref should succeed cleanly, got: %v", err)
	}
}

// TestIntegration_Clone_InitGitAdopts: `md init --git` in a fresh clone of
// a git-mode repo adopts origin's refs instead of seeding an unrelated
// config ref (whose push would have been rejected non-fast-forward).
func TestIntegration_Clone_InitGitAdopts(t *testing.T) {
	origin := seedGitModeOrigin(t, "one", "two")
	g, _ := cloneGlobals(t, origin)

	out, err := captureStdout(t, (&initCmd{globals: g, Git: true}).Run)
	if err != nil {
		t.Fatalf("init --git in a fresh clone: %v", err)
	}
	if !strings.Contains(out, "adopted 2 task refs from origin") {
		t.Errorf("init --git output = %q, want \"adopted 2 task refs from origin\"", out)
	}
	gs := meads.NewGitStore(g.git())
	all, err := gs.Get(nil)
	if err != nil || len(all) != 2 {
		t.Fatalf("tasks after init --git adopt = %v, %v; want the origin's 2 tasks", all, err)
	}
}

// TestIntegration_Clone_SecondListNoLsRemote proves the resolution is
// one-shot at the CLI level: after the adopting `md list`, a second command
// (add, which resolves the store again) issues no further ls-remote. Since
// cmd can't count git calls through OpenTasksGit's internal ExecGit, this
// asserts on the terminal state instead: the adopted refs exist locally, so
// resolution can never reach the network again.
func TestIntegration_Clone_SecondListNoLsRemote(t *testing.T) {
	origin := seedGitModeOrigin(t, "a task")
	g, _ := cloneGlobals(t, origin)

	if err := (&listCmd{globals: g}).Run(); err != nil {
		t.Fatalf("first list: %v", err)
	}
	refs, initChecked := func() ([]string, bool) {
		out, _ := g.git().Output("for-each-ref", "--format=%(refname)", meads.RefNamespace, meads.InitCheckRef)
		var refs []string
		checked := false
		for _, line := range strings.Split(out, "\n") {
			if line == meads.InitCheckRef {
				checked = true
			} else if strings.HasPrefix(line, meads.RefNamespace) {
				refs = append(refs, line)
			}
		}
		return refs, checked
	}()
	if len(refs) == 0 {
		t.Fatal("after the adopting list, local refs/meads/* must exist (the terminal state)")
	}
	if initChecked {
		t.Errorf("%s must not be written on the adopt branch", meads.InitCheckRef)
	}
	// Sever the remote entirely: a second command must still work, proving
	// no network call is needed (or even attempted) once adopted.
	g2 := &globals{Git: &meads.ExecGit{Dir: g.Dir}, Dir: g.Dir, TasksFile: "TASKS.md"}
	g2.git().Run("remote", "remove", "origin")
	out, err := captureStdout(t, (&listCmd{globals: g2}).Run)
	if err != nil {
		t.Fatalf("second list with origin removed: %v", err)
	}
	if !strings.Contains(out, "a task") {
		t.Errorf("second list output = %q, want the adopted task", out)
	}
}

// TestIntegration_CloneOfPlainRepo_StaysFileMode: cloning a repo that never
// used meads keeps today's behavior - `md add` creates TASKS.md - with the
// marker ref written so origin is never asked twice.
func TestIntegration_CloneOfPlainRepo_StaysFileMode(t *testing.T) {
	src := t.TempDir()
	runGit(t, src, "init", "-b", "main")
	runGit(t, src, "config", "user.name", "Test")
	runGit(t, src, "config", "user.email", "test@test.com")
	writeFile(t, src, "f.txt", "x")
	runGit(t, src, "add", ".")
	runGit(t, src, "commit", "-m", "initial")
	bare := t.TempDir()
	runGit(t, bare, "init", "--bare", "-b", "main")
	runGit(t, src, "remote", "add", "origin", bare)
	runGit(t, src, "push", "origin", "main")
	g, dir := cloneGlobals(t, bare)

	if err := (&addCmd{globals: g, Args: []string{"ordinary task"}}).Run(); err != nil {
		t.Fatalf("add in a plain clone: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "TASKS.md")); err != nil {
		t.Errorf("TASKS.md should be created in a plain clone, stat: %v", err)
	}
	if out, _ := g.git().Output("for-each-ref", "--format=%(refname)", meads.InitCheckRef); out == "" {
		t.Errorf("%s should have been written after origin answered empty", meads.InitCheckRef)
	}
	// The marker is local-only and can never reach origin: the meads push
	// refspec (refs/meads/*:refs/meads/*) does not match it (the pkg-level
	// round trip asserts the same after a real push).
	if out, _ := (&meads.ExecGit{Dir: bare}).Output("for-each-ref", "--format=%(refname)", meads.InitCheckRef); out != "" {
		t.Errorf("origin must never hold %s, got %q", meads.InitCheckRef, out)
	}
}
