package meads

import (
	"strings"
	"testing"

	"github.com/go-git/go-billy/v5/memfs"
	"github.com/go-git/go-billy/v5/util"
)

func newCSVTestStore(t *testing.T, content string) *Store {
	t.Helper()
	fs := memfs.New()
	if content != "" {
		if err := util.WriteFile(fs, "TASKS.csv", []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}
	return NewStore(fs, "TASKS.csv")
}

func TestCSV_NewlineEscaping(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"simple newline", "line one\nline two"},
		{"multiple newlines", "a\nb\nc\nd"},
		{"backslash", `path\to\file`},
		{"backslash-n literal", `literal \n in text`},
		{"empty", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			escaped := escapeNewlines(tt.input)
			if strings.Contains(escaped, "\n") {
				t.Errorf("escaped string should not contain actual newlines: %q", escaped)
			}
			unescaped := unescapeNewlines(escaped)
			if unescaped != tt.input {
				t.Errorf("round-trip failed: got %q, want %q", unescaped, tt.input)
			}
		})
	}
}

// --- Tombstone pruning tests (whitebox — uses unexported pruneTombstones) ---
// CSV (no preamble) keeps one tombstone row; markdown (preamble) records the
// high-water mark in f.Meta["max-id"] instead.

func TestPruneTombstones_CSV_NoTombstones(t *testing.T) {
	f := &File{Tasks: []Task{{ID: 1, Status: "open"}, {ID: 2, Status: "open"}}}
	pruneTombstones(f, false)
	if len(f.Tasks) != 2 {
		t.Fatalf("expected 2 tasks, got %d", len(f.Tasks))
	}
}

func TestPruneTombstones_CSV_ActiveHigherThanDeleted(t *testing.T) {
	f := &File{Tasks: []Task{
		{ID: 1, Deleted: true}, {ID: 2, Deleted: true}, {ID: 3, Status: "open"},
	}}
	pruneTombstones(f, false)
	if len(f.Tasks) != 1 || f.Tasks[0].ID != 3 {
		t.Fatalf("expected [3], got %v", f.Tasks)
	}
}

func TestPruneTombstones_CSV_DeletedHigherThanActive(t *testing.T) {
	f := &File{Tasks: []Task{
		{ID: 1, Status: "open"}, {ID: 2, Deleted: true}, {ID: 3, Deleted: true},
	}}
	pruneTombstones(f, false)
	if len(f.Tasks) != 2 {
		t.Fatalf("expected 2 tasks, got %d", len(f.Tasks))
	}
	ids := map[int]bool{}
	for _, task := range f.Tasks {
		ids[task.ID] = true
	}
	if !ids[1] || !ids[3] {
		t.Errorf("expected [1,3], got %v", ids)
	}
}

func TestPruneTombstones_CSV_AllDeleted(t *testing.T) {
	f := &File{Tasks: []Task{
		{ID: 1, Deleted: true}, {ID: 2, Deleted: true}, {ID: 3, Deleted: true},
	}}
	pruneTombstones(f, false)
	if len(f.Tasks) != 1 || f.Tasks[0].ID != 3 {
		t.Fatalf("expected [3], got %v", f.Tasks)
	}
}

func TestPruneTombstones_Markdown_DropsAllRowsClearsMetaWhenActiveHigher(t *testing.T) {
	f := &File{
		Meta: map[string]string{"max-id": "5"},
		Tasks: []Task{
			{ID: 1, Deleted: true}, {ID: 2, Deleted: true}, {ID: 3, Status: "open"},
		},
	}
	pruneTombstones(f, true)
	if len(f.Tasks) != 1 || f.Tasks[0].ID != 3 {
		t.Fatalf("expected [3], got %v", f.Tasks)
	}
	if _, ok := f.Meta["max-id"]; ok {
		t.Errorf("max-id should be cleared, got %q", f.Meta["max-id"])
	}
}

func TestPruneTombstones_Markdown_DropsRowsRecordsMaxIDWhenDeletedHigher(t *testing.T) {
	f := &File{
		Tasks: []Task{
			{ID: 1, Status: "open"}, {ID: 2, Deleted: true}, {ID: 3, Deleted: true},
		},
	}
	pruneTombstones(f, true)
	if len(f.Tasks) != 1 || f.Tasks[0].ID != 1 {
		t.Fatalf("expected [1], got %v", f.Tasks)
	}
	if f.Meta["max-id"] != "3" {
		t.Errorf("max-id = %q, want %q", f.Meta["max-id"], "3")
	}
}

func TestPruneTombstones_Markdown_AllDeletedRecordsMaxID(t *testing.T) {
	f := &File{Tasks: []Task{
		{ID: 1, Deleted: true}, {ID: 2, Deleted: true}, {ID: 3, Deleted: true},
	}}
	pruneTombstones(f, true)
	if len(f.Tasks) != 0 {
		t.Fatalf("expected no tasks, got %v", f.Tasks)
	}
	if f.Meta["max-id"] != "3" {
		t.Errorf("max-id = %q, want %q", f.Meta["max-id"], "3")
	}
}
