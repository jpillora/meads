package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jpillora/meads/pkg/meads"
)

// Tests for the commit hooks (auto-save, auto-delete) becoming no-ops in git
// mode (git mode phase 9, TASKS #66): in git mode there is no working-tree
// tasks file to stage and nothing to prune (soft delete keeps every ref
// forever - GitStore.SoftDelete). Both hooks run from a real git pre-commit
// hook, so a spurious failure here would abort a user's commit - see
// sequencerInProgress's identical defensive posture, which these mirror.

// TestIntegration_AutoSave_GitMode_Noop proves auto-save no-ops in git mode
// even when a stray leftover TASKS.md is sitting in the working tree (e.g.
// from before migrating via `md convert --to-git`) - the git-mode check
// must win over the file-existence check, not just happen to no-op because
// no file exists.
func TestIntegration_AutoSave_GitMode_Noop(t *testing.T) {
	h := gitModeHarness(t)
	strayPath := filepath.Join(h.dir, "TASKS.md")
	if err := os.WriteFile(strayPath, []byte("stray content, unrelated to git mode"), 0644); err != nil {
		t.Fatal(err)
	}

	if err := h.runAutoSave(); err != nil {
		t.Fatalf("runAutoSave in git mode: %v", err)
	}
	if h.tasksFileStaged() {
		t.Fatal("auto-save must not stage a stray TASKS.md in git mode")
	}
	got, err := os.ReadFile(strayPath)
	if err != nil || string(got) != "stray content, unrelated to git mode" {
		t.Fatalf("stray TASKS.md was modified: content=%q err=%v", got, err)
	}
}

// TestIntegration_AutoDelete_GitMode_Noop proves auto-delete no-ops in git
// mode: a closed git-mode task's ref must survive untouched (nothing is
// ever pruned in git mode), and a stray leftover TASKS.md must not be
// rewritten or staged.
func TestIntegration_AutoDelete_GitMode_Noop(t *testing.T) {
	h := gitModeHarness(t)
	gs := meads.NewGitStore(h.globals.git())
	created, err := gs.Create(meads.Task{Title: "closed git task", Status: "closed"})
	if err != nil {
		t.Fatalf("seeding a closed git-mode task: %v", err)
	}

	strayPath := filepath.Join(h.dir, "TASKS.md")
	if err := os.WriteFile(strayPath, []byte("stray content"), 0644); err != nil {
		t.Fatal(err)
	}

	if err := h.runAutoDelete(); err != nil {
		t.Fatalf("runAutoDelete in git mode: %v", err)
	}
	if h.tasksFileStaged() {
		t.Fatal("auto-delete must not stage a stray TASKS.md in git mode")
	}
	got, err := os.ReadFile(strayPath)
	if err != nil || string(got) != "stray content" {
		t.Fatalf("stray TASKS.md was modified: content=%q err=%v", got, err)
	}

	// The closed task's ref must still exist, untouched: git mode never
	// prunes (soft delete keeps the ref forever).
	tasks, err := gs.Get([]int{created.ID})
	if err != nil || tasks[0].Status != "closed" {
		t.Fatalf("closed git-mode task should survive auto-delete untouched: got=%v err=%v", tasks, err)
	}
}

// TestIntegration_Hooks_GitMode_RealCommitSucceeds is the critical guard:
// with BOTH hooks actually installed in a git-mode repo, an ordinary
// `git commit` unrelated to tasks must still succeed. This runs the real
// compiled md binary (unlike runAutoSave/runAutoDelete above, which call the
// Go command directly) so the hook's own `command -v md` / `GITHOOK=1 md
// ...` invocation is genuinely exercised - a regression that turned the
// git-mode no-op check into an error (rather than a clean skip) would abort
// this commit.
func TestIntegration_Hooks_GitMode_RealCommitSucceeds(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping binary spawn test in short mode")
	}
	h := newHarness(t) // real init --git below only needs a real repo; auto-detection (not h.globals' own mode) is what the subprocess will use
	if err := (&initCmd{globals: h.globals, Git: true}).Run(); err != nil {
		t.Fatalf("init --git: %v", err)
	}

	bin := filepath.Join(t.TempDir(), "md")
	build := exec.Command("go", "build", "-o", bin, ".")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}

	if _, err := autoSaveBlock.install(h.globals); err != nil {
		t.Fatalf("install auto-save: %v", err)
	}
	if _, err := autoDeleteBlock.install(h.globals); err != nil {
		t.Fatalf("install auto-delete: %v", err)
	}

	writeFile(t, h.dir, "unrelated.txt", "hello")
	add := exec.Command("git", "add", "unrelated.txt")
	add.Dir = h.dir
	if out, err := add.CombinedOutput(); err != nil {
		t.Fatalf("git add: %v\n%s", err, out)
	}

	commit := exec.Command("git", "commit", "-m", "unrelated change")
	commit.Dir = h.dir
	commit.Env = append(os.Environ(), "PATH="+filepath.Dir(bin)+string(os.PathListSeparator)+os.Getenv("PATH"))
	out, err := commit.CombinedOutput()
	if err != nil {
		t.Fatalf("git commit with git-mode hooks installed should succeed: %v\n%s", err, out)
	}

	if _, err := os.Stat(filepath.Join(h.dir, "TASKS.md")); !os.IsNotExist(err) {
		t.Fatalf("the hooks must not create TASKS.md in a git-mode repo (stat err=%v)", err)
	}
}
