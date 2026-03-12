package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/jpillora/meads/pkg/meads"
)

// testHarness provides an isolated git repo with task helpers for integration tests.
type testHarness struct {
	t       *testing.T
	dir     string
	globals *globals
	store   *meads.Store
}

// newHarness creates a temp dir with an initialized git repo and returns a harness.
func newHarness(t *testing.T) *testHarness {
	return newHarnessWithBranch(t, "main")
}

// newCSVHarness creates a harness that uses TASKS.csv instead of TASKS.md.
func newCSVHarness(t *testing.T) *testHarness {
	return newHarnessWithBranchAndFile(t, "main", "TASKS.csv")
}

// newHarnessWithBranch creates a harness with a bare remote so origin/HEAD resolves correctly.
func newHarnessWithBranch(t *testing.T, branch string) *testHarness {
	return newHarnessWithBranchAndFile(t, branch, "TASKS.md")
}

// newHarnessWithBranchAndFile creates a harness with configurable branch and task file.
func newHarnessWithBranchAndFile(t *testing.T, branch, taskFile string) *testHarness {
	t.Helper()
	dir := t.TempDir()
	store := meads.NewFileStore(filepath.Join(dir, taskFile))
	h := &testHarness{
		t:     t,
		dir:   dir,
		store: store,
		globals: &globals{
			Store:     store,
			Git:       &meads.ExecGit{Dir: dir},
			TasksFile: filepath.Join(dir, taskFile),
			Dir:       dir,
		},
	}
	h.git("init", "-b", branch)
	h.git("config", "user.name", "Test")
	h.git("config", "user.email", "test@test.com")
	initial := filepath.Join(dir, ".gitkeep")
	if err := os.WriteFile(initial, []byte(""), 0644); err != nil {
		t.Fatal(err)
	}
	h.git("add", ".")
	h.git("commit", "-m", "initial")
	// Create a bare remote so symbolic-ref and set-head work
	bareDir := t.TempDir()
	cmd := exec.Command("git", "clone", "--bare", dir, bareDir)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("creating bare remote: %v\n%s", err, out)
	}
	h.git("remote", "add", "origin", bareDir)
	h.git("fetch", "origin")
	h.git("branch", "--set-upstream-to=origin/"+branch, branch)
	return h
}

// --- git helpers ---

// git runs a git command in the harness dir and fails the test on error.
func (h *testHarness) git(args ...string) string {
	h.t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = h.dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		h.t.Fatalf("git %s failed: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// commit stages the tasks file and commits with the given message.
func (h *testHarness) commit(msg string) {
	h.t.Helper()
	h.git("add", filepath.Base(h.globals.TasksFile))
	h.git("commit", "-m", msg)
}

// commitAll stages all files and commits.
func (h *testHarness) commitAll(msg string) {
	h.t.Helper()
	h.git("add", "-A")
	h.git("commit", "-m", msg)
}

// lastCommitMessage returns the subject of the last commit.
func (h *testHarness) lastCommitMessage() string {
	h.t.Helper()
	return h.git("log", "-1", "--format=%s")
}

// commitCount returns the number of commits.
func (h *testHarness) commitCount() int {
	h.t.Helper()
	out := h.git("rev-list", "--count", "HEAD")
	n, err := strconv.Atoi(out)
	if err != nil {
		h.t.Fatalf("parsing commit count: %v", err)
	}
	return n
}

// branch creates a new branch.
func (h *testHarness) branch(name string) {
	h.t.Helper()
	h.git("branch", name)
}

// checkout switches to a branch.
func (h *testHarness) checkout(name string) {
	h.t.Helper()
	h.git("checkout", name)
}

// --- task helpers ---

// addTask creates a task with the given title (status=open) and returns its ID.
func (h *testHarness) addTask(title string) int {
	h.t.Helper()
	t := meads.Task{Title: title}
	t.SetStatus("open")
	id, err := h.store.Add(t)
	if err != nil {
		h.t.Fatalf("addTask(%q): %v", title, err)
	}
	return id
}

// closeTask sets a task's status to closed.
func (h *testHarness) closeTask(id int) {
	h.t.Helper()
	err := h.store.Update(id, func(t *meads.Task) {
		t.SetStatus("closed")
	})
	if err != nil {
		h.t.Fatalf("closeTask(%d): %v", id, err)
	}
}

// setStatus sets a task's status.
func (h *testHarness) setStatus(id int, status string) {
	h.t.Helper()
	err := h.store.Update(id, func(t *meads.Task) {
		t.SetStatus(status)
	})
	if err != nil {
		h.t.Fatalf("setStatus(%d, %q): %v", id, status, err)
	}
}

// updatePriority sets a task's priority.
func (h *testHarness) updatePriority(id int, priority string) {
	h.t.Helper()
	err := h.store.Update(id, func(t *meads.Task) {
		t.SetPriority(priority)
	})
	if err != nil {
		h.t.Fatalf("updatePriority(%d, %q): %v", id, priority, err)
	}
}

// updateDescription sets a task's description.
func (h *testHarness) updateDescription(id int, desc string) {
	h.t.Helper()
	err := h.store.Update(id, func(t *meads.Task) {
		t.Description = desc
	})
	if err != nil {
		h.t.Fatalf("updateDescription(%d): %v", id, err)
	}
}

// deleteTask removes a task.
func (h *testHarness) deleteTask(id int) {
	h.t.Helper()
	if err := h.store.Delete(id); err != nil {
		h.t.Fatalf("deleteTask(%d): %v", id, err)
	}
}

// getTask returns a single task by ID.
func (h *testHarness) getTask(id int) meads.Task {
	h.t.Helper()
	tasks, err := h.store.Get([]int{id})
	if err != nil {
		h.t.Fatalf("getTask(%d): %v", id, err)
	}
	return tasks[0]
}

// getTasks returns all tasks.
func (h *testHarness) getTasks() []meads.Task {
	h.t.Helper()
	tasks, err := h.store.Get(nil)
	if err != nil {
		h.t.Fatalf("getTasks: %v", err)
	}
	return tasks
}

// readyTasks returns tasks from store.Ready.
func (h *testHarness) readyTasks() []meads.Task {
	h.t.Helper()
	tasks, err := h.store.Ready()
	if err != nil {
		h.t.Fatalf("readyTasks: %v", err)
	}
	return tasks
}

// addDep makes child depend on parent.
func (h *testHarness) addDep(child, parent int) {
	h.t.Helper()
	err := h.store.Update(child, func(t *meads.Task) {
		t.AddDep(parent)
	})
	if err != nil {
		h.t.Fatalf("addDep(%d, %d): %v", child, parent, err)
	}
}

// --- auto-delete helper ---

// runAutoDelete simulates the pre-commit hook by running autoDeleteCmd with GITHOOK=1.
// It modifies and stages TASKS.md but does not commit.
func (h *testHarness) runAutoDelete() error {
	h.t.Helper()
	h.t.Setenv("GITHOOK", "1")
	cmd := &autoDeleteCmd{globals: h.globals}
	return cmd.Run()
}


// createIndexLock creates a .git/index.lock to block git add/commit.
func (h *testHarness) createIndexLock() {
	h.t.Helper()
	lockPath := filepath.Join(h.dir, ".git", "index.lock")
	if err := os.WriteFile(lockPath, []byte(""), 0644); err != nil {
		h.t.Fatal(err)
	}
}

// removeIndexLock removes the .git/index.lock file.
func (h *testHarness) removeIndexLock() {
	h.t.Helper()
	lockPath := filepath.Join(h.dir, ".git", "index.lock")
	os.Remove(lockPath)
}

// tasksFileContent returns the raw content of TASKS.md.
func (h *testHarness) tasksFileContent() string {
	h.t.Helper()
	data, err := os.ReadFile(h.globals.TasksFile)
	if err != nil {
		h.t.Fatalf("reading tasks file: %v", err)
	}
	return string(data)
}

// assertTasksFileClean verifies the tasks file has no staged or unstaged changes vs HEAD.
func (h *testHarness) assertTasksFileClean() {
	h.t.Helper()
	taskFile := filepath.Base(h.globals.TasksFile)
	cmd := exec.Command("git", "diff", "--quiet", "HEAD", "--", taskFile)
	cmd.Dir = h.dir
	if err := cmd.Run(); err != nil {
		h.t.Fatalf("%s has unstaged changes (expected clean)", taskFile)
	}
	cmd = exec.Command("git", "diff", "--quiet", "--cached", "--", taskFile)
	cmd.Dir = h.dir
	if err := cmd.Run(); err != nil {
		h.t.Fatalf("%s has staged changes (expected clean)", taskFile)
	}
}

// --- assertions ---

func (h *testHarness) assertTaskCount(expected int) {
	h.t.Helper()
	tasks := h.getTasks()
	if len(tasks) != expected {
		h.t.Fatalf("expected %d tasks, got %d", expected, len(tasks))
	}
}

func (h *testHarness) assertTaskExists(id int) {
	h.t.Helper()
	_, err := h.store.Get([]int{id})
	if err != nil {
		h.t.Fatalf("expected task %d to exist, but got: %v", id, err)
	}
}

func (h *testHarness) assertTaskNotExists(id int) {
	h.t.Helper()
	_, err := h.store.Get([]int{id})
	if err == nil {
		h.t.Fatalf("expected task %d to not exist, but it does", id)
	}
}

func (h *testHarness) assertTaskStatus(id int, status string) {
	h.t.Helper()
	t := h.getTask(id)
	if t.Status != status {
		h.t.Fatalf("expected task %d status %q, got %q", id, status, t.Status)
	}
}

func (h *testHarness) assertReadyCount(expected int) {
	h.t.Helper()
	tasks := h.readyTasks()
	if len(tasks) != expected {
		h.t.Fatalf("expected %d ready tasks, got %d", expected, len(tasks))
	}
}

func (h *testHarness) assertReadyContains(id int) {
	h.t.Helper()
	for _, t := range h.readyTasks() {
		if t.ID == id {
			return
		}
	}
	h.t.Fatalf("expected ready list to contain task %d", id)
}

func (h *testHarness) assertReadyNotContains(id int) {
	h.t.Helper()
	for _, t := range h.readyTasks() {
		if t.ID == id {
			h.t.Fatalf("expected ready list to NOT contain task %d", id)
		}
	}
}
