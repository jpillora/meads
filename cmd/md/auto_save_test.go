package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// auto-save stages working-tree task changes so they ride along in the next commit.

func TestIntegration_AutoSave_StagesTasksFile(t *testing.T) {
	h := newHarness(t)
	h.addTask("Base task")
	h.commit("add base task")

	// Mutate the tasks file without staging — as an md command would between commits.
	h.addTask("New task")
	if h.tasksFileStaged() {
		t.Fatal("precondition: tasks file should not be staged yet")
	}

	if err := h.runAutoSave(); err != nil {
		t.Fatalf("runAutoSave: %v", err)
	}
	if !h.tasksFileStaged() {
		t.Fatal("auto-save should have staged the tasks file")
	}
}

func TestIntegration_AutoSave_IncludedInUserCommit(t *testing.T) {
	h := newHarness(t)
	id := h.addTask("First task")
	h.commit("add first task")

	commitsBefore := h.commitCount()

	// Task changes accumulate in the working tree, unstaged.
	h.addTask("Second task")
	h.setStatus(id, "inprogress")

	// auto-save stages them but creates no commit of its own.
	if err := h.runAutoSave(); err != nil {
		t.Fatalf("runAutoSave: %v", err)
	}
	if h.commitCount() != commitsBefore {
		t.Fatal("auto-save should not create its own commit")
	}
	if !h.tasksFileStaged() {
		t.Fatal("auto-save should have staged the tasks file")
	}

	// The user's next commit (here for unrelated work) folds the tasks in.
	codeFile := filepath.Join(h.dir, "main.go")
	if err := os.WriteFile(codeFile, []byte("package main\n"), 0644); err != nil {
		t.Fatal(err)
	}
	h.git("add", "main.go")
	h.git("commit", "-m", "unrelated code change")

	if h.commitCount() != commitsBefore+1 {
		t.Fatalf("expected %d commits, got %d", commitsBefore+1, h.commitCount())
	}
	// The committed file matches the working tree exactly, and contains the new task.
	h.assertTasksFileClean()
	if show := h.git("show", "HEAD:TASKS.md"); !strings.Contains(show, "Second task") {
		t.Fatal("user commit should include the task added after the last commit")
	}
}

func TestIntegration_AutoSave_RunsOnFeatureBranch(t *testing.T) {
	h := newHarness(t)
	h.addTask("Base task")
	h.commit("add base task")

	h.branch("feature/x")
	h.checkout("feature/x")

	// Unlike auto-delete, auto-save is not gated to the default branch.
	h.addTask("Feature-branch task")
	if err := h.runAutoSave(); err != nil {
		t.Fatalf("runAutoSave: %v", err)
	}
	if !h.tasksFileStaged() {
		t.Fatal("auto-save should stage the tasks file even on a feature branch")
	}
}

func TestIntegration_AutoSave_NoTasksFileIsNoop(t *testing.T) {
	h := newHarness(t)
	// No tasks file has been created.
	if err := h.runAutoSave(); err != nil {
		t.Fatalf("runAutoSave with no tasks file should be a no-op: %v", err)
	}
}

func TestIntegration_AutoSave_NoChangesIsClean(t *testing.T) {
	h := newHarness(t)
	h.addTask("Task")
	h.commit("add task")

	// File is committed and unchanged; staging is a harmless no-op.
	if err := h.runAutoSave(); err != nil {
		t.Fatalf("runAutoSave: %v", err)
	}
	h.assertTasksFileClean()
}

func TestCSVIntegration_AutoSave(t *testing.T) {
	h := newCSVHarness(t)
	h.addTask("CSV task")
	h.commit("add task")

	h.addTask("Another CSV task")
	if err := h.runAutoSave(); err != nil {
		t.Fatalf("runAutoSave: %v", err)
	}
	if !h.tasksFileStaged() {
		t.Fatal("auto-save should stage TASKS.csv")
	}
}

// --- hook installation lifecycle ---

func TestIntegration_AutoSave_EnableDisableStatus(t *testing.T) {
	h := newHarness(t)
	t.Setenv("GITHOOK", "") // exercise the management path, not the hook path
	hookPath := h.preCommitHookPath()

	on, err := autoSaveBlock.installed(h.globals)
	if err != nil {
		t.Fatal(err)
	}
	if on {
		t.Fatal("auto-save should start disabled")
	}

	// Enable.
	if err := (&autoSaveCmd{globals: h.globals}).Run(); err != nil {
		t.Fatalf("enable: %v", err)
	}
	data, err := os.ReadFile(hookPath)
	if err != nil {
		t.Fatalf("reading hook: %v", err)
	}
	if !strings.Contains(string(data), autoSaveBlock.marker) {
		t.Fatal("hook should contain the auto-save marker after enable")
	}

	// Enabling twice is idempotent.
	if err := (&autoSaveCmd{globals: h.globals}).Run(); err != nil {
		t.Fatalf("re-enable: %v", err)
	}
	data, _ = os.ReadFile(hookPath)
	if n := strings.Count(string(data), autoSaveBlock.marker); n != 1 {
		t.Fatalf("expected exactly one auto-save block, got %d", n)
	}

	// Disable removes the block, and the now-empty hook file.
	if err := (&autoSaveCmd{globals: h.globals, Disable: true}).Run(); err != nil {
		t.Fatalf("disable: %v", err)
	}
	if _, err := os.Stat(hookPath); !os.IsNotExist(err) {
		t.Fatal("hook file should be removed once empty")
	}
}

func TestIntegration_Hooks_Coexist(t *testing.T) {
	h := newHarness(t)
	hookPath := h.preCommitHookPath()

	if _, err := autoDeleteBlock.install(h.globals); err != nil {
		t.Fatalf("install auto-delete: %v", err)
	}
	if _, err := autoSaveBlock.install(h.globals); err != nil {
		t.Fatalf("install auto-save: %v", err)
	}

	content := readFileString(t, hookPath)
	if !strings.Contains(content, autoDeleteBlock.marker) {
		t.Fatal("hook should contain the auto-delete block")
	}
	if !strings.Contains(content, autoSaveBlock.marker) {
		t.Fatal("hook should contain the auto-save block")
	}

	// Removing one block leaves the other intact.
	if _, err := autoSaveBlock.remove(h.globals); err != nil {
		t.Fatalf("remove auto-save: %v", err)
	}
	content = readFileString(t, hookPath)
	if strings.Contains(content, autoSaveBlock.marker) {
		t.Fatal("auto-save block should be gone")
	}
	if !strings.Contains(content, autoDeleteBlock.marker) {
		t.Fatal("auto-delete block should remain")
	}

	// Removing the last block removes the file.
	if _, err := autoDeleteBlock.remove(h.globals); err != nil {
		t.Fatalf("remove auto-delete: %v", err)
	}
	if _, err := os.Stat(hookPath); !os.IsNotExist(err) {
		t.Fatal("pre-commit hook should be removed when empty")
	}
}

// TestHookBlock_ValidShellSyntax guards against malformed hook templates without
// requiring md to be installed on PATH.
func TestHookBlock_ValidShellSyntax(t *testing.T) {
	for _, b := range []hookBlock{autoDeleteBlock, autoSaveBlock} {
		cmd := exec.Command("sh", "-n")
		cmd.Stdin = strings.NewReader(b.text())
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("hook %q is not valid shell: %v\n%s", b.marker, err, out)
		}
	}
}

func readFileString(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	return string(data)
}
