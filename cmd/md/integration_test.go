package main

import (
	"strings"
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

	h.commit("mixed statuses")

	if err := h.runAutoDelete(); err != nil {
		t.Fatalf("runAutoDelete: %v", err)
	}

	// Only closed tasks should be deleted
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

	h.commit("tasks with metadata")

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

	// Close and commit the parent
	h.closeTask(parent)
	h.commit("close parent")

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

func TestIntegration_AutoDelete_RestoresOnAmendFailure(t *testing.T) {
	h := newHarness(t)

	id1 := h.addTask("Open task")
	id2 := h.addTask("Closed task")
	h.closeTask(id2)
	h.commit("close task")

	contentBefore := h.tasksFileContent()

	// Install a pre-commit hook that blocks the amend
	h.installPreCommitHook("#!/bin/sh\nexit 1\n")

	err := h.runAutoDelete()
	if err == nil {
		t.Fatal("expected error from runAutoDelete, got nil")
	}

	// Remove hook so we can verify git state
	h.removePreCommitHook()

	// TASKS.md should be restored to pre-delete state
	contentAfter := h.tasksFileContent()
	if contentAfter != contentBefore {
		t.Fatal("TASKS.md content was not restored after amend failure")
	}

	// Both tasks should still exist
	h.assertTaskCount(2)
	h.assertTaskExists(id1)
	h.assertTaskExists(id2)

	// File should be clean vs git HEAD
	h.assertTasksFileClean()
}

func TestIntegration_AutoDelete_RestoresOnGitAddFailure(t *testing.T) {
	h := newHarness(t)

	id1 := h.addTask("Open task")
	id2 := h.addTask("Closed task")
	h.closeTask(id2)
	h.commit("close task")

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

func TestIntegration_AutoDelete_Idempotent(t *testing.T) {
	h := newHarness(t)

	id1 := h.addTask("Open task")
	id2 := h.addTask("Closed task")
	h.closeTask(id2)
	h.commit("close task")

	// First run: deletes closed task
	if err := h.runAutoDelete(); err != nil {
		t.Fatalf("first runAutoDelete: %v", err)
	}
	h.assertTaskCount(1)
	h.assertTaskExists(id1)
	h.assertTaskNotExists(id2)

	commitCountAfterFirst := h.commitCount()

	// Second run: no-op (no closed tasks remain)
	if err := h.runAutoDelete(); err != nil {
		t.Fatalf("second runAutoDelete: %v", err)
	}
	h.assertTaskCount(1)

	// No amend should have happened
	if h.commitCount() != commitCountAfterFirst {
		t.Fatal("second auto-delete should be a no-op but modified commits")
	}
}

func TestIntegration_AutoDelete_CommitIncludesDeletions(t *testing.T) {
	h := newHarness(t)

	h.addTask("Open task")
	id2 := h.addTask("Closed task")
	h.closeTask(id2)
	h.commit("close task")

	if err := h.runAutoDelete(); err != nil {
		t.Fatalf("runAutoDelete: %v", err)
	}

	// Verify the amended commit has the clean TASKS.md
	h.assertTasksFileClean()

	// Verify git show HEAD:TASKS.md does NOT contain the closed task
	showOutput := h.git("show", "HEAD:TASKS.md")
	if strings.Contains(showOutput, "Closed task") {
		t.Fatal("amended commit still contains deleted task")
	}
	if !strings.Contains(showOutput, "Open task") {
		t.Fatal("amended commit is missing the open task")
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
	h.commit("close parent")

	if err := h.runAutoDelete(); err != nil {
		t.Fatalf("runAutoDelete: %v", err)
	}

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

func TestIntegration_AutoDelete_StagedTASKSmd(t *testing.T) {
	h := newHarness(t)

	id := h.addTask("Closed task")
	h.closeTask(id)
	h.commit("close task")

	// Add a new task but only stage it (don't commit)
	h.addTask("Staged but not committed")
	h.git("add", "TASKS.md")

	// Auto-delete should skip because TASKS.md has staged changes
	if err := h.runAutoDelete(); err != nil {
		t.Fatalf("runAutoDelete: %v", err)
	}

	// Closed task should still exist (auto-delete was skipped)
	h.assertTaskExists(id)
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
