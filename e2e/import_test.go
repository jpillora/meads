package e2e

import (
	"fmt"
	"strings"
	"testing"

	"github.com/go-git/go-billy/v5/memfs"
	"github.com/go-git/go-billy/v5/util"
	"github.com/jpillora/meads/pkg/meads"
)

type fakeImporter struct {
	tasks []meads.Task
}

func (f *fakeImporter) Name() string                     { return "fake" }
func (f *fakeImporter) Import() ([]meads.Task, error) { return f.tasks, nil }

type errorImporter struct{}

func (e *errorImporter) Name() string                     { return "error" }
func (e *errorImporter) Import() ([]meads.Task, error) { return nil, fmt.Errorf("import failed") }

func TestGetImporter_Unknown(t *testing.T) {
	_, err := meads.GetImporter("nonexistent-importer")
	if err == nil || !strings.Contains(err.Error(), "unknown import target") {
		t.Fatalf("expected error, got %v", err)
	}
}

func TestGetImporter_Known(t *testing.T) {
	imp, err := meads.GetImporter("bead")
	if err != nil {
		t.Fatalf("GetImporter(bead): %v", err)
	}
	if imp.Name() != "bead" {
		t.Errorf("Name() = %q", imp.Name())
	}
}

func TestRunImport_NewTasks(t *testing.T) {
	s := newMDStore(t)
	imp := &fakeImporter{tasks: []meads.Task{
		{Title: "Task A", Meta: map[string]string{"fake-id": "1", "status": "open"}, Status: "open"},
		{Title: "Task B", Meta: map[string]string{"fake-id": "2", "status": "open"}, Status: "open"},
	}}
	result, err := s.RunImport(imp)
	if err != nil {
		t.Fatalf("RunImport: %v", err)
	}
	if result.Imported != 2 || result.Skipped != 0 {
		t.Errorf("Imported=%d Skipped=%d", result.Imported, result.Skipped)
	}
	data, _ := util.ReadFile(s.FS(), s.Path())
	f := meads.ParseFile(string(data))
	if len(f.Tasks) != 2 {
		t.Fatalf("expected 2 tasks, got %d", len(f.Tasks))
	}
}

func TestRunImport_Dedup(t *testing.T) {
	s := newMDStore(t)
	imp := &fakeImporter{tasks: []meads.Task{
		{Title: "Task A", Meta: map[string]string{"fake-id": "1", "status": "open"}, Status: "open"},
	}}
	s.RunImport(imp)
	result, _ := s.RunImport(imp)
	if result.Imported != 0 || result.Skipped != 1 {
		t.Errorf("Imported=%d Skipped=%d", result.Imported, result.Skipped)
	}
}

func TestRunImport_FileNotExist(t *testing.T) {
	fs := memfs.New()
	s := meads.NewStore(fs, "TASKS.md")
	imp := &fakeImporter{tasks: []meads.Task{
		{Title: "New task", Meta: map[string]string{"fake-id": "1", "status": "open"}, Status: "open"},
	}}
	result, err := s.RunImport(imp)
	if err != nil {
		t.Fatalf("RunImport: %v", err)
	}
	if result.Imported != 1 {
		t.Errorf("Imported = %d, want 1", result.Imported)
	}
}

func TestRunImport_CSVMode(t *testing.T) {
	s := newCSVStore(t)
	imp := &fakeImporter{tasks: []meads.Task{
		{Title: "CSV Task", Meta: map[string]string{"fake-id": "1", "status": "open"}, Status: "open"},
	}}
	result, _ := s.RunImport(imp)
	if result.Imported != 1 {
		t.Errorf("Imported = %d", result.Imported)
	}
	tasks, _ := s.Get(nil)
	if len(tasks) != 1 || tasks[0].Title != "CSV Task" {
		t.Errorf("unexpected tasks: %v", tasks)
	}
}

func TestRunImport_ImportError(t *testing.T) {
	s := newMDStore(t)
	_, err := s.RunImport(&errorImporter{})
	if err == nil || !strings.Contains(err.Error(), "import failed") {
		t.Fatalf("expected error, got %v", err)
	}
}

func TestRunImport_EmptyList(t *testing.T) {
	s := newMDStore(t)
	result, err := s.RunImport(&fakeImporter{tasks: []meads.Task{}})
	if err != nil {
		t.Fatalf("RunImport: %v", err)
	}
	if result.Imported != 0 || result.Skipped != 0 {
		t.Errorf("Imported=%d Skipped=%d", result.Imported, result.Skipped)
	}
}
