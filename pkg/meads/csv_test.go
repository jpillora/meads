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

func TestPruneTombstones_NoTombstones(t *testing.T) {
	f := &File{Tasks: []Task{{ID: 1, Status: "open"}, {ID: 2, Status: "open"}}}
	pruneTombstones(f)
	if len(f.Tasks) != 2 {
		t.Fatalf("expected 2 tasks, got %d", len(f.Tasks))
	}
}

func TestPruneTombstones_ActiveHigherThanDeleted(t *testing.T) {
	f := &File{Tasks: []Task{
		{ID: 1, Deleted: true}, {ID: 2, Deleted: true}, {ID: 3, Status: "open"},
	}}
	pruneTombstones(f)
	if len(f.Tasks) != 1 || f.Tasks[0].ID != 3 {
		t.Fatalf("expected [3], got %v", f.Tasks)
	}
}

func TestPruneTombstones_DeletedHigherThanActive(t *testing.T) {
	f := &File{Tasks: []Task{
		{ID: 1, Status: "open"}, {ID: 2, Deleted: true}, {ID: 3, Deleted: true},
	}}
	pruneTombstones(f)
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

func TestPruneTombstones_AllDeleted(t *testing.T) {
	f := &File{Tasks: []Task{
		{ID: 1, Deleted: true}, {ID: 2, Deleted: true}, {ID: 3, Deleted: true},
	}}
	pruneTombstones(f)
	if len(f.Tasks) != 1 || f.Tasks[0].ID != 3 {
		t.Fatalf("expected [3], got %v", f.Tasks)
	}
}
