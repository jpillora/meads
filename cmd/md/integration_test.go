package main

import (
	"testing"
)

func TestIntegration_FullLifecycle(t *testing.T) {
	h := newHarness(t)

	// Add a task
	id := h.addTask("Implement feature X")
	h.assertTaskCount(1)
	h.assertTaskStatus(id, "open")

	// Close the task
	h.closeTask(id)
	h.assertTaskStatus(id, "closed")

	// Commit TASKS.md (simulates a normal workflow commit)
	h.commit("close task")

	// Run auto-delete (simulates post-commit hook)
	if err := h.runAutoDelete(); err != nil {
		t.Fatalf("runAutoDelete: %v", err)
	}

	// The closed task should be deleted
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

	// Task should still exist
	h.assertTaskCount(1)
	h.assertTaskExists(id)
}

func TestIntegration_AutoDelete_SkipsUncommittedChanges(t *testing.T) {
	h := newHarness(t)

	id1 := h.addTask("Task one")
	h.closeTask(id1)
	h.commit("close task one")

	// Add another task (makes TASKS.md dirty relative to HEAD)
	id2 := h.addTask("Task two")

	// Auto-delete should skip because TASKS.md has uncommitted changes
	if err := h.runAutoDelete(); err != nil {
		t.Fatalf("runAutoDelete: %v", err)
	}

	// Both tasks should still exist
	h.assertTaskCount(2)
	h.assertTaskExists(id1)
	h.assertTaskExists(id2)
}

func TestIntegration_AutoDelete_MultipleClosed(t *testing.T) {
	h := newHarness(t)

	id1 := h.addTask("Closed task A")
	id2 := h.addTask("Open task B")
	id3 := h.addTask("Closed task C")

	h.closeTask(id1)
	h.closeTask(id3)

	h.commit("close tasks")

	if err := h.runAutoDelete(); err != nil {
		t.Fatalf("runAutoDelete: %v", err)
	}

	// Only the open task should remain
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

	countBefore := h.commitCount()

	if err := h.runAutoDelete(); err != nil {
		t.Fatalf("runAutoDelete: %v", err)
	}

	// No amend should have happened, commit count unchanged
	countAfter := h.commitCount()
	if countAfter != countBefore {
		t.Fatalf("expected %d commits, got %d (amend happened when it shouldn't)", countBefore, countAfter)
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
