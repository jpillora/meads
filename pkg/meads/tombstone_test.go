package meads

import (
	"strings"
	"testing"

	"github.com/go-git/go-billy/v5/memfs"
	"github.com/go-git/go-billy/v5/util"
)

func TestNextID_Empty(t *testing.T) {
	f := &File{Tasks: []Task{}}
	if got := nextID(f); got != 1 {
		t.Errorf("nextID on empty = %d, want 1", got)
	}
}

func TestNextID_WithTasks(t *testing.T) {
	f := &File{Tasks: []Task{
		{ID: 3, Status: "open"},
		{ID: 1, Status: "open"},
		{ID: 5, Status: "deleted"},
	}}
	if got := nextID(f); got != 6 {
		t.Errorf("nextID = %d, want 6", got)
	}
}

func TestValidateDeps_Valid(t *testing.T) {
	f := &File{Tasks: []Task{
		{ID: 1, Status: "open"},
		{ID: 2, Status: "open", DependsOn: []int{1}},
	}}
	if err := validateDeps(f); err != nil {
		t.Errorf("expected nil error, got %v", err)
	}
}

func TestValidateDeps_Invalid(t *testing.T) {
	f := &File{Tasks: []Task{
		{ID: 1, Status: "open", DependsOn: []int{99}},
	}}
	err := validateDeps(f)
	if err == nil {
		t.Fatal("expected error for invalid dep")
	}
	if !strings.Contains(err.Error(), "non-existent task 99") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidateDeps_SkipsDeleted(t *testing.T) {
	// A deleted task's deps should not be validated.
	f := &File{Tasks: []Task{
		{ID: 1, Status: "deleted", DependsOn: []int{99}},
		{ID: 2, Status: "open"},
	}}
	if err := validateDeps(f); err != nil {
		t.Errorf("expected nil (deleted tasks skipped), got %v", err)
	}
}

func TestValidateDeps_DepOnDeletedIsInvalid(t *testing.T) {
	// An active task depending on a deleted task is invalid.
	f := &File{Tasks: []Task{
		{ID: 1, Status: "deleted"},
		{ID: 2, Status: "open", DependsOn: []int{1}},
	}}
	err := validateDeps(f)
	if err == nil {
		t.Fatal("expected error for dep on deleted task")
	}
}

func TestFilterDeleted(t *testing.T) {
	tasks := []Task{
		{ID: 1, Status: "open"},
		{ID: 2, Status: "deleted"},
		{ID: 3, Status: "closed"},
	}
	filtered := filterDeleted(tasks)
	if len(filtered) != 2 {
		t.Fatalf("expected 2, got %d", len(filtered))
	}
	for _, task := range filtered {
		if task.Status == "deleted" {
			t.Errorf("deleted task %d should be filtered", task.ID)
		}
	}
}

func TestEnsureProjectMeta_CreatesMap(t *testing.T) {
	f := &File{}
	ensureProjectMeta(f, "2026-01-01T00:00:00Z")
	if f.Meta == nil {
		t.Fatal("Meta should be initialized")
	}
	if f.Meta["created"] != "2026-01-01T00:00:00Z" {
		t.Errorf("Meta[created] = %q", f.Meta["created"])
	}
}

func TestEnsureProjectMeta_Idempotent(t *testing.T) {
	f := &File{Meta: map[string]string{"created": "2025-01-01T00:00:00Z"}}
	ensureProjectMeta(f, "2026-01-01T00:00:00Z")
	// Should not overwrite existing created.
	if f.Meta["created"] != "2025-01-01T00:00:00Z" {
		t.Errorf("Meta[created] = %q, want preserved value", f.Meta["created"])
	}
}

func TestEnsureFile_CreatesFile(t *testing.T) {
	fs := memfs.New()
	s := NewStore(fs, "TASKS.csv")

	if err := s.ensureFile(); err != nil {
		t.Fatalf("ensureFile: %v", err)
	}

	data, err := util.ReadFile(fs, "TASKS.csv")
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(data) != csvHeaderRow() {
		t.Errorf("content = %q, want CSV header", string(data))
	}
}

func TestEnsureFile_ExistingNoop(t *testing.T) {
	fs := memfs.New()
	existing := "existing content\n"
	util.WriteFile(fs, "TASKS.csv", []byte(existing), 0644)
	s := NewStore(fs, "TASKS.csv")

	if err := s.ensureFile(); err != nil {
		t.Fatalf("ensureFile: %v", err)
	}

	data, _ := util.ReadFile(fs, "TASKS.csv")
	if string(data) != existing {
		t.Errorf("ensureFile modified existing file")
	}
}

func TestEnsureFile_MarkdownFormat(t *testing.T) {
	fs := memfs.New()
	s := NewStore(fs, "TASKS.md")

	if err := s.ensureFile(); err != nil {
		t.Fatalf("ensureFile: %v", err)
	}

	data, err := util.ReadFile(fs, "TASKS.md")
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	// markdownFormat.EmptyFile() returns "".
	if string(data) != "" {
		t.Errorf("content = %q, want empty string", string(data))
	}
}
