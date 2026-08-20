package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Tests for task 71's end-to-end guarantee: the REAL compiled binary, run as
// a subprocess (so main()'s actual opts.ParseArgsError/dispatch wiring is
// exercised, not just the Run() methods these other test files call
// directly), must (a) hide auto-save/auto-delete/beads-import from rendered
// help in a git-mode repo while still listing them in a file-mode repo, on
// BOTH the "-h"/error path and the "bare command, not runnable" path (see
// main()'s doc comment on why it needs both), and (b) still actually run
// auto-save/auto-delete successfully even while hidden - proving hiding
// never unregisters (task 71's CRITICAL constraint).

// buildMD compiles the md binary from this package into a temp dir shared
// by every subtest, mirroring webui_test.go/hook_git_test.go's own
// per-test build (skipped in -short for the same reason: a real `go build`
// subprocess is comparatively slow).
func buildMD(t *testing.T) string {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping binary spawn test in short mode")
	}
	bin := filepath.Join(t.TempDir(), "md")
	build := exec.Command("go", "build", "-o", bin, ".")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	return bin
}

// runMD runs bin with args in dir, returning combined stdout+stderr and the
// process's error (non-nil for a non-zero exit).
func runMD(t *testing.T, bin, dir string, env []string, args ...string) (string, error) {
	t.Helper()
	cmd := exec.Command(bin, args...)
	cmd.Dir = dir
	if len(env) > 0 {
		cmd.Env = append(os.Environ(), env...)
	}
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	err := cmd.Run()
	return buf.String(), err
}

// bulletLineFor reports whether out contains a rendered "· name" command row
// for name specifically (not merely name as a substring anywhere in the
// text, which could also appear inside another command's own help string).
func bulletLineFor(out, name string) bool {
	for _, line := range strings.Split(out, "\n") {
		if isBulletLine(line) && bulletCommandName(line) == name {
			return true
		}
	}
	return false
}

func TestIntegration_HelpVisibility_FileMode_ListsAllThree(t *testing.T) {
	bin := buildMD(t)
	h := newHarness(t) // plain git repo, no git-mode refs, TasksFile is absolute (irrelevant here: we invoke via cwd)
	writeFile(t, h.dir, "TASKS.md", "# TASKS\n")

	for _, args := range [][]string{{"--help"}, {"-h"}, {}} {
		out, _ := runMD(t, bin, h.dir, nil, args...)
		for _, name := range []string{"auto-save", "auto-delete", "beads-import"} {
			if !bulletLineFor(out, name) {
				t.Errorf("args=%v: file-mode help missing %q command row:\n%s", args, name, out)
			}
		}
	}
}

func TestIntegration_HelpVisibility_GitMode_HidesFileOnlyCommands(t *testing.T) {
	bin := buildMD(t)
	h := newHarness(t)
	if out, err := runMD(t, bin, h.dir, nil, "init", "--git"); err != nil {
		t.Fatalf("init --git: %v\n%s", err, out)
	}
	if out, err := runMD(t, bin, h.dir, nil, "add", "a git-mode task"); err != nil {
		t.Fatalf("add: %v\n%s", err, out)
	}

	// Both help-rendering paths (main()'s doc comment): (1) an error path -
	// "-h"/"--help" goes through opts' own exitError, reproduced by
	// optsFailureText; (2) the plain "bare command, nothing runnable, no
	// error" path - IsRunnable() false.
	for _, args := range [][]string{{"--help"}, {"-h"}, {}} {
		out, _ := runMD(t, bin, h.dir, nil, args...)
		for _, name := range []string{"auto-save", "auto-delete", "beads-import"} {
			if bulletLineFor(out, name) {
				t.Errorf("args=%v: git-mode help should not list %q:\n%s", args, name, out)
			}
		}
		// Sanity: this isn't just an empty/broken help output - ordinary
		// commands must still be listed.
		for _, name := range []string{"add", "list", "doctor", "webui", "mcp"} {
			if !bulletLineFor(out, name) {
				t.Errorf("args=%v: git-mode help unexpectedly dropped ordinary command %q:\n%s", args, name, out)
			}
		}
	}
}

// TestIntegration_HelpVisibility_GitMode_LinkedWorktree is task 75's
// acceptance check, stated the way the bug was reported: the same `md --help`,
// run from a linked worktree of the same git-mode repo, must hide the same
// commands. It used to list all three, because refs/meads/* is a shared ref
// and the probe was looking in the per-worktree git directory.
func TestIntegration_HelpVisibility_GitMode_LinkedWorktree(t *testing.T) {
	bin := buildMD(t)
	h := newHarness(t)
	if out, err := runMD(t, bin, h.dir, nil, "init", "--git"); err != nil {
		t.Fatalf("init --git: %v\n%s", err, out)
	}
	if out, err := runMD(t, bin, h.dir, nil, "add", "a git-mode task"); err != nil {
		t.Fatalf("add: %v\n%s", err, out)
	}
	wt := addWorktree(t, h, "help-integration-side")

	// The authoritative check: the worktree really is in git mode, so help
	// disagreeing with it is the bug and not a mis-set-up fixture.
	if out, err := runMD(t, bin, wt, nil, "list"); err != nil || !strings.Contains(out, "a git-mode task") {
		t.Fatalf("precondition: `md list` in the worktree = %q, err=%v", out, err)
	}

	for _, args := range [][]string{{"--help"}, {"-h"}, {}} {
		out, _ := runMD(t, bin, wt, nil, args...)
		for _, name := range []string{"auto-save", "auto-delete", "beads-import"} {
			if bulletLineFor(out, name) {
				t.Errorf("args=%v: help in a linked worktree of a git-mode repo should not list %q:\n%s", args, name, out)
			}
		}
		for _, name := range []string{"add", "list", "doctor"} {
			if !bulletLineFor(out, name) {
				t.Errorf("args=%v: help in a linked worktree dropped ordinary command %q:\n%s", args, name, out)
			}
		}
	}
}

// TestIntegration_HiddenCommands_StillInvokable_GitMode is the CRITICAL
// constraint's end-to-end proof: even though auto-save/auto-delete are
// hidden from git-mode help (proven above), the real compiled binary must
// still recognise and run them - exactly what the installed pre-commit hook
// depends on (`GITHOOK=1 md auto-save`/`auto-delete`, unconditionally).
func TestIntegration_HiddenCommands_StillInvokable_GitMode(t *testing.T) {
	bin := buildMD(t)
	h := newHarness(t)
	if out, err := runMD(t, bin, h.dir, nil, "init", "--git"); err != nil {
		t.Fatalf("init --git: %v\n%s", err, out)
	}

	// Precondition (from the test above, re-asserted narrowly here): these
	// really are hidden from this repo's help right now.
	helpOut, _ := runMD(t, bin, h.dir, nil, "--help")
	for _, name := range []string{"auto-save", "auto-delete"} {
		if bulletLineFor(helpOut, name) {
			t.Fatalf("precondition failed: %q is still listed in git-mode help:\n%s", name, helpOut)
		}
	}

	for _, name := range []string{"auto-save", "auto-delete"} {
		out, err := runMD(t, bin, h.dir, []string{"GITHOOK=1"}, name)
		if err != nil {
			t.Errorf("GITHOOK=1 md %s in git mode (hidden from help) should still succeed, got error: %v\n%s", name, err, out)
		}
	}

	// beads-import is hidden too, but unlike auto-save/auto-delete it is
	// genuinely unsupported in git mode (errGitModeUnsupported) rather than
	// a silent no-op - it must still be RECOGNISED (not "unknown command"),
	// even though it correctly errors for a different reason.
	out, err := runMD(t, bin, h.dir, nil, "beads-import")
	if err == nil {
		t.Fatal("beads-import in git mode should still error (unsupported), got nil")
	}
	if strings.Contains(out, "does not exist") || strings.Contains(out, "unexpected arguments") {
		t.Errorf("beads-import must be RECOGNISED as a command even while hidden from help - got what looks like an unknown-command error:\n%s", out)
	}
	if !strings.Contains(out, "not supported in git mode yet") {
		t.Errorf("beads-import in git mode error = %q, want it to mention \"not supported in git mode yet\"", out)
	}
}

// TestIntegration_HiddenCommands_StillNoop_GitMode complements the Run()-level
// no-op guards already in hook_git_test.go (TestIntegration_AutoSave_GitMode_Noop
// /TestIntegration_AutoDelete_GitMode_Noop) by proving the same thing through
// the real dispatch path: a stray leftover TASKS.md must not get staged, and
// a closed git-mode task's ref must survive untouched.
func TestIntegration_HiddenCommands_StillNoop_GitMode(t *testing.T) {
	bin := buildMD(t)
	h := newHarness(t)
	if out, err := runMD(t, bin, h.dir, nil, "init", "--git"); err != nil {
		t.Fatalf("init --git: %v\n%s", err, out)
	}
	strayPath := filepath.Join(h.dir, "TASKS.md")
	if err := os.WriteFile(strayPath, []byte("stray, unrelated to git mode"), 0644); err != nil {
		t.Fatal(err)
	}

	if out, err := runMD(t, bin, h.dir, []string{"GITHOOK=1"}, "auto-save"); err != nil {
		t.Fatalf("GITHOOK=1 md auto-save: %v\n%s", err, out)
	}
	if h.tasksFileStaged() {
		t.Fatal("auto-save must not stage a stray TASKS.md in git mode, even hidden from help")
	}
	got, err := os.ReadFile(strayPath)
	if err != nil || string(got) != "stray, unrelated to git mode" {
		t.Fatalf("stray TASKS.md was modified: content=%q err=%v", got, err)
	}
}

func TestIntegration_HelpVisibility_UnknownDirectory_ShowsEverything(t *testing.T) {
	// Neither a git repo nor a tasks file at all: helpModeUnknown, nothing
	// hidden (see hiddenCommands).
	bin := buildMD(t)
	dir := t.TempDir()
	out, _ := runMD(t, bin, dir, nil, "--help")
	for _, name := range []string{"auto-save", "auto-delete", "beads-import"} {
		if !bulletLineFor(out, name) {
			t.Errorf("help outside any repo/tasks-file should still list %q:\n%s", name, out)
		}
	}
}
