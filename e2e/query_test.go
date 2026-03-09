package e2e

import (
	"strings"
	"testing"

	"github.com/jpillora/meads/pkg/meads"
)

func TestGet_FileNotExist_NoIDs(t *testing.T) {
	s := meads.NewStore(newCSVStore(t).FS(), "NONEXISTENT.csv")
	tasks, err := s.Get(nil)
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if tasks != nil {
		t.Fatalf("expected nil tasks, got %v", tasks)
	}
}

func TestGet_FileNotExist_WithIDs(t *testing.T) {
	s := meads.NewStore(newCSVStore(t).FS(), "NONEXISTENT.csv")
	_, err := s.Get([]int{1})
	if err == nil || !strings.Contains(err.Error(), "task 1 not found") {
		t.Fatalf("expected 'task 1 not found', got %v", err)
	}
}

func TestGet_ByID_NotFound(t *testing.T) {
	s := newCSVStore(t)
	s.Add(meads.Task{Title: "Task 1", Status: "open"})
	_, err := s.Get([]int{99})
	if err == nil || !strings.Contains(err.Error(), "task 99 not found") {
		t.Fatalf("expected error, got %v", err)
	}
}

func TestGet_ByID_Found(t *testing.T) {
	s := newCSVStore(t)
	s.Add(meads.Task{Title: "Task A", Status: "open"})
	s.Add(meads.Task{Title: "Task B", Status: "open"})
	tasks, _ := s.Get([]int{2, 1})
	if len(tasks) != 2 || tasks[0].ID != 2 || tasks[1].ID != 1 {
		t.Errorf("wrong order: [%d, %d]", tasks[0].ID, tasks[1].ID)
	}
}

func TestGet_ExcludesDeleted(t *testing.T) {
	s := newCSVStore(t)
	s.Add(meads.Task{Title: "Task 1", Status: "open"})
	s.Add(meads.Task{Title: "Task 2", Status: "open"})
	s.Delete(1)
	tasks, _ := s.Get(nil)
	if len(tasks) != 1 || tasks[0].ID != 2 {
		t.Fatalf("expected task 2, got %v", tasks)
	}
}

func TestReady_FileNotExist(t *testing.T) {
	s := meads.NewStore(newCSVStore(t).FS(), "NONEXISTENT.csv")
	tasks, err := s.Ready()
	if err != nil || tasks != nil {
		t.Fatalf("expected nil/nil, got %v/%v", tasks, err)
	}
}

func TestReady_PrioritySorting(t *testing.T) {
	s := newCSVStore(t)
	s.Add(meads.Task{Title: "Low", Status: "open", Priority: "P9"})
	s.Add(meads.Task{Title: "High", Status: "open", Priority: "P0"})
	s.Add(meads.Task{Title: "Default1", Status: "open"})
	s.Add(meads.Task{Title: "Mid", Status: "open", Priority: "P5"})
	s.Add(meads.Task{Title: "Default2", Status: "open"})

	ready, _ := s.Ready()
	if len(ready) != 5 {
		t.Fatalf("expected 5, got %d", len(ready))
	}
	if ready[0].Priority != "P0" {
		t.Errorf("ready[0].Priority = %q, want P0", ready[0].Priority)
	}
	if ready[4].Priority != "P9" {
		t.Errorf("ready[4].Priority = %q, want P9", ready[4].Priority)
	}
}

func TestReady_ExcludesClosed(t *testing.T) {
	s := newCSVStore(t)
	s.Add(meads.Task{Title: "Open", Status: "open"})
	s.Add(meads.Task{Title: "Closed", Status: "closed"})
	s.Add(meads.Task{Title: "InProgress", Status: "inprogress"})
	ready, _ := s.Ready()
	if len(ready) != 1 || ready[0].Title != "Open" {
		t.Fatalf("expected only Open, got %v", ready)
	}
}

func TestReady_DepOnDeletedUnblocks(t *testing.T) {
	s := newCSVStore(t)
	id1, _ := s.Add(meads.Task{Title: "Parent", Status: "open"})
	id2, _ := s.Add(meads.Task{Title: "Child", Status: "open"})
	s.Update(id2, func(t *meads.Task) { t.AddDep(id1) })

	s.Delete(id1)
	ready, _ := s.Ready()
	if len(ready) != 1 || ready[0].ID != id2 {
		t.Fatalf("expected child ready, got %v", ready)
	}
}

func TestReady_DepOnClosedUnblocks(t *testing.T) {
	s := newCSVStore(t)
	id1, _ := s.Add(meads.Task{Title: "Parent", Status: "open"})
	id2, _ := s.Add(meads.Task{Title: "Child", Status: "open"})
	s.Update(id2, func(t *meads.Task) { t.AddDep(id1) })
	s.Update(id1, func(t *meads.Task) { t.SetStatus("closed") })

	ready, _ := s.Ready()
	if len(ready) != 1 || ready[0].ID != id2 {
		t.Fatalf("expected child ready, got %v", ready)
	}
}
