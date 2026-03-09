package e2e

import (
	"strings"
	"testing"

	"github.com/go-git/go-billy/v5/util"
	"github.com/jpillora/meads/pkg/meads"
)

func TestAdd_NonZeroID(t *testing.T) {
	s := newMDStore(t)
	_, err := s.Add(meads.Task{ID: 5, Title: "Bad", Status: "open"})
	if err == nil || !strings.Contains(err.Error(), "must not be set") {
		t.Fatalf("expected 'must not be set' error, got %v", err)
	}
}

func TestAddMany_NonZeroID(t *testing.T) {
	s := newMDStore(t)
	_, err := s.AddMany([]meads.Task{{ID: 1, Title: "Bad"}})
	if err == nil || !strings.Contains(err.Error(), "must not be set") {
		t.Fatalf("expected 'must not be set' error, got %v", err)
	}
}

func TestAddMany_Empty(t *testing.T) {
	s := newMDStore(t)
	ids, err := s.AddMany(nil)
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if ids != nil {
		t.Fatalf("expected nil ids, got %v", ids)
	}
}

func TestAddMany_PreservesImportCreated(t *testing.T) {
	s := newCSVStore(t)
	tasks := []meads.Task{
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
	s := newMDStore(t)
	s.Add(meads.Task{Title: "Task 1", Status: "open"})
	err := s.Delete(99)
	if err == nil || !strings.Contains(err.Error(), "task 99 not found") {
		t.Fatalf("expected 'task 99 not found', got %v", err)
	}
}

func TestDelete_AlreadyDeleted(t *testing.T) {
	s := newCSVStore(t)
	s.Add(meads.Task{Title: "Task 1", Status: "open"})
	s.Add(meads.Task{Title: "Task 2", Status: "open"})
	s.Delete(1)

	err := s.Delete(1)
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("expected 'not found' error, got %v", err)
	}
}

func TestDelete_CleansDepOnOtherTasks(t *testing.T) {
	s := newCSVStore(t)
	id1, _ := s.Add(meads.Task{Title: "Parent", Status: "closed"})
	id2, _ := s.Add(meads.Task{Title: "Child", Status: "open"})
	s.Update(id2, func(t *meads.Task) { t.AddDep(id1) })

	s.Delete(id1)
	tasks, _ := s.Get([]int{id2})
	if len(tasks[0].DependsOn) != 0 {
		t.Fatalf("expected 0 deps after delete, got %v", tasks[0].DependsOn)
	}
}

func TestDelete_MarkdownFormat(t *testing.T) {
	s := newMDStore(t)
	id1, _ := s.Add(meads.Task{Title: "MD Task 1", Status: "open"})
	id2, _ := s.Add(meads.Task{Title: "MD Task 2", Status: "open"})
	s.Delete(id1)
	tasks, _ := s.Get(nil)
	if len(tasks) != 1 || tasks[0].ID != id2 {
		t.Fatalf("expected only task %d, got %v", id2, tasks)
	}
}

func TestUpdate_NotFound(t *testing.T) {
	s := newMDStore(t)
	s.Add(meads.Task{Title: "Task 1", Status: "open"})
	err := s.Update(99, func(t *meads.Task) { t.SetStatus("closed") })
	if err == nil || !strings.Contains(err.Error(), "task 99 not found") {
		t.Fatalf("expected 'task 99 not found', got %v", err)
	}
}

func TestUpdate_DeletedTask(t *testing.T) {
	s := newCSVStore(t)
	id, _ := s.Add(meads.Task{Title: "Task 1", Status: "open"})
	s.Delete(id)
	err := s.Update(id, func(t *meads.Task) { t.SetStatus("closed") })
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("expected 'not found' error, got %v", err)
	}
}

func TestUpdate_InvalidDep(t *testing.T) {
	s := newCSVStore(t)
	id, _ := s.Add(meads.Task{Title: "Task 1", Status: "open"})
	err := s.Update(id, func(t *meads.Task) { t.SetDependsOn([]int{99}) })
	if err == nil || !strings.Contains(err.Error(), "non-existent task 99") {
		t.Fatalf("expected dep error, got %v", err)
	}
}

func TestUpdate_Description(t *testing.T) {
	s := newMDStore(t)
	id, _ := s.Add(meads.Task{Title: "Test task", Status: "open"})
	s.Update(id, func(t *meads.Task) { t.Description = "simple description" })
	tasks, _ := s.Get([]int{id})
	if tasks[0].Description != "simple description" {
		t.Errorf("Description = %q", tasks[0].Description)
	}
}

func TestUpdate_MultilineDescription(t *testing.T) {
	s := newMDStore(t)
	id, _ := s.Add(meads.Task{Title: "Crash report", Status: "open"})
	multilineDesc := "rais-control-prod crashed with: panic.\n\nStack trace:\n- reflect.Value.Set\n- encoding/json"
	s.Update(id, func(t *meads.Task) { t.Description = multilineDesc })
	tasks, _ := s.Get([]int{id})
	if tasks[0].Description != multilineDesc {
		t.Errorf("Description round-trip failed")
	}
	data, _ := util.ReadFile(s.FS(), s.Path())
	if !strings.Contains(string(data), "Stack trace:") {
		t.Error("raw file does not contain multiline description content")
	}
}

func TestUpdate_DescriptionReplace(t *testing.T) {
	s := newMDStore(t)
	id, _ := s.Add(meads.Task{Title: "Task with description", Status: "open", Description: "original description"})
	s.Update(id, func(t *meads.Task) { t.Description = "replaced description" })
	tasks, _ := s.Get([]int{id})
	if tasks[0].Description != "replaced description" {
		t.Errorf("Description = %q", tasks[0].Description)
	}
}

func TestDeleteMany_Basic(t *testing.T) {
	s := newMDStore(t)
	id1, _ := s.Add(meads.Task{Title: "Task 1", Status: "open"})
	id2, _ := s.Add(meads.Task{Title: "Task 2", Status: "open"})
	id3, _ := s.Add(meads.Task{Title: "Task 3", Status: "open"})
	s.DeleteMany([]int{id1, id3})
	tasks, _ := s.Get(nil)
	if len(tasks) != 1 || tasks[0].ID != id2 {
		t.Fatalf("expected task %d, got %v", id2, tasks)
	}
}

func TestDeleteMany_Empty(t *testing.T) {
	s := newMDStore(t)
	s.Add(meads.Task{Title: "Task 1", Status: "open"})
	if err := s.DeleteMany(nil); err != nil {
		t.Fatalf("DeleteMany(nil): %v", err)
	}
}

func TestDeleteMany_NotFound(t *testing.T) {
	s := newMDStore(t)
	s.Add(meads.Task{Title: "Task 1", Status: "open"})
	err := s.DeleteMany([]int{99})
	if err == nil || !strings.Contains(err.Error(), "task 99 not found") {
		t.Fatalf("expected error, got %v", err)
	}
}

func TestDeleteMany_CleansDeps(t *testing.T) {
	s := newMDStore(t)
	id1, _ := s.Add(meads.Task{Title: "Parent", Status: "closed"})
	id2, _ := s.Add(meads.Task{Title: "Child", Status: "open"})
	s.Update(id2, func(t *meads.Task) { t.AddDep(id1) })
	s.DeleteMany([]int{id1})
	tasks, _ := s.Get([]int{id2})
	if len(tasks[0].DependsOn) != 0 {
		t.Fatalf("expected 0 deps, got %v", tasks[0].DependsOn)
	}
}
