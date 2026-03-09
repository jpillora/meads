package meads

import (
	"fmt"
	"strings"
	"testing"

	"github.com/go-git/go-billy/v5/memfs"
	"github.com/go-git/go-billy/v5/util"
)

type fakeImporter struct {
	tasks []Task
}

func (f *fakeImporter) Name() string { return "fake" }
func (f *fakeImporter) Import() ([]Task, error) {
	return f.tasks, nil
}

func TestRunImport_NewTasks(t *testing.T) {
	s := newTestStore(t, "")
	imp := &fakeImporter{
		tasks: []Task{
			{Title: "Task A", Meta: map[string]string{"fake-id": "1", "status": "open"}, Status: "open"},
			{Title: "Task B", Meta: map[string]string{"fake-id": "2", "status": "open"}, Status: "open"},
		},
	}
	result, err := s.RunImport(imp)
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
	data, _ := util.ReadFile(s.fs, s.file)
	f := ParseFile(string(data))
	if len(f.Tasks) != 2 {
		t.Fatalf("expected 2 tasks in file, got %d", len(f.Tasks))
	}
}

func TestRunImport_Dedup(t *testing.T) {
	s := newTestStore(t, "")
	imp := &fakeImporter{
		tasks: []Task{
			{Title: "Task A", Meta: map[string]string{"fake-id": "1", "status": "open"}, Status: "open"},
			{Title: "Task B", Meta: map[string]string{"fake-id": "2", "status": "open"}, Status: "open"},
		},
	}
	// First import.
	_, err := s.RunImport(imp)
	if err != nil {
		t.Fatalf("first RunImport error: %v", err)
	}
	// Second import — same tasks, should all be skipped.
	result, err := s.RunImport(imp)
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
	data, _ := util.ReadFile(s.fs, s.file)
	f := ParseFile(string(data))
	if len(f.Tasks) != 2 {
		t.Fatalf("expected 2 tasks in file, got %d", len(f.Tasks))
	}
}

func TestRunImport_PartialDedup(t *testing.T) {
	s := newTestStore(t, "")
	imp1 := &fakeImporter{
		tasks: []Task{
			{Title: "Task A", Meta: map[string]string{"fake-id": "1", "status": "open"}, Status: "open"},
		},
	}
	_, err := s.RunImport(imp1)
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
	result, err := s.RunImport(imp2)
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

func TestGetImporter_Unknown(t *testing.T) {
	_, err := GetImporter("nonexistent-importer")
	if err == nil {
		t.Fatal("expected error for unknown importer")
	}
	if !strings.Contains(err.Error(), "unknown import target") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestGetImporter_Known(t *testing.T) {
	// "bead" is registered via init() in import_beads.go.
	imp, err := GetImporter("bead")
	if err != nil {
		t.Fatalf("GetImporter(bead): %v", err)
	}
	if imp.Name() != "bead" {
		t.Errorf("Name() = %q, want %q", imp.Name(), "bead")
	}
}

func TestRunImport_FileNotExist(t *testing.T) {
	// Store with no file — RunImport should create it.
	fs := memfs.New()
	s := NewStore(fs, "TASKS.md")
	imp := &fakeImporter{
		tasks: []Task{
			{Title: "New task", Meta: map[string]string{"fake-id": "1", "status": "open"}, Status: "open"},
		},
	}
	result, err := s.RunImport(imp)
	if err != nil {
		t.Fatalf("RunImport: %v", err)
	}
	if result.Imported != 1 {
		t.Errorf("Imported = %d, want 1", result.Imported)
	}
}

func TestRunImport_CSVMode(t *testing.T) {
	s := newCSVTestStore(t, "")
	imp := &fakeImporter{
		tasks: []Task{
			{Title: "CSV Task", Meta: map[string]string{"fake-id": "1", "status": "open"}, Status: "open"},
		},
	}
	result, err := s.RunImport(imp)
	if err != nil {
		t.Fatalf("RunImport: %v", err)
	}
	if result.Imported != 1 {
		t.Errorf("Imported = %d, want 1", result.Imported)
	}
	// Verify task written in CSV format.
	tasks, _ := s.Get(nil)
	if len(tasks) != 1 || tasks[0].Title != "CSV Task" {
		t.Errorf("unexpected tasks: %v", tasks)
	}
}

type errorImporter struct{}

func (e *errorImporter) Name() string               { return "error" }
func (e *errorImporter) Import() ([]Task, error)     { return nil, fmt.Errorf("import failed") }

func TestRunImport_ImportError(t *testing.T) {
	s := newTestStore(t, "")
	imp := &errorImporter{}
	_, err := s.RunImport(imp)
	if err == nil {
		t.Fatal("expected error from failed import")
	}
	if !strings.Contains(err.Error(), "import failed") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestRunImport_EmptyList(t *testing.T) {
	s := newTestStore(t, "")
	imp := &fakeImporter{tasks: []Task{}}
	result, err := s.RunImport(imp)
	if err != nil {
		t.Fatalf("RunImport error: %v", err)
	}
	if result.Imported != 0 || result.Skipped != 0 {
		t.Errorf("Imported=%d Skipped=%d, want 0/0", result.Imported, result.Skipped)
	}
}
