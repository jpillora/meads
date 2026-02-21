package meads

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDeleteMany_Basic(t *testing.T) {
	path := tempTaskFile(t, "")
	id1, _ := Add(path, Task{Title: "Task 1", Status: "open"})
	id2, _ := Add(path, Task{Title: "Task 2", Status: "open"})
	id3, _ := Add(path, Task{Title: "Task 3", Status: "open"})

	if err := DeleteMany(path, []int{id1, id3}); err != nil {
		t.Fatalf("DeleteMany: %v", err)
	}

	tasks, _ := Get(path, nil)
	if len(tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(tasks))
	}
	if tasks[0].ID != id2 {
		t.Fatalf("expected task %d, got %d", id2, tasks[0].ID)
	}
}

func TestDeleteMany_Empty(t *testing.T) {
	path := tempTaskFile(t, "")
	Add(path, Task{Title: "Task 1", Status: "open"})

	if err := DeleteMany(path, nil); err != nil {
		t.Fatalf("DeleteMany(nil): %v", err)
	}
	tasks, _ := Get(path, nil)
	if len(tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(tasks))
	}
}

func TestDeleteMany_NotFound(t *testing.T) {
	path := tempTaskFile(t, "")
	Add(path, Task{Title: "Task 1", Status: "open"})

	err := DeleteMany(path, []int{99})
	if err == nil {
		t.Fatal("expected error for missing task, got nil")
	}
	if !strings.Contains(err.Error(), "task 99 not found") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestDeleteMany_CleansDeps(t *testing.T) {
	path := tempTaskFile(t, "")
	id1, _ := Add(path, Task{Title: "Parent", Status: "closed"})
	id2, _ := Add(path, Task{Title: "Child", Status: "open"})
	Update(path, id2, func(t *Task) {
		t.AddDep(id1)
	})

	// Verify dep exists before delete
	tasks, _ := Get(path, []int{id2})
	if len(tasks[0].DependsOn) != 1 {
		t.Fatalf("expected 1 dep before delete, got %v", tasks[0].DependsOn)
	}

	if err := DeleteMany(path, []int{id1}); err != nil {
		t.Fatalf("DeleteMany: %v", err)
	}

	// Child should have dep cleaned up
	tasks, _ = Get(path, []int{id2})
	if len(tasks[0].DependsOn) != 0 {
		t.Fatalf("expected 0 deps after delete, got %v", tasks[0].DependsOn)
	}

	// Child should be updatable (no dangling dep error)
	if err := Update(path, id2, func(t *Task) {
		t.SetPriority("P1")
	}); err != nil {
		t.Fatalf("Update after dep cleanup failed: %v", err)
	}
}

func TestDeleteMany_PreservesRemainingDeps(t *testing.T) {
	path := tempTaskFile(t, "")
	id1, _ := Add(path, Task{Title: "Delete me", Status: "closed"})
	id2, _ := Add(path, Task{Title: "Keep me", Status: "open"})
	id3, _ := Add(path, Task{Title: "Child", Status: "open"})
	Update(path, id3, func(t *Task) {
		t.AddDep(id1)
		t.AddDep(id2)
	})

	if err := DeleteMany(path, []int{id1}); err != nil {
		t.Fatalf("DeleteMany: %v", err)
	}

	tasks, _ := Get(path, []int{id3})
	if len(tasks[0].DependsOn) != 1 || tasks[0].DependsOn[0] != id2 {
		t.Fatalf("expected depends-on [%d], got %v", id2, tasks[0].DependsOn)
	}
}

func TestDeleteMany_Atomic(t *testing.T) {
	// Verify that DeleteMany doesn't partially delete tasks on error.
	path := filepath.Join(t.TempDir(), "TASKS.md")
	os.WriteFile(path, []byte(""), 0644)
	id1, _ := Add(path, Task{Title: "Task 1", Status: "open"})
	id2, _ := Add(path, Task{Title: "Task 2", Status: "open"})

	// Try to delete one valid and one invalid ID
	err := DeleteMany(path, []int{id1, 99})
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	// Both tasks should still exist (no partial deletion)
	tasks, err := Get(path, nil)
	if err != nil {
		t.Fatalf("Get after failed DeleteMany: %v", err)
	}
	if len(tasks) != 2 {
		t.Fatalf("expected 2 tasks after failed delete, got %d", len(tasks))
	}
	ids := map[int]bool{}
	for _, task := range tasks {
		ids[task.ID] = true
	}
	if !ids[id1] || !ids[id2] {
		t.Fatalf("expected tasks %d and %d to exist, got %v", id1, id2, ids)
	}
}

func TestUpdate_Description(t *testing.T) {
	path := tempTaskFile(t, "")
	// Create a task to update.
	id, err := Add(path, Task{Title: "Test task", Status: "open"})
	if err != nil {
		t.Fatalf("Add failed: %v", err)
	}
	// Update with a simple description.
	err = Update(path, id, func(t *Task) {
		t.Description = "simple description"
	})
	if err != nil {
		t.Fatalf("Update failed: %v", err)
	}
	tasks, err := Get(path, []int{id})
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if tasks[0].Description != "simple description" {
		t.Errorf("Description = %q, want %q", tasks[0].Description, "simple description")
	}
}

func TestUpdate_MultilineDescription(t *testing.T) {
	path := tempTaskFile(t, "")
	id, err := Add(path, Task{Title: "Crash report", Status: "open"})
	if err != nil {
		t.Fatalf("Add failed: %v", err)
	}
	multilineDesc := `rais-control-prod crashed with: panic: reflect: call of reflect.Value.Set on zero Value.

Stack trace:
- reflect.Value.Set (reflect/value.go:2126)
- encoding/json/v2.makeMapArshaler.func2 (arshal_default.go:820)
- encoding/json/v2.makeDefaultArshaler.makeStructArshaler.func6 (arshal_default.go:1142)

A nil map value inside the state struct causes reflect.Value.Set on a zero value.`

	err = Update(path, id, func(t *Task) {
		t.Description = multilineDesc
	})
	if err != nil {
		t.Fatalf("Update failed: %v", err)
	}
	// Read back and verify the multiline description survives the round-trip.
	tasks, err := Get(path, []int{id})
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if tasks[0].Description != multilineDesc {
		t.Errorf("Description round-trip failed.\ngot:\n%s\nwant:\n%s", tasks[0].Description, multilineDesc)
	}
	// Also verify the raw file contains the description text.
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile failed: %v", err)
	}
	if !strings.Contains(string(data), "Stack trace:") {
		t.Error("raw file does not contain multiline description content")
	}
}

func TestUpdate_DescriptionReplace(t *testing.T) {
	path := tempTaskFile(t, "")
	id, err := Add(path, Task{Title: "Task with description", Status: "open", Description: "original description"})
	if err != nil {
		t.Fatalf("Add failed: %v", err)
	}
	// Replace the description via update.
	err = Update(path, id, func(t *Task) {
		t.Description = "replaced description"
	})
	if err != nil {
		t.Fatalf("Update failed: %v", err)
	}
	tasks, err := Get(path, []int{id})
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if tasks[0].Description != "replaced description" {
		t.Errorf("Description = %q, want %q", tasks[0].Description, "replaced description")
	}
}
