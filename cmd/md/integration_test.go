package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jpillora/meads/pkg/meads"
)

func TestIntegration_FullLifecycle(t *testing.T) {
	h := newHarness(t)

	id := h.addTask("Implement feature X")
	h.assertTaskCount(1)
	h.assertTaskStatus(id, "open")

	h.closeTask(id)
	h.assertTaskStatus(id, "closed")

	// Pre-commit auto-clean removes the closed task in the same commit.
	if err := h.runAutoDelete(); err != nil {
		t.Fatalf("runAutoDelete: %v", err)
	}
	h.assertTaskCount(0)
	h.assertTaskNotExists(id)
	h.commit("close task")

	// Subsequent auto-delete is a no-op.
	if err := h.runAutoDelete(); err != nil {
		t.Fatalf("runAutoDelete (no-op): %v", err)
	}
	h.assertTaskCount(0)
	h.assertTaskNotExists(id)
}

func TestIntegration_AutoDelete_OnlyOnDefaultBranch(t *testing.T) {
	h := newHarness(t)

	id := h.addTask("Task on feature branch")
	h.closeTask(id)
	h.commit("close task")

	// Switch to a feature branch
	h.branch("feature/test")
	h.checkout("feature/test")

	// Auto-delete should be a no-op on non-default branch
	if err := h.runAutoDelete(); err != nil {
		t.Fatalf("runAutoDelete: %v", err)
	}

	// Task should still exist (closed but not marked deleted)
	h.assertTaskCount(1)
	h.assertTaskExists(id)
}

func TestIntegration_AutoDelete_NonStandardDefaultBranch(t *testing.T) {
	h := newHarnessWithBranch(t, "beta")

	id := h.addTask("Task on beta")
	h.closeTask(id)

	// Pre-commit auto-clean marks closed task as deleted
	if err := h.runAutoDelete(); err != nil {
		t.Fatalf("runAutoDelete: %v", err)
	}

	// Task marked deleted — invisible to Get
	h.assertTaskCount(0)
	h.assertTaskNotExists(id)
}

func TestIntegration_AutoDelete_NonStandardDefaultBranch_SkipsFeature(t *testing.T) {
	h := newHarnessWithBranch(t, "beta")

	id := h.addTask("Task on feature branch")
	h.closeTask(id)
	h.commit("close task")

	h.branch("feature/test")
	h.checkout("feature/test")

	if err := h.runAutoDelete(); err != nil {
		t.Fatalf("runAutoDelete: %v", err)
	}

	h.assertTaskCount(1)
	h.assertTaskExists(id)
}

func TestIntegration_AutoDelete_MultipleClosed(t *testing.T) {
	h := newHarness(t)

	id1 := h.addTask("Closed task A")
	id2 := h.addTask("Open task B")
	id3 := h.addTask("Closed task C")

	h.closeTask(id1)
	h.closeTask(id3)

	// Pre-commit auto-clean marks closed tasks as deleted
	if err := h.runAutoDelete(); err != nil {
		t.Fatalf("runAutoDelete: %v", err)
	}

	// Closed tasks marked deleted — only open task visible
	h.assertTaskCount(1)
	h.assertTaskExists(id2)
	h.assertTaskNotExists(id1)
	h.assertTaskNotExists(id3)
}

func TestIntegration_AutoDelete_NoClosed(t *testing.T) {
	h := newHarness(t)

	h.addTask("Open task A")
	h.addTask("Open task B")
	h.commit("add tasks")

	contentBefore := h.tasksFileContent()

	if err := h.runAutoDelete(); err != nil {
		t.Fatalf("runAutoDelete: %v", err)
	}

	// No changes should have been made
	contentAfter := h.tasksFileContent()
	if contentAfter != contentBefore {
		t.Fatal("auto-delete modified TASKS.md when there was nothing to clean")
	}

	h.assertTaskCount(2)
}

func TestIntegration_Dependencies_BlockReady(t *testing.T) {
	h := newHarness(t)

	parent := h.addTask("Parent task")
	child := h.addTask("Child task")
	h.addDep(child, parent)

	// Child should be blocked, parent should be ready
	h.assertReadyCount(1)
	h.assertReadyContains(parent)
	h.assertReadyNotContains(child)

	// Close the parent
	h.closeTask(parent)

	// Now child should be ready
	h.assertReadyCount(1)
	h.assertReadyContains(child)
	h.assertReadyNotContains(parent) // closed tasks aren't "ready"
}

func TestIntegration_AutoDelete_MixedStatuses(t *testing.T) {
	h := newHarness(t)

	id1 := h.addTask("Closed task")
	id2 := h.addTask("Open task")
	id3 := h.addTask("InProgress task")
	id4 := h.addTask("Draft task")
	id5 := h.addTask("Another closed task")

	h.closeTask(id1)
	// id2 stays open
	h.setStatus(id3, "inprogress")
	h.setStatus(id4, "draft")
	h.closeTask(id5)

	// Pre-commit auto-clean marks closed tasks as deleted
	if err := h.runAutoDelete(); err != nil {
		t.Fatalf("runAutoDelete: %v", err)
	}

	// Closed tasks marked deleted — only non-closed tasks visible
	h.assertTaskNotExists(id1)
	h.assertTaskExists(id2)
	h.assertTaskExists(id3)
	h.assertTaskExists(id4)
	h.assertTaskNotExists(id5)
	h.assertTaskCount(3)

	// Verify statuses preserved
	h.assertTaskStatus(id2, "open")
	h.assertTaskStatus(id3, "inprogress")
	h.assertTaskStatus(id4, "draft")
}

func TestIntegration_AutoDelete_PreservesTaskData(t *testing.T) {
	h := newHarness(t)

	// Create a task with rich metadata
	id1 := h.addTask("Keep this task")
	h.updatePriority(id1, "P0")
	h.updateDescription(id1, "Important details\nwith multiple lines")
	id2 := h.addTask("Closed task")
	h.closeTask(id2)

	// Make id1 depend on a third task (not closed)
	id3 := h.addTask("Dependency task")
	h.addDep(id1, id3)

	// Pre-commit auto-clean marks closed task as deleted
	if err := h.runAutoDelete(); err != nil {
		t.Fatalf("runAutoDelete: %v", err)
	}

	h.assertTaskNotExists(id2)
	h.assertTaskExists(id1)

	// Verify metadata survived
	task := h.getTask(id1)
	if task.Priority != "P0" {
		t.Fatalf("expected priority P0, got %s", task.Priority)
	}
	if task.Description != "Important details\nwith multiple lines" {
		t.Fatalf("description mangled: %q", task.Description)
	}
	if len(task.DependsOn) != 1 || task.DependsOn[0] != id3 {
		t.Fatalf("expected depends-on [%d], got %v", id3, task.DependsOn)
	}
}

func TestIntegration_AutoDelete_CleansDanglingDeps(t *testing.T) {
	h := newHarness(t)

	parent := h.addTask("Parent task")
	child := h.addTask("Child task")
	h.addDep(child, parent)

	// Close the parent
	h.closeTask(parent)

	// Pre-commit auto-clean marks parent as deleted
	if err := h.runAutoDelete(); err != nil {
		t.Fatalf("runAutoDelete: %v", err)
	}

	// Parent should be deleted
	h.assertTaskNotExists(parent)
	h.assertTaskExists(child)

	// Child's DependsOn should be cleaned up
	task := h.getTask(child)
	if len(task.DependsOn) != 0 {
		t.Fatalf("expected empty depends-on after parent deleted, got %v", task.DependsOn)
	}

	// Child should now be updatable (no dangling dep validation error)
	h.updatePriority(child, "P1")
	task = h.getTask(child)
	if task.Priority != "P1" {
		t.Fatalf("expected priority P1, got %s", task.Priority)
	}
}

func TestIntegration_AutoDelete_RestoresOnGitAddFailure(t *testing.T) {
	h := newHarness(t)

	id1 := h.addTask("Open task")
	id2 := h.addTask("Closed task")
	h.closeTask(id2)

	contentBefore := h.tasksFileContent()

	// Block git add by creating index.lock
	h.createIndexLock()

	err := h.runAutoDelete()
	if err == nil {
		t.Fatal("expected error from runAutoDelete, got nil")
	}

	// Remove lock so we can verify state
	h.removeIndexLock()

	// TASKS.md should be restored
	contentAfter := h.tasksFileContent()
	if contentAfter != contentBefore {
		t.Fatal("TASKS.md content was not restored after git add failure")
	}

	// Both tasks should still exist
	h.assertTaskCount(2)
	h.assertTaskExists(id1)
	h.assertTaskExists(id2)
}

func TestIntegration_AutoDelete_RecordsMaxIDWhenLatestClosed(t *testing.T) {
	h := newHarness(t)

	id1 := h.addTask("Open task")
	id2 := h.addTask("Closed task (latest)")
	h.closeTask(id2)

	// Pre-commit auto-clean removes the closed task. Because it was the
	// highest ID, the high-water mark is persisted in project meta.
	if err := h.runAutoDelete(); err != nil {
		t.Fatalf("runAutoDelete: %v", err)
	}
	h.assertTaskCount(1)
	h.assertTaskExists(id1)
	h.assertTaskNotExists(id2)

	content := h.tasksFileContent()
	if !strings.Contains(content, "max-id: 2") {
		t.Fatalf("expected max-id meta marker, got:\n%s", content)
	}

	h.commit("close task")

	// Adding a new task must continue from the recorded max-id, not reuse 2.
	id3 := h.addTask("Brand new task")
	if id3 != 3 {
		t.Fatalf("expected id 3 (continuing from max-id), got %d", id3)
	}
}

func TestIntegration_AutoDelete_NoMaxIDWhenNonLatestClosed(t *testing.T) {
	h := newHarness(t)

	id1 := h.addTask("Closed task (not latest)")
	id2 := h.addTask("Open task")
	h.closeTask(id1)

	if err := h.runAutoDelete(); err != nil {
		t.Fatalf("runAutoDelete: %v", err)
	}
	h.assertTaskCount(1)
	h.assertTaskExists(id2)
	h.assertTaskNotExists(id1)

	content := h.tasksFileContent()
	if strings.Contains(content, "max-id:") {
		t.Fatalf("max-id should not be set when surviving active id is higher, got:\n%s", content)
	}
}

func TestIntegration_AutoDelete_IncludedInUserCommit(t *testing.T) {
	h := newHarness(t)

	h.addTask("Open task")
	id2 := h.addTask("Closed task")
	h.closeTask(id2)

	commitsBefore := h.commitCount()

	// Pre-commit auto-clean stages the deletion
	if err := h.runAutoDelete(); err != nil {
		t.Fatalf("runAutoDelete: %v", err)
	}

	// No new commit yet — auto-delete only stages
	if h.commitCount() != commitsBefore {
		t.Fatal("auto-delete should not create its own commit")
	}

	// User's commit includes the auto-clean changes
	h.commit("close task")

	// Only ONE new commit (the user's), not two
	if h.commitCount() != commitsBefore+1 {
		t.Fatalf("expected %d commits, got %d", commitsBefore+1, h.commitCount())
	}

	// Verify the commit message is the user's, not auto-clean
	msg := h.lastCommitMessage()
	if msg != "close task" {
		t.Fatalf("expected user's commit message, got %q", msg)
	}

	// Verify the commit has the closed task removed and the open task kept.
	showOutput := h.git("show", "HEAD:TASKS.md")
	if strings.Contains(showOutput, "Closed task") {
		t.Fatal("closed task should have been removed from the file")
	}
	if !strings.Contains(showOutput, "Open task") {
		t.Fatal("commit is missing the open task")
	}
	if !strings.Contains(showOutput, "max-id: 2") {
		t.Fatal("commit should record max-id for the closed (highest) task")
	}
}

func TestIntegration_AutoDelete_ClosedWithMultipleDependents(t *testing.T) {
	h := newHarness(t)

	parent := h.addTask("Parent task")
	child1 := h.addTask("Child one")
	child2 := h.addTask("Child two")
	h.addDep(child1, parent)
	h.addDep(child2, parent)

	h.closeTask(parent)

	// Pre-commit auto-clean marks parent as deleted
	if err := h.runAutoDelete(); err != nil {
		t.Fatalf("runAutoDelete: %v", err)
	}

	// Parent marked deleted — not visible to Get
	h.assertTaskNotExists(parent)
	h.assertTaskCount(2)

	// Both children should have clean deps and be ready
	for _, childID := range []int{child1, child2} {
		task := h.getTask(childID)
		if len(task.DependsOn) != 0 {
			t.Fatalf("child %d still has deps: %v", childID, task.DependsOn)
		}
	}
	h.assertReadyCount(2)
}


func TestIntegration_NewlineAsTitleDelimiter(t *testing.T) {
	t.Run("newline splits title and description", func(t *testing.T) {
		h := newHarness(t)
		cmd := &addCmd{globals: h.globals}
		cmd.Args = []string{"Fix the login bug\nSession cookie expires too early"}
		if err := cmd.Run(); err != nil {
			t.Fatalf("addCmd.Run: %v", err)
		}
		task := h.getTask(1)
		if task.Title != "Fix the login bug" {
			t.Fatalf("expected title %q, got %q", "Fix the login bug", task.Title)
		}
		if task.Description != "Session cookie expires too early" {
			t.Fatalf("expected description %q, got %q", "Session cookie expires too early", task.Description)
		}
	})

	t.Run("period before newline uses period", func(t *testing.T) {
		h := newHarness(t)
		cmd := &addCmd{globals: h.globals}
		cmd.Args = []string{"Fix bug. Details here\nMore details"}
		if err := cmd.Run(); err != nil {
			t.Fatalf("addCmd.Run: %v", err)
		}
		task := h.getTask(1)
		if task.Title != "Fix bug" {
			t.Fatalf("expected title %q, got %q", "Fix bug", task.Title)
		}
		if task.Description != "Details here\nMore details" {
			t.Fatalf("expected description %q, got %q", "Details here\nMore details", task.Description)
		}
	})

	t.Run("newline before period uses newline", func(t *testing.T) {
		h := newHarness(t)
		cmd := &addCmd{globals: h.globals}
		cmd.Args = []string{"Fix bug\nDetails here. More details"}
		if err := cmd.Run(); err != nil {
			t.Fatalf("addCmd.Run: %v", err)
		}
		task := h.getTask(1)
		if task.Title != "Fix bug" {
			t.Fatalf("expected title %q, got %q", "Fix bug", task.Title)
		}
		if task.Description != "Details here. More details" {
			t.Fatalf("expected description %q, got %q", "Details here. More details", task.Description)
		}
	})

	t.Run("type and priority with newline", func(t *testing.T) {
		h := newHarness(t)
		cmd := &addCmd{globals: h.globals}
		cmd.Args = []string{"bug: Fix login P1\nSession cookie expires"}
		if err := cmd.Run(); err != nil {
			t.Fatalf("addCmd.Run: %v", err)
		}
		task := h.getTask(1)
		if task.Title != "Fix login" {
			t.Fatalf("expected title %q, got %q", "Fix login", task.Title)
		}
		if task.Type != "bug" {
			t.Fatalf("expected type %q, got %q", "bug", task.Type)
		}
		if task.Priority != "P1" {
			t.Fatalf("expected priority %q, got %q", "P1", task.Priority)
		}
		if task.Description != "Session cookie expires" {
			t.Fatalf("expected description %q, got %q", "Session cookie expires", task.Description)
		}
	})

	t.Run("period inside URL does not split", func(t *testing.T) {
		h := newHarness(t)
		cmd := &addCmd{globals: h.globals}
		cmd.Args = []string{"Investigate http://foo.com latency. See the dashboard"}
		if err := cmd.Run(); err != nil {
			t.Fatalf("addCmd.Run: %v", err)
		}
		task := h.getTask(1)
		if task.Title != "Investigate http://foo.com latency" {
			t.Fatalf("expected title %q, got %q", "Investigate http://foo.com latency", task.Title)
		}
		if task.Description != "See the dashboard" {
			t.Fatalf("expected description %q, got %q", "See the dashboard", task.Description)
		}
	})

	t.Run("JSON unicode escapes decode in description flag", func(t *testing.T) {
		h := newHarness(t)
		cmd := &addCmd{globals: h.globals}
		cmd.Title = "Unicode"
		cmd.Description = `— line1\nline2\tafter tab`
		if err := cmd.Run(); err != nil {
			t.Fatalf("addCmd.Run: %v", err)
		}
		task := h.getTask(1)
		want := "— line1\nline2\tafter tab"
		if task.Description != want {
			t.Fatalf("expected description %q, got %q", want, task.Description)
		}
	})
}

func TestIntegration_PriorityNormalization(t *testing.T) {
	t.Run("bare number via flag", func(t *testing.T) {
		h := newHarness(t)
		cmd := &addCmd{globals: h.globals}
		cmd.Title = "Bare number priority"
		cmd.Priority = "1"
		if err := cmd.Run(); err != nil {
			t.Fatalf("addCmd.Run: %v", err)
		}
		task := h.getTask(1)
		if task.Priority != "P1" {
			t.Fatalf("expected P1, got %s", task.Priority)
		}
	})

	t.Run("lowercase via flag", func(t *testing.T) {
		h := newHarness(t)
		cmd := &addCmd{globals: h.globals}
		cmd.Title = "Lowercase priority"
		cmd.Priority = "p3"
		if err := cmd.Run(); err != nil {
			t.Fatalf("addCmd.Run: %v", err)
		}
		task := h.getTask(1)
		if task.Priority != "P3" {
			t.Fatalf("expected P3, got %s", task.Priority)
		}
	})

	t.Run("invalid priority rejected", func(t *testing.T) {
		h := newHarness(t)
		cmd := &addCmd{globals: h.globals}
		cmd.Title = "Bad priority"
		cmd.Priority = "banana"
		if err := cmd.Run(); err == nil {
			t.Fatal("expected error for invalid priority")
		}
	})

	t.Run("update normalizes", func(t *testing.T) {
		h := newHarness(t)
		h.addTask("Update me")
		cmd := &updateCmd{globals: h.globals, ID: "1", Priority: "3"}
		if err := cmd.Run(); err != nil {
			t.Fatalf("updateCmd.Run: %v", err)
		}
		task := h.getTask(1)
		if task.Priority != "P3" {
			t.Fatalf("expected P3, got %s", task.Priority)
		}
	})
}

func TestIntegration_CommandStructs(t *testing.T) {
	t.Run("addCmd", func(t *testing.T) {
		h := newHarness(t)
		cmd := &addCmd{globals: h.globals}
		cmd.Title = "Test task via addCmd"
		if err := cmd.Run(); err != nil {
			t.Fatalf("addCmd.Run: %v", err)
		}
		h.assertTaskCount(1)
		task := h.getTask(1)
		if task.Title != "Test task via addCmd" {
			t.Fatalf("expected title %q, got %q", "Test task via addCmd", task.Title)
		}
	})

	t.Run("updateCmd", func(t *testing.T) {
		h := newHarness(t)
		id := h.addTask("Update me")
		cmd := &updateCmd{globals: h.globals, ID: "1", Priority: "P1"}
		if err := cmd.Run(); err != nil {
			t.Fatalf("updateCmd.Run: %v", err)
		}
		task := h.getTask(id)
		if task.Priority != "P1" {
			t.Fatalf("expected priority P1, got %s", task.Priority)
		}
	})

	t.Run("setStatusCmd", func(t *testing.T) {
		h := newHarness(t)
		id := h.addTask("Close me")
		cmd := &setStatusCmd{globals: h.globals, ID: "1", Status: "closed"}
		if err := cmd.Run(); err != nil {
			t.Fatalf("setStatusCmd.Run: %v", err)
		}
		h.assertTaskStatus(id, "closed")
	})
}

// --- CSV Integration Tests ---

func TestCSVIntegration_FullLifecycle(t *testing.T) {
	h := newCSVHarness(t)

	// Add tasks
	id1 := h.addTask("First CSV task")
	id2 := h.addTask("Second CSV task")
	h.assertTaskCount(2)
	h.assertTaskStatus(id1, "open")
	h.assertTaskStatus(id2, "open")

	// Update priority
	h.updatePriority(id1, "P1")
	task := h.getTask(id1)
	if task.Priority != "P1" {
		t.Fatalf("expected priority P1, got %s", task.Priority)
	}

	// Close a task
	h.closeTask(id1)
	h.assertTaskStatus(id1, "closed")

	// Delete a task (soft-delete)
	h.deleteTask(id2)
	h.assertTaskCount(1) // only id1 visible (closed)
	h.assertTaskNotExists(id2)

	// New task should get correct ID
	id3 := h.addTask("Third CSV task")
	if id3 != 3 {
		t.Fatalf("expected ID 3, got %d", id3)
	}
	h.assertTaskCount(2) // id1 (closed) + id3 (open)
}

func TestCSVIntegration_Dependencies(t *testing.T) {
	h := newCSVHarness(t)

	parent := h.addTask("Parent task")
	child := h.addTask("Child task")
	h.addDep(child, parent)

	// Child should be blocked
	h.assertReadyCount(1)
	h.assertReadyContains(parent)
	h.assertReadyNotContains(child)

	// Close parent
	h.closeTask(parent)

	// Now child should be ready
	h.assertReadyCount(1)
	h.assertReadyContains(child)
}

func TestCSVIntegration_SoftDeleteCleansDeps(t *testing.T) {
	h := newCSVHarness(t)

	parent := h.addTask("Parent")
	child := h.addTask("Child")
	h.addDep(child, parent)

	// Delete parent
	h.deleteTask(parent)

	// Child should have clean deps
	task := h.getTask(child)
	if len(task.DependsOn) != 0 {
		t.Fatalf("expected empty depends-on, got %v", task.DependsOn)
	}

	// Child should be updatable
	h.updatePriority(child, "P0")
	task = h.getTask(child)
	if task.Priority != "P0" {
		t.Fatalf("expected priority P0, got %s", task.Priority)
	}
}

func TestCSVIntegration_CommitAndShow(t *testing.T) {
	h := newCSVHarness(t)

	h.addTask("CSV task one")
	h.addTask("CSV task two")
	h.commit("add tasks")

	// Verify git show contains CSV data
	taskFile := filepath.Base(h.globals.TasksFile)
	showOutput := h.git("show", "HEAD:"+taskFile)
	if !strings.Contains(showOutput, "CSV task one") {
		t.Fatal("committed file missing 'CSV task one'")
	}
	if !strings.Contains(showOutput, "CSV task two") {
		t.Fatal("committed file missing 'CSV task two'")
	}
}

func TestCSVIntegration_MultilineDescription(t *testing.T) {
	h := newCSVHarness(t)

	id := h.addTask("Bug report")
	multiline := "Line one.\n\nLine two with detail.\n\nLine three."
	h.updateDescription(id, multiline)

	task := h.getTask(id)
	if task.Description != multiline {
		t.Errorf("description round-trip failed:\ngot:  %q\nwant: %q", task.Description, multiline)
	}
}

// --- List Filter Tests ---

func TestIntegration_ListFilter_Status(t *testing.T) {
	h := newHarness(t)

	h.addTask("Open task")
	id2 := h.addTask("Closed task")
	id3 := h.addTask("InProgress task")
	h.closeTask(id2)
	h.setStatus(id3, "inprogress")

	cmd := &listCmd{globals: h.globals}
	cmd.Status = "open"
	tasks := cmd.filterTasks(h.getTasks())
	if len(tasks) != 1 {
		t.Fatalf("expected 1 open task, got %d", len(tasks))
	}
	if tasks[0].Title != "Open task" {
		t.Fatalf("expected 'Open task', got %q", tasks[0].Title)
	}

	cmd.Status = "inprogress"
	tasks = cmd.filterTasks(h.getTasks())
	if len(tasks) != 1 {
		t.Fatalf("expected 1 inprogress task, got %d", len(tasks))
	}
	if tasks[0].Title != "InProgress task" {
		t.Fatalf("expected 'InProgress task', got %q", tasks[0].Title)
	}

	cmd.Status = "closed"
	tasks = cmd.filterTasks(h.getTasks())
	if len(tasks) != 1 {
		t.Fatalf("expected 1 closed task, got %d", len(tasks))
	}
}

func TestIntegration_ListFilter_Priority(t *testing.T) {
	h := newHarness(t)

	id1 := h.addTask("Default priority task")
	id2 := h.addTask("High priority task")
	h.addTask("Another default priority task")
	h.updatePriority(id2, "P0")

	// Default priority is P2
	cmd := &listCmd{globals: h.globals}
	cmd.Priority = "P2"
	tasks := cmd.filterTasks(h.getTasks())
	if len(tasks) != 2 {
		t.Fatalf("expected 2 P2 tasks, got %d", len(tasks))
	}

	cmd.Priority = "P0"
	tasks = cmd.filterTasks(h.getTasks())
	if len(tasks) != 1 {
		t.Fatalf("expected 1 P0 task, got %d", len(tasks))
	}
	if tasks[0].ID != id2 {
		t.Fatalf("expected task %d, got %d", id2, tasks[0].ID)
	}

	cmd.Priority = "P9"
	tasks = cmd.filterTasks(h.getTasks())
	if len(tasks) != 0 {
		t.Fatalf("expected 0 P9 tasks, got %d", len(tasks))
	}
	_ = id1
}

func TestIntegration_ListFilter_Type(t *testing.T) {
	h := newHarness(t)

	h.addTask("Default task")
	id2 := h.addTask("Bug task")
	h.store.Update(id2, func(t *meads.Task) {
		t.SetType("bug")
	})

	// Default type is "task"
	cmd := &listCmd{globals: h.globals}
	cmd.Type = "task"
	tasks := cmd.filterTasks(h.getTasks())
	if len(tasks) != 1 {
		t.Fatalf("expected 1 task-type task, got %d", len(tasks))
	}
	if tasks[0].Title != "Default task" {
		t.Fatalf("expected 'Default task', got %q", tasks[0].Title)
	}

	cmd.Type = "bug"
	tasks = cmd.filterTasks(h.getTasks())
	if len(tasks) != 1 {
		t.Fatalf("expected 1 bug task, got %d", len(tasks))
	}
	if tasks[0].Title != "Bug task" {
		t.Fatalf("expected 'Bug task', got %q", tasks[0].Title)
	}
}

func TestIntegration_ListFilter_Tag(t *testing.T) {
	h := newHarness(t)

	id1 := h.addTask("Tagged task")
	h.addTask("Untagged task")
	h.store.Update(id1, func(t *meads.Task) {
		t.SetTags([]string{"urgent", "frontend"})
	})

	cmd := &listCmd{globals: h.globals}
	cmd.Tag = "urgent"
	tasks := cmd.filterTasks(h.getTasks())
	if len(tasks) != 1 {
		t.Fatalf("expected 1 urgent task, got %d", len(tasks))
	}
	if tasks[0].Title != "Tagged task" {
		t.Fatalf("expected 'Tagged task', got %q", tasks[0].Title)
	}

	cmd.Tag = "frontend"
	tasks = cmd.filterTasks(h.getTasks())
	if len(tasks) != 1 {
		t.Fatalf("expected 1 frontend task, got %d", len(tasks))
	}

	cmd.Tag = "nonexistent"
	tasks = cmd.filterTasks(h.getTasks())
	if len(tasks) != 0 {
		t.Fatalf("expected 0 tasks with nonexistent tag, got %d", len(tasks))
	}
}

func TestIntegration_ListFilter_Combined(t *testing.T) {
	h := newHarness(t)

	id1 := h.addTask("Open bug P0")
	id2 := h.addTask("Open bug P1")
	id3 := h.addTask("Closed bug P0")
	h.addTask("Open task P0")

	h.store.Update(id1, func(t *meads.Task) {
		t.SetType("bug")
		t.SetPriority("P0")
	})
	h.store.Update(id2, func(t *meads.Task) {
		t.SetType("bug")
		t.SetPriority("P1")
	})
	h.store.Update(id3, func(t *meads.Task) {
		t.SetType("bug")
		t.SetPriority("P0")
		t.SetStatus("closed")
	})
	h.store.Update(4, func(t *meads.Task) {
		t.SetPriority("P0")
	})

	// Filter: open + bug + P0
	cmd := &listCmd{globals: h.globals}
	cmd.Status = "open"
	cmd.Type = "bug"
	cmd.Priority = "P0"
	tasks := cmd.filterTasks(h.getTasks())
	if len(tasks) != 1 {
		t.Fatalf("expected 1 task matching open+bug+P0, got %d", len(tasks))
	}
	if tasks[0].ID != id1 {
		t.Fatalf("expected task %d, got %d", id1, tasks[0].ID)
	}
}

func TestIntegration_ListFilter_NoFilters(t *testing.T) {
	h := newHarness(t)

	h.addTask("Task A")
	h.addTask("Task B")
	h.addTask("Task C")

	cmd := &listCmd{globals: h.globals}
	tasks := cmd.filterTasks(h.getTasks())
	if len(tasks) != 3 {
		t.Fatalf("expected 3 tasks with no filters, got %d", len(tasks))
	}
}

func TestIntegration_ReadyJSON(t *testing.T) {
	h := newHarness(t)

	h.addTask("Task A")
	parent := h.addTask("Task B")
	child := h.addTask("Task C")
	h.addDep(child, parent)

	// Capture stdout from readyCmd with JSON=true
	r, w, _ := os.Pipe()
	oldStdout := os.Stdout
	os.Stdout = w

	cmd := &readyCmd{globals: h.globals, JSON: true}
	if err := cmd.Run(); err != nil {
		os.Stdout = oldStdout
		t.Fatalf("readyCmd.Run: %v", err)
	}
	w.Close()
	os.Stdout = oldStdout

	var got []meads.Task
	if err := json.NewDecoder(r).Decode(&got); err != nil {
		t.Fatalf("failed to decode JSON: %v", err)
	}

	// Task A and Task B should be ready; Task C is blocked
	if len(got) != 2 {
		t.Fatalf("expected 2 ready tasks in JSON, got %d", len(got))
	}
	titles := map[string]bool{}
	for _, task := range got {
		titles[task.Title] = true
	}
	if !titles["Task A"] || !titles["Task B"] {
		t.Fatalf("expected Task A and Task B, got %v", titles)
	}
}

func TestIntegration_SetStatus_WithReason(t *testing.T) {
	h := newHarness(t)
	id := h.addTask("Deploy feature")
	cmd := &setStatusCmd{globals: h.globals, ID: "1", Status: "closed", Reason: "deployed to prod"}
	if err := cmd.Run(); err != nil {
		t.Fatalf("setStatusCmd.Run: %v", err)
	}
	task := h.getTask(id)
	if task.Status != "closed" {
		t.Fatalf("expected status closed, got %s", task.Status)
	}
	if task.StatusReason != "deployed to prod" {
		t.Fatalf("expected status_reason 'deployed to prod', got %q", task.StatusReason)
	}
}

func TestIntegration_Update_StatusReason(t *testing.T) {
	h := newHarness(t)
	id := h.addTask("Investigate issue")
	cmd := &updateCmd{globals: h.globals, ID: "1", Status: "closed", StatusReason: "duplicate"}
	if err := cmd.Run(); err != nil {
		t.Fatalf("updateCmd.Run: %v", err)
	}
	task := h.getTask(id)
	if task.Status != "closed" {
		t.Fatalf("expected status closed, got %s", task.Status)
	}
	if task.StatusReason != "duplicate" {
		t.Fatalf("expected status_reason 'duplicate', got %q", task.StatusReason)
	}
}

func TestIntegration_DeleteRecordsMaxIDForHighestID(t *testing.T) {
	h := newHarness(t)
	id := h.addTask("Important task")
	h.updatePriority(id, "P0")
	h.setStatus(id, "inprogress")
	h.deleteTask(id)

	h.assertTaskNotExists(id)

	// Markdown drops the row entirely but records the high-water mark
	// in project meta so the next add doesn't reuse the ID.
	content := h.tasksFileContent()
	if strings.Contains(content, "Important task") {
		t.Fatal("deleted task row should be removed from markdown file")
	}
	if !strings.Contains(content, "max-id: 1") {
		t.Fatalf("expected max-id meta for highest-deleted id, got:\n%s", content)
	}
}

func TestCSVIntegration_StatusReason(t *testing.T) {
	h := newCSVHarness(t)
	id := h.addTask("CSV status reason test")
	cmd := &setStatusCmd{globals: h.globals, ID: "1", Status: "closed", Reason: "won't fix"}
	if err := cmd.Run(); err != nil {
		t.Fatalf("setStatusCmd.Run: %v", err)
	}
	task := h.getTask(id)
	if task.StatusReason != "won't fix" {
		t.Fatalf("expected status_reason \"won't fix\", got %q", task.StatusReason)
	}
}

func TestCSVIntegration_DeletePreservesFields(t *testing.T) {
	h := newCSVHarness(t)
	id := h.addTask("CSV delete test")
	h.setStatus(id, "inprogress")
	h.deleteTask(id)

	// Task should not be visible via Get
	h.assertTaskNotExists(id)

	// Raw CSV should show deleted=true with original status preserved
	content := h.tasksFileContent()
	if !strings.Contains(content, "true") {
		t.Fatal("deleted task should have 'true' in the deleted column")
	}
	if !strings.Contains(content, "inprogress") {
		t.Fatal("deleted task should preserve original status in CSV")
	}
}
