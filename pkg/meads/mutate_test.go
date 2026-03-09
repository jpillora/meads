package meads

import (
	"strings"
	"testing"

	"github.com/go-git/go-billy/v5/util"
)

func TestDeleteMany_Basic(t *testing.T) {
	s := newTestStore(t, "")
	id1, _ := s.Add(Task{Title: "Task 1", Status: "open"})
	id2, _ := s.Add(Task{Title: "Task 2", Status: "open"})
	id3, _ := s.Add(Task{Title: "Task 3", Status: "open"})

	if err := s.DeleteMany([]int{id1, id3}); err != nil {
		t.Fatalf("DeleteMany: %v", err)
	}

	tasks, _ := s.Get(nil)
	if len(tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(tasks))
	}
	if tasks[0].ID != id2 {
		t.Fatalf("expected task %d, got %d", id2, tasks[0].ID)
	}
}

func TestDeleteMany_Empty(t *testing.T) {
	s := newTestStore(t, "")
	s.Add(Task{Title: "Task 1", Status: "open"})

	if err := s.DeleteMany(nil); err != nil {
		t.Fatalf("DeleteMany(nil): %v", err)
	}
	tasks, _ := s.Get(nil)
	if len(tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(tasks))
	}
}

func TestDeleteMany_NotFound(t *testing.T) {
	s := newTestStore(t, "")
	s.Add(Task{Title: "Task 1", Status: "open"})

	err := s.DeleteMany([]int{99})
	if err == nil {
		t.Fatal("expected error for missing task, got nil")
	}
	if !strings.Contains(err.Error(), "task 99 not found") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestDeleteMany_CleansDeps(t *testing.T) {
	s := newTestStore(t, "")
	id1, _ := s.Add(Task{Title: "Parent", Status: "closed"})
	id2, _ := s.Add(Task{Title: "Child", Status: "open"})
	s.Update(id2, func(t *Task) {
		t.AddDep(id1)
	})

	// Verify dep exists before delete
	tasks, _ := s.Get([]int{id2})
	if len(tasks[0].DependsOn) != 1 {
		t.Fatalf("expected 1 dep before delete, got %v", tasks[0].DependsOn)
	}

	if err := s.DeleteMany([]int{id1}); err != nil {
		t.Fatalf("DeleteMany: %v", err)
	}

	// Child should have dep cleaned up
	tasks, _ = s.Get([]int{id2})
	if len(tasks[0].DependsOn) != 0 {
		t.Fatalf("expected 0 deps after delete, got %v", tasks[0].DependsOn)
	}

	// Child should be updatable (no dangling dep error)
	if err := s.Update(id2, func(t *Task) {
		t.SetPriority("P1")
	}); err != nil {
		t.Fatalf("Update after dep cleanup failed: %v", err)
	}
}

func TestDeleteMany_PreservesRemainingDeps(t *testing.T) {
	s := newTestStore(t, "")
	id1, _ := s.Add(Task{Title: "Delete me", Status: "closed"})
	id2, _ := s.Add(Task{Title: "Keep me", Status: "open"})
	id3, _ := s.Add(Task{Title: "Child", Status: "open"})
	s.Update(id3, func(t *Task) {
		t.AddDep(id1)
		t.AddDep(id2)
	})

	if err := s.DeleteMany([]int{id1}); err != nil {
		t.Fatalf("DeleteMany: %v", err)
	}

	tasks, _ := s.Get([]int{id3})
	if len(tasks[0].DependsOn) != 1 || tasks[0].DependsOn[0] != id2 {
		t.Fatalf("expected depends-on [%d], got %v", id2, tasks[0].DependsOn)
	}
}

func TestDeleteMany_Atomic(t *testing.T) {
	// Verify that DeleteMany doesn't partially delete tasks on error.
	s := newTestStore(t, "")
	id1, _ := s.Add(Task{Title: "Task 1", Status: "open"})
	id2, _ := s.Add(Task{Title: "Task 2", Status: "open"})

	// Try to delete one valid and one invalid ID
	err := s.DeleteMany([]int{id1, 99})
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	// Both tasks should still exist (no partial deletion)
	tasks, err := s.Get(nil)
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

func TestNormalizePriority(t *testing.T) {
	tests := []struct {
		input string
		want  string
		err   bool
	}{
		{"P0", "P0", false},
		{"P9", "P9", false},
		{"p1", "P1", false},
		{"0", "P0", false},
		{"5", "P5", false},
		{" P3 ", "P3", false},
		{"P10", "", true},
		{"", "", true},
		{"banana", "", true},
		{"PP1", "", true},
		{"P", "", true},
		{"-1", "", true},
	}
	for _, tt := range tests {
		got, err := NormalizePriority(tt.input)
		if (err != nil) != tt.err {
			t.Errorf("NormalizePriority(%q): err=%v, wantErr=%v", tt.input, err, tt.err)
			continue
		}
		if got != tt.want {
			t.Errorf("NormalizePriority(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestUpdate_Description(t *testing.T) {
	s := newTestStore(t, "")
	// Create a task to update.
	id, err := s.Add(Task{Title: "Test task", Status: "open"})
	if err != nil {
		t.Fatalf("Add failed: %v", err)
	}
	// Update with a simple description.
	err = s.Update(id, func(t *Task) {
		t.Description = "simple description"
	})
	if err != nil {
		t.Fatalf("Update failed: %v", err)
	}
	tasks, err := s.Get([]int{id})
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if tasks[0].Description != "simple description" {
		t.Errorf("Description = %q, want %q", tasks[0].Description, "simple description")
	}
}

func TestUpdate_MultilineDescription(t *testing.T) {
	s := newTestStore(t, "")
	id, err := s.Add(Task{Title: "Crash report", Status: "open"})
	if err != nil {
		t.Fatalf("Add failed: %v", err)
	}
	multilineDesc := `rais-control-prod crashed with: panic: reflect: call of reflect.Value.Set on zero Value.

Stack trace:
- reflect.Value.Set (reflect/value.go:2126)
- encoding/json/v2.makeMapArshaler.func2 (arshal_default.go:820)
- encoding/json/v2.makeDefaultArshaler.makeStructArshaler.func6 (arshal_default.go:1142)

A nil map value inside the state struct causes reflect.Value.Set on a zero value.`

	err = s.Update(id, func(t *Task) {
		t.Description = multilineDesc
	})
	if err != nil {
		t.Fatalf("Update failed: %v", err)
	}
	// Read back and verify the multiline description survives the round-trip.
	tasks, err := s.Get([]int{id})
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if tasks[0].Description != multilineDesc {
		t.Errorf("Description round-trip failed.\ngot:\n%s\nwant:\n%s", tasks[0].Description, multilineDesc)
	}
	// Also verify the raw file contains the description text.
	data, err := util.ReadFile(s.fs, s.file)
	if err != nil {
		t.Fatalf("ReadFile failed: %v", err)
	}
	if !strings.Contains(string(data), "Stack trace:") {
		t.Error("raw file does not contain multiline description content")
	}
}

func TestAdd_NonZeroID(t *testing.T) {
	s := newTestStore(t, "")
	_, err := s.Add(Task{ID: 5, Title: "Bad", Status: "open"})
	if err == nil {
		t.Fatal("expected error for non-zero ID")
	}
	if !strings.Contains(err.Error(), "must not be set") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestAddMany_NonZeroID(t *testing.T) {
	s := newTestStore(t, "")
	_, err := s.AddMany([]Task{{ID: 1, Title: "Bad"}})
	if err == nil {
		t.Fatal("expected error for non-zero ID")
	}
	if !strings.Contains(err.Error(), "must not be set") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestAddMany_Empty(t *testing.T) {
	s := newTestStore(t, "")
	ids, err := s.AddMany(nil)
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if ids != nil {
		t.Fatalf("expected nil ids, got %v", ids)
	}
}

func TestAddMany_PreservesImportCreated(t *testing.T) {
	s := newCSVTestStore(t, "")
	tasks := []Task{
		{Title: "Imported", Status: "open", Meta: map[string]string{"created": "2020-01-01T00:00:00Z"}},
	}
	ids, err := s.AddMany(tasks)
	if err != nil {
		t.Fatalf("AddMany: %v", err)
	}
	got, _ := s.Get(ids)
	if got[0].Meta["created"] != "2020-01-01T00:00:00Z" {
		t.Errorf("created = %q, want preserved value", got[0].Meta["created"])
	}
}

func TestDelete_NotFound(t *testing.T) {
	s := newTestStore(t, "")
	s.Add(Task{Title: "Task 1", Status: "open"})

	err := s.Delete(99)
	if err == nil {
		t.Fatal("expected error for missing task")
	}
	if !strings.Contains(err.Error(), "task 99 not found") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestDelete_AlreadyDeleted(t *testing.T) {
	s := newCSVTestStore(t, "")
	s.Add(Task{Title: "Task 1", Status: "open"})
	id2, _ := s.Add(Task{Title: "Task 2", Status: "open"})

	// Delete task 1. Since task 2 has a higher ID, the tombstone for 1 gets pruned.
	s.Delete(1)

	// Task 1 no longer exists (pruned tombstone). Deleting again should fail.
	err := s.Delete(1)
	if err == nil {
		t.Fatal("expected error for pruned deleted task")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("unexpected error: %v", err)
	}
	_ = id2
}

func TestUpdate_NotFound(t *testing.T) {
	s := newTestStore(t, "")
	s.Add(Task{Title: "Task 1", Status: "open"})

	err := s.Update(99, func(t *Task) { t.SetStatus("closed") })
	if err == nil {
		t.Fatal("expected error for missing task")
	}
	if !strings.Contains(err.Error(), "task 99 not found") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestUpdate_DeletedTask(t *testing.T) {
	s := newCSVTestStore(t, "")
	id, _ := s.Add(Task{Title: "Task 1", Status: "open"})
	s.Delete(id)

	err := s.Update(id, func(t *Task) { t.SetStatus("closed") })
	if err == nil {
		t.Fatal("expected error for deleted task")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestUpdate_InvalidDep(t *testing.T) {
	s := newCSVTestStore(t, "")
	id, _ := s.Add(Task{Title: "Task 1", Status: "open"})

	err := s.Update(id, func(t *Task) {
		t.SetDependsOn([]int{99})
	})
	if err == nil {
		t.Fatal("expected error for invalid dep")
	}
	if !strings.Contains(err.Error(), "non-existent task 99") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestDelete_CleansDepOnOtherTasks(t *testing.T) {
	s := newCSVTestStore(t, "")
	id1, _ := s.Add(Task{Title: "Parent", Status: "closed"})
	id2, _ := s.Add(Task{Title: "Child", Status: "open"})
	s.Update(id2, func(t *Task) {
		t.AddDep(id1)
	})

	// Verify dep exists.
	tasks, _ := s.Get([]int{id2})
	if len(tasks[0].DependsOn) != 1 {
		t.Fatalf("expected 1 dep, got %v", tasks[0].DependsOn)
	}

	// Delete parent — dep should be cleaned from child.
	s.Delete(id1)
	tasks, _ = s.Get([]int{id2})
	if len(tasks[0].DependsOn) != 0 {
		t.Fatalf("expected 0 deps after delete, got %v", tasks[0].DependsOn)
	}
}

func TestDelete_MarkdownFormat(t *testing.T) {
	s := newTestStore(t, "")
	id1, _ := s.Add(Task{Title: "MD Task 1", Status: "open"})
	id2, _ := s.Add(Task{Title: "MD Task 2", Status: "open"})

	if err := s.Delete(id1); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	tasks, _ := s.Get(nil)
	if len(tasks) != 1 || tasks[0].ID != id2 {
		t.Fatalf("expected only task %d, got %v", id2, tasks)
	}
}

func TestUpdate_DescriptionReplace(t *testing.T) {
	s := newTestStore(t, "")
	id, err := s.Add(Task{Title: "Task with description", Status: "open", Description: "original description"})
	if err != nil {
		t.Fatalf("Add failed: %v", err)
	}
	// Replace the description via update.
	err = s.Update(id, func(t *Task) {
		t.Description = "replaced description"
	})
	if err != nil {
		t.Fatalf("Update failed: %v", err)
	}
	tasks, err := s.Get([]int{id})
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if tasks[0].Description != "replaced description" {
		t.Errorf("Description = %q, want %q", tasks[0].Description, "replaced description")
	}
}
