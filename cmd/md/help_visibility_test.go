package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jpillora/meads/pkg/meads"
)

// Tests for task 71's fast, subprocess-free mode detection and help
// filtering (help_visibility.go). All repos here are built under
// t.TempDir() (or the harness, which already does the same) - never inside
// this actual project checkout.

// --- fastGitDir ---

func TestFastGitDir_PlainDirectory(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, ".git"), 0755); err != nil {
		t.Fatal(err)
	}
	got, ok := fastGitDir(dir)
	if !ok {
		t.Fatal("fastGitDir on a plain .git directory should succeed")
	}
	if want := filepath.Join(dir, ".git"); got != want {
		t.Errorf("fastGitDir = %q, want %q", got, want)
	}
}

func TestFastGitDir_LinkedWorktreeGitfile_AbsolutePath(t *testing.T) {
	dir := t.TempDir()
	realGitDir := t.TempDir() // stands in for the main worktree's .git
	writeFile(t, dir, ".git", "gitdir: "+realGitDir+"\n")
	got, ok := fastGitDir(dir)
	if !ok {
		t.Fatal("fastGitDir on a gitfile-style .git should succeed")
	}
	if got != realGitDir {
		t.Errorf("fastGitDir = %q, want %q", got, realGitDir)
	}
}

func TestFastGitDir_LinkedWorktreeGitfile_RelativePath(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "sub", "gitdir"), 0755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, dir, ".git", "gitdir: sub/gitdir\n")
	got, ok := fastGitDir(dir)
	if !ok {
		t.Fatal("fastGitDir with a relative gitdir: target should succeed")
	}
	if want := filepath.Join(dir, "sub", "gitdir"); got != want {
		t.Errorf("fastGitDir = %q, want %q", got, want)
	}
}

func TestFastGitDir_NoRepoAtAll(t *testing.T) {
	dir := t.TempDir()
	if _, ok := fastGitDir(dir); ok {
		t.Fatal("fastGitDir with no .git entry at all should report false")
	}
}

func TestFastGitDir_GitfileGarbage(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, ".git", "not a gitdir line at all\n")
	if _, ok := fastGitDir(dir); ok {
		t.Fatal("fastGitDir with an unrecognised .git file content should report false")
	}
}

func TestFastGitDir_GitfilePointsNowhere(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, ".git", "gitdir: /this/path/does/not/exist\n")
	if _, ok := fastGitDir(dir); ok {
		t.Fatal("fastGitDir whose gitdir: target does not exist should report false")
	}
}

// --- fastGitModeLikely ---

func TestFastGitModeLikely_NoRepo(t *testing.T) {
	dir := t.TempDir()
	if fastGitModeLikely(dir) {
		t.Fatal("fastGitModeLikely with no .git at all should be false")
	}
}

func TestFastGitModeLikely_PlainGitRepo_NoMeadsRefs(t *testing.T) {
	h := newHarness(t) // a real git repo via harness_test.go, no git-mode refs seeded
	if fastGitModeLikely(h.dir) {
		t.Fatal("fastGitModeLikely on an ordinary git repo with no refs/meads/* should be false")
	}
}

func TestFastGitModeLikely_LooseRefs(t *testing.T) {
	h := newHarness(t)
	gs := meads.NewGitStore(h.globals.git())
	if _, err := gs.Create(meads.Task{Title: "a git task", Status: "open"}); err != nil {
		t.Fatalf("seeding a task ref: %v", err)
	}
	// Precondition: still loose, not packed, so this genuinely exercises the
	// loose-ref path.
	if _, err := os.Stat(filepath.Join(h.dir, ".git", "refs", "meads")); err != nil {
		t.Fatalf("precondition: expected loose refs under .git/refs/meads, got: %v", err)
	}
	if !fastGitModeLikely(h.dir) {
		t.Fatal("fastGitModeLikely with a loose refs/meads/* ref present should be true")
	}
}

// TestFastGitModeLikely_PackedRefs proves the packed-refs path works by
// actually running `git pack-refs --all` against a real git-mode repo (not
// just a hand-crafted packed-refs file - see TestFilterHelp for a
// lower-level check of the substring match alone), confirming detection
// survives a real git gc-style repack.
func TestFastGitModeLikely_PackedRefs(t *testing.T) {
	h := newHarness(t)
	gs := meads.NewGitStore(h.globals.git())
	if _, err := gs.Create(meads.Task{Title: "a git task", Status: "open"}); err != nil {
		t.Fatalf("seeding a task ref: %v", err)
	}
	h.git("pack-refs", "--all")
	// Precondition: loose refs/meads/tasks/<id> must actually be gone now,
	// or this test would not distinguish the packed path from the loose one.
	if entries, err := os.ReadDir(filepath.Join(h.dir, ".git", "refs", "meads")); err == nil {
		for _, e := range entries {
			t.Fatalf("precondition: expected refs/meads/ to be empty after pack-refs, found %s", e.Name())
		}
	}
	if !fastGitModeLikely(h.dir) {
		t.Fatal("fastGitModeLikely after `git pack-refs --all` should still be true")
	}
}

func TestFastGitModeLikely_PackedRefsFile_HandCrafted(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, ".git"), 0755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, ".git"), "packed-refs",
		"# pack-refs with: peeled fully-peeled sorted\n"+
			"deadbeefdeadbeefdeadbeefdeadbeefdeadbeef refs/heads/main\n"+
			"cafecafecafecafecafecafecafecafecafecafe refs/meads/config\n")
	if !fastGitModeLikely(dir) {
		t.Fatal("fastGitModeLikely with a hand-crafted packed-refs entry for refs/meads/config should be true")
	}
}

func TestFastGitModeLikely_PackedRefsFile_NoMeadsNamespace(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, ".git"), 0755); err != nil {
		t.Fatal(err)
	}
	// A ref name that merely ENDS in "meads" must never false-positive
	// (isCommandGroupHeader-style substring confusion) - see
	// fastGitModeLikely's doc comment on why a leading space is part of the
	// match.
	writeFile(t, filepath.Join(dir, ".git"), "packed-refs",
		"# pack-refs with: peeled fully-peeled sorted\n"+
			"deadbeefdeadbeefdeadbeefdeadbeefdeadbeef refs/heads/notmeads\n")
	if fastGitModeLikely(dir) {
		t.Fatal("fastGitModeLikely must not match a ref that merely ends in \"meads\"")
	}
}

// --- fastTasksFileExists ---

func TestFastTasksFileExists(t *testing.T) {
	t.Run("TASKS.md present", func(t *testing.T) {
		dir := t.TempDir()
		writeFile(t, dir, "TASKS.md", "# TASKS\n")
		if !fastTasksFileExists(dir) {
			t.Fatal("want true with TASKS.md present")
		}
	})
	t.Run("TASKS.csv present", func(t *testing.T) {
		dir := t.TempDir()
		writeFile(t, dir, "TASKS.csv", "id,title\n")
		if !fastTasksFileExists(dir) {
			t.Fatal("want true with TASKS.csv present")
		}
	})
	t.Run("neither present", func(t *testing.T) {
		dir := t.TempDir()
		if fastTasksFileExists(dir) {
			t.Fatal("want false with no tasks file present")
		}
	})
}

// --- detectHelpMode / hiddenCommands ---

func TestDetectHelpMode(t *testing.T) {
	t.Run("unknown: empty directory", func(t *testing.T) {
		dir := t.TempDir()
		if got := detectHelpMode(dir); got != helpModeUnknown {
			t.Errorf("detectHelpMode = %v, want helpModeUnknown", got)
		}
	})
	t.Run("file: TASKS.md present, no git refs", func(t *testing.T) {
		dir := t.TempDir()
		writeFile(t, dir, "TASKS.md", "# TASKS\n")
		if got := detectHelpMode(dir); got != helpModeFile {
			t.Errorf("detectHelpMode = %v, want helpModeFile", got)
		}
	})
	t.Run("git: refs/meads/* present", func(t *testing.T) {
		h := newHarness(t)
		gs := meads.NewGitStore(h.globals.git())
		if _, err := gs.Create(meads.Task{Title: "t", Status: "open"}); err != nil {
			t.Fatalf("seeding a task ref: %v", err)
		}
		if got := detectHelpMode(h.dir); got != helpModeGit {
			t.Errorf("detectHelpMode = %v, want helpModeGit", got)
		}
	})
	t.Run("git wins even with a stray leftover tasks file", func(t *testing.T) {
		// Mirrors hook_git_test.go's TestIntegration_AutoSave_GitMode_Noop:
		// a stray TASKS.md (e.g. left over from before `md convert
		// --to-git`) must not flip detection back to file mode.
		h := newHarness(t)
		gs := meads.NewGitStore(h.globals.git())
		if _, err := gs.Create(meads.Task{Title: "t", Status: "open"}); err != nil {
			t.Fatalf("seeding a task ref: %v", err)
		}
		writeFile(t, h.dir, "TASKS.md", "stray, unrelated to git mode\n")
		if got := detectHelpMode(h.dir); got != helpModeGit {
			t.Errorf("detectHelpMode = %v, want helpModeGit (git-mode refs must win over a stray tasks file)", got)
		}
	})
	t.Run("empty dir argument means cwd", func(t *testing.T) {
		dir := t.TempDir()
		writeFile(t, dir, "TASKS.md", "# TASKS\n")
		t.Chdir(dir)
		if got := detectHelpMode(""); got != helpModeFile {
			t.Errorf("detectHelpMode(\"\") = %v, want helpModeFile (cwd fallback)", got)
		}
	})
}

func TestHiddenCommands(t *testing.T) {
	tests := []struct {
		mode helpMode
		want map[string]bool
	}{
		{helpModeUnknown, nil},
		{helpModeFile, nil},
		{helpModeGit, map[string]bool{"auto-save": true, "auto-delete": true, "beads-import": true}},
	}
	for _, tt := range tests {
		got := hiddenCommands(tt.mode)
		if len(got) != len(tt.want) {
			t.Errorf("hiddenCommands(%v) = %v, want %v", tt.mode, got, tt.want)
			continue
		}
		for name := range tt.want {
			if !got[name] {
				t.Errorf("hiddenCommands(%v) missing %q", tt.mode, name)
			}
		}
	}
}

// --- proof: the fast check never shells out to git ---

// TestFastCheck_NeverInvokesGit builds a real git-mode repo FIRST (using the
// normal harness, which needs a real git binary), then strips PATH down to a
// directory containing no executables at all and re-runs the fast checks
// against that same repo. If any of them shelled out to "git", they would
// fail (exec: "git": executable file not found in $PATH) or panic; instead
// they must return the exact same answers as before, proving they are pure
// filesystem stat/read calls.
func TestFastCheck_NeverInvokesGit(t *testing.T) {
	h := newHarness(t)
	gs := meads.NewGitStore(h.globals.git())
	if _, err := gs.Create(meads.Task{Title: "t", Status: "open"}); err != nil {
		t.Fatalf("seeding a task ref: %v", err)
	}

	emptyPathDir := t.TempDir() // guaranteed to contain no "git" binary
	if _, err := exec.LookPath(filepath.Join(emptyPathDir, "git")); err == nil {
		t.Fatal("precondition failed: emptyPathDir unexpectedly resolves a git binary")
	}
	t.Setenv("PATH", emptyPathDir)
	if _, err := exec.LookPath("git"); err == nil {
		t.Fatal("precondition failed: git is still resolvable on PATH")
	}

	if !fastGitModeLikely(h.dir) {
		t.Error("fastGitModeLikely returned a wrong/false answer with git removed from PATH - it must not depend on a git subprocess")
	}
	if got := detectHelpMode(h.dir); got != helpModeGit {
		t.Errorf("detectHelpMode with git removed from PATH = %v, want helpModeGit", got)
	}
	if hidden := hiddenCommands(detectHelpMode(h.dir)); !hidden["auto-save"] {
		t.Error("hiddenCommands did not reflect git mode with git removed from PATH")
	}
}

// --- filterHelp ---

func TestFilterHelp_NoHidden_Passthrough(t *testing.T) {
	help := "  Misc commands:\n  · auto-save  Auto-save hook\n"
	if got := filterHelp(help, nil); got != help {
		t.Errorf("filterHelp with nil hidden set should return input unchanged, got %q", got)
	}
	if got := filterHelp(help, map[string]bool{}); got != help {
		t.Errorf("filterHelp with an empty hidden set should return input unchanged, got %q", got)
	}
}

func TestFilterHelp_RemovesHiddenCommandLines_KeepsSiblings(t *testing.T) {
	help := "" +
		"  Misc commands:\n" +
		"  · auto-save    Auto-save hook\n" +
		"  · convert      Convert formats\n" +
		"\n" +
		"  Basic commands:\n" +
		"  · get          Get a task\n" +
		"  · list         List all tasks\n"
	got := filterHelp(help, map[string]bool{"auto-save": true})
	if strings.Contains(got, "auto-save") {
		t.Errorf("filterHelp left a hidden command in the output:\n%s", got)
	}
	for _, want := range []string{"convert", "get", "list", "Misc commands:", "Basic commands:"} {
		if !strings.Contains(got, want) {
			t.Errorf("filterHelp dropped %q it should have kept:\n%s", want, got)
		}
	}
}

func TestFilterHelp_RemovesWholeGroupWhenEmptied(t *testing.T) {
	help := "" +
		"  Usage: md [options] <command>\n" +
		"\n" +
		"  Misc commands:\n" +
		"  · convert       Convert formats\n" +
		"\n" +
		"  Beads commands:\n" +
		"  · beads-import  Import from beads\n" +
		"\n" +
		"  Version:\n" +
		"    1.0.0\n"
	got := filterHelp(help, map[string]bool{"beads-import": true})
	if strings.Contains(got, "beads-import") {
		t.Errorf("filterHelp left the hidden command in the output:\n%s", got)
	}
	if strings.Contains(got, "Beads commands:") {
		t.Errorf("filterHelp left a dangling empty group header:\n%s", got)
	}
	if !strings.Contains(got, "Misc commands:") || !strings.Contains(got, "convert") {
		t.Errorf("filterHelp dropped an unrelated group it should have kept:\n%s", got)
	}
	if !strings.Contains(got, "Version:") || !strings.Contains(got, "1.0.0") {
		t.Errorf("filterHelp dropped trailing content after the removed group:\n%s", got)
	}
	if strings.Contains(got, "\n\n\n") {
		t.Errorf("filterHelp left a run of blank lines behind after removing a whole group:\n%q", got)
	}
}

func TestFilterHelp_ExactNameMatch_NoPrefixCollision(t *testing.T) {
	// A command whose name merely starts with a hidden name's letters (e.g.
	// a hypothetical "auto-save-all") must survive - bulletCommandName must
	// match the exact first field, not a prefix.
	help := "  Misc commands:\n" +
		"  · auto-save      Auto-save hook\n" +
		"  · auto-save-all  Not the same command\n"
	got := filterHelp(help, map[string]bool{"auto-save": true})
	if strings.Contains(got, "Auto-save hook") {
		t.Errorf("filterHelp should have removed the exact \"auto-save\" row:\n%s", got)
	}
	if !strings.Contains(got, "auto-save-all") || !strings.Contains(got, "Not the same command") {
		t.Errorf("filterHelp must not remove a command whose name merely has the hidden name as a prefix:\n%s", got)
	}
}
