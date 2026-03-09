package meads

import (
	"strings"
	"testing"
)

func TestGet_FileNotExist_NoIDs(t *testing.T) {
	s := newCSVTestStore(t, "") // no file written
	// Remove the file to ensure it doesn't exist.
	s = NewStore(s.fs, "NONEXISTENT.csv")

	tasks, err := s.Get(nil)
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if tasks != nil {
		t.Fatalf("expected nil tasks, got %v", tasks)
	}
}

func TestGet_FileNotExist_WithIDs(t *testing.T) {
	s := NewStore(newCSVTestStore(t, "").fs, "NONEXISTENT.csv")

	_, err := s.Get([]int{1})
	if err == nil {
		t.Fatal("expected error for missing file with IDs")
	}
	if !strings.Contains(err.Error(), "task 1 not found") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestGet_ByID_NotFound(t *testing.T) {
	s := newCSVTestStore(t, "")
	s.Add(Task{Title: "Task 1", Status: "open"})

	_, err := s.Get([]int{99})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "task 99 not found") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestGet_ByID_Found(t *testing.T) {
	s := newCSVTestStore(t, "")
	s.Add(Task{Title: "Task A", Status: "open"})
	s.Add(Task{Title: "Task B", Status: "open"})

	tasks, err := s.Get([]int{2, 1})
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if len(tasks) != 2 {
		t.Fatalf("expected 2 tasks, got %d", len(tasks))
	}
	// Order should match requested IDs.
	if tasks[0].ID != 2 || tasks[1].ID != 1 {
		t.Errorf("tasks returned in wrong order: [%d, %d]", tasks[0].ID, tasks[1].ID)
	}
}

func TestGet_ExcludesDeleted(t *testing.T) {
	s := newCSVTestStore(t, "")
	s.Add(Task{Title: "Task 1", Status: "open"})
	s.Add(Task{Title: "Task 2", Status: "open"})
	s.Delete(1)

	tasks, err := s.Get(nil)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(tasks))
	}
	if tasks[0].ID != 2 {
		t.Errorf("expected task 2, got task %d", tasks[0].ID)
	}
}

func TestReady_FileNotExist(t *testing.T) {
	s := NewStore(newCSVTestStore(t, "").fs, "NONEXISTENT.csv")

	tasks, err := s.Ready()
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if tasks != nil {
		t.Fatalf("expected nil tasks, got %v", tasks)
	}
}

func TestReady_PrioritySorting(t *testing.T) {
	s := newCSVTestStore(t, "")
	s.Add(Task{Title: "Low", Status: "open", Priority: "P9"})
	s.Add(Task{Title: "High", Status: "open", Priority: "P0"})
	s.Add(Task{Title: "Default1", Status: "open"}) // P2 default in sorting
	s.Add(Task{Title: "Mid", Status: "open", Priority: "P5"})
	s.Add(Task{Title: "Default2", Status: "open"}) // Another empty priority

	ready, err := s.Ready()
	if err != nil {
		t.Fatalf("Ready: %v", err)
	}
	if len(ready) != 5 {
		t.Fatalf("expected 5 ready tasks, got %d", len(ready))
	}
	// Verify sorted order: P0, empty(P2), empty(P2), P5, P9.
	if ready[0].Priority != "P0" {
		t.Errorf("ready[0].Priority = %q, want P0", ready[0].Priority)
	}
	if ready[4].Priority != "P9" {
		t.Errorf("ready[4].Priority = %q, want P9", ready[4].Priority)
	}
	// Both empty-priority tasks should sort between P0 and P5.
	for i := 1; i <= 2; i++ {
		if ready[i].Priority != "" {
			t.Errorf("ready[%d].Priority = %q, want empty (default P2)", i, ready[i].Priority)
		}
	}
	if ready[3].Priority != "P5" {
		t.Errorf("ready[3].Priority = %q, want P5", ready[3].Priority)
	}
}

func TestReady_ExcludesClosed(t *testing.T) {
	s := newCSVTestStore(t, "")
	s.Add(Task{Title: "Open", Status: "open"})
	s.Add(Task{Title: "Closed", Status: "closed"})
	s.Add(Task{Title: "InProgress", Status: "inprogress"})

	ready, err := s.Ready()
	if err != nil {
		t.Fatalf("Ready: %v", err)
	}
	if len(ready) != 1 {
		t.Fatalf("expected 1 ready task, got %d", len(ready))
	}
	if ready[0].Title != "Open" {
		t.Errorf("expected Open task, got %q", ready[0].Title)
	}
}

func TestReady_DepOnDeletedUnblocks(t *testing.T) {
	s := newCSVTestStore(t, "")
	id1, _ := s.Add(Task{Title: "Parent", Status: "open"})
	id2, _ := s.Add(Task{Title: "Child", Status: "open"})
	s.Update(id2, func(t *Task) {
		t.AddDep(id1)
	})

	// Child is blocked by parent.
	ready, _ := s.Ready()
	if len(ready) != 1 || ready[0].ID != id1 {
		t.Fatalf("expected only parent ready, got %v", ready)
	}

	// Delete parent — child's dep gets cleaned, should become ready.
	s.Delete(id1)
	ready, err := s.Ready()
	if err != nil {
		t.Fatalf("Ready: %v", err)
	}
	if len(ready) != 1 || ready[0].ID != id2 {
		t.Fatalf("expected child ready after parent deleted, got %v", ready)
	}
}

func TestReady_DepOnClosedUnblocks(t *testing.T) {
	s := newCSVTestStore(t, "")
	id1, _ := s.Add(Task{Title: "Parent", Status: "open"})
	id2, _ := s.Add(Task{Title: "Child", Status: "open"})
	s.Update(id2, func(t *Task) {
		t.AddDep(id1)
	})

	// Close parent — child should become unblocked.
	s.Update(id1, func(t *Task) {
		t.SetStatus("closed")
	})

	ready, err := s.Ready()
	if err != nil {
		t.Fatalf("Ready: %v", err)
	}
	if len(ready) != 1 || ready[0].ID != id2 {
		t.Fatalf("expected child ready after parent closed, got %v", ready)
	}
}
