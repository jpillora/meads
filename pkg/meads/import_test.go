package meads

import (
	"os"
	"path/filepath"
	"testing"
)

type fakeImporter struct {
	tasks []Task
}

func (f *fakeImporter) Name() string { return "fake" }
func (f *fakeImporter) Import() ([]Task, error) {
	return f.tasks, nil
}

func TestRunImport_NewTasks(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "TASKS.md")
	imp := &fakeImporter{
		tasks: []Task{
			{Title: "Task A", Meta: map[string]string{"fake-id": "1", "status": "open"}, Status: "open"},
			{Title: "Task B", Meta: map[string]string{"fake-id": "2", "status": "open"}, Status: "open"},
		},
	}
	result, err := RunImport(file, imp)
	if err != nil {
		t.Fatalf("RunImport error: %v", err)
	}
	if result.Imported != 2 {
		t.Errorf("Imported = %d, want 2", result.Imported)
	}
	if result.Skipped != 0 {
		t.Errorf("Skipped = %d, want 0", result.Skipped)
	}
	// Verify tasks were written.
	data, _ := os.ReadFile(file)
	f := ParseFile(string(data))
	if len(f.Tasks) != 2 {
		t.Fatalf("expected 2 tasks in file, got %d", len(f.Tasks))
	}
}

func TestRunImport_Dedup(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "TASKS.md")
	imp := &fakeImporter{
		tasks: []Task{
			{Title: "Task A", Meta: map[string]string{"fake-id": "1", "status": "open"}, Status: "open"},
			{Title: "Task B", Meta: map[string]string{"fake-id": "2", "status": "open"}, Status: "open"},
		},
	}
	// First import.
	_, err := RunImport(file, imp)
	if err != nil {
		t.Fatalf("first RunImport error: %v", err)
	}
	// Second import — same tasks, should all be skipped.
	result, err := RunImport(file, imp)
	if err != nil {
		t.Fatalf("second RunImport error: %v", err)
	}
	if result.Imported != 0 {
		t.Errorf("Imported = %d, want 0", result.Imported)
	}
	if result.Skipped != 2 {
		t.Errorf("Skipped = %d, want 2", result.Skipped)
	}
	// Verify still only 2 tasks.
	data, _ := os.ReadFile(file)
	f := ParseFile(string(data))
	if len(f.Tasks) != 2 {
		t.Fatalf("expected 2 tasks in file, got %d", len(f.Tasks))
	}
}

func TestRunImport_PartialDedup(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "TASKS.md")
	imp1 := &fakeImporter{
		tasks: []Task{
			{Title: "Task A", Meta: map[string]string{"fake-id": "1", "status": "open"}, Status: "open"},
		},
	}
	_, err := RunImport(file, imp1)
	if err != nil {
		t.Fatalf("first RunImport error: %v", err)
	}
	// Second import has one existing and one new.
	imp2 := &fakeImporter{
		tasks: []Task{
			{Title: "Task A", Meta: map[string]string{"fake-id": "1", "status": "open"}, Status: "open"},
			{Title: "Task C", Meta: map[string]string{"fake-id": "3", "status": "open"}, Status: "open"},
		},
	}
	result, err := RunImport(file, imp2)
	if err != nil {
		t.Fatalf("second RunImport error: %v", err)
	}
	if result.Imported != 1 {
		t.Errorf("Imported = %d, want 1", result.Imported)
	}
	if result.Skipped != 1 {
		t.Errorf("Skipped = %d, want 1", result.Skipped)
	}
}

func TestRunImport_EmptyList(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "TASKS.md")
	imp := &fakeImporter{tasks: []Task{}}
	result, err := RunImport(file, imp)
	if err != nil {
		t.Fatalf("RunImport error: %v", err)
	}
	if result.Imported != 0 || result.Skipped != 0 {
		t.Errorf("Imported=%d Skipped=%d, want 0/0", result.Imported, result.Skipped)
	}
}
