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

func TestParseCSV_Empty(t *testing.T) {
	f := ParseCSV("")
	if len(f.Tasks) != 0 {
		t.Fatalf("expected 0 tasks, got %d", len(f.Tasks))
	}
}

func TestParseCSV_HeaderOnly(t *testing.T) {
	f := ParseCSV(csvHeaderRow())
	if len(f.Tasks) != 0 {
		t.Fatalf("expected 0 tasks, got %d", len(f.Tasks))
	}
}

func TestCSV_RoundTrip(t *testing.T) {
	original := File{
		Tasks: []Task{
			{
				ID:       1,
				Title:    "Fix login",
				Status:   "open",
				Priority: "P1",
				Type:     "bug",
				Tags:     []string{"backend", "api"},
				Meta: map[string]string{
					"status":   "open",
					"priority": "P1",
					"type":     "bug",
					"tags":     "backend,api",
					"created":  "2026-01-01T00:00:00Z",
				},
				Description: "Session cookie expires prematurely.",
			},
			{
				ID:          2,
				Title:       "Set up CI",
				Status:      "closed",
				Priority:    "P2",
				Type:        "task",
				CloseReason: "done",
				Meta: map[string]string{
					"status":       "closed",
					"priority":     "P2",
					"type":         "task",
					"close-reason": "done",
					"created":      "2026-01-02T00:00:00Z",
					"updated":      "2026-01-03T00:00:00Z",
				},
			},
		},
	}
	csv := FormatCSV(original)
	parsed := ParseCSV(csv)
	if len(parsed.Tasks) != 2 {
		t.Fatalf("expected 2 tasks, got %d", len(parsed.Tasks))
	}
	for i, want := range original.Tasks {
		got := parsed.Tasks[i]
		if got.ID != want.ID {
			t.Errorf("task %d: ID %d != %d", i, got.ID, want.ID)
		}
		if got.Title != want.Title {
			t.Errorf("task %d: Title %q != %q", i, got.Title, want.Title)
		}
		if got.Status != want.Status {
			t.Errorf("task %d: Status %q != %q", i, got.Status, want.Status)
		}
		if got.Priority != want.Priority {
			t.Errorf("task %d: Priority %q != %q", i, got.Priority, want.Priority)
		}
		if got.Type != want.Type {
			t.Errorf("task %d: Type %q != %q", i, got.Type, want.Type)
		}
		if got.CloseReason != want.CloseReason {
			t.Errorf("task %d: CloseReason %q != %q", i, got.CloseReason, want.CloseReason)
		}
		if got.Description != want.Description {
			t.Errorf("task %d: Description %q != %q", i, got.Description, want.Description)
		}
	}
	// Verify tags round-trip.
	if len(parsed.Tasks[0].Tags) != 2 || parsed.Tasks[0].Tags[0] != "backend" || parsed.Tasks[0].Tags[1] != "api" {
		t.Errorf("task 0: Tags = %v, want [backend api]", parsed.Tasks[0].Tags)
	}
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

func TestCSV_DescriptionWithNewlines(t *testing.T) {
	f := File{
		Tasks: []Task{
			{
				ID:          1,
				Title:       "Bug report",
				Status:      "open",
				Meta:        map[string]string{"status": "open"},
				Description: "First paragraph.\n\nSecond paragraph with more detail.\n\nThird paragraph.",
			},
		},
	}
	csv := FormatCSV(f)
	// CSV output should not contain unescaped newlines within the description field.
	parsed := ParseCSV(csv)
	if len(parsed.Tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(parsed.Tasks))
	}
	if parsed.Tasks[0].Description != f.Tasks[0].Description {
		t.Errorf("description round-trip failed:\ngot:  %q\nwant: %q", parsed.Tasks[0].Description, f.Tasks[0].Description)
	}
}

func TestCSV_DependsOnWithCommas(t *testing.T) {
	f := File{
		Tasks: []Task{
			{
				ID:        1,
				Title:     "Parent A",
				Status:    "open",
				Meta:      map[string]string{"status": "open"},
				DependsOn: []int{},
			},
			{
				ID:        2,
				Title:     "Parent B",
				Status:    "open",
				Meta:      map[string]string{"status": "open"},
				DependsOn: []int{},
			},
			{
				ID:        3,
				Title:     "Child",
				Status:    "open",
				Meta:      map[string]string{"status": "open", "depends-on": "1,2"},
				DependsOn: []int{1, 2},
			},
		},
	}
	csv := FormatCSV(f)
	parsed := ParseCSV(csv)
	if len(parsed.Tasks) != 3 {
		t.Fatalf("expected 3 tasks, got %d", len(parsed.Tasks))
	}
	child := parsed.Tasks[2]
	if len(child.DependsOn) != 2 || child.DependsOn[0] != 1 || child.DependsOn[1] != 2 {
		t.Errorf("DependsOn = %v, want [1 2]", child.DependsOn)
	}
}

func TestCSV_MetaJSONRoundTrip(t *testing.T) {
	f := File{
		Tasks: []Task{
			{
				ID:     1,
				Title:  "Custom meta",
				Status: "open",
				Meta: map[string]string{
					"status":   "open",
					"assignee": "alice",
					"bead-id":  "xyz-123",
				},
			},
		},
	}
	csv := FormatCSV(f)
	parsed := ParseCSV(csv)
	if len(parsed.Tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(parsed.Tasks))
	}
	if parsed.Tasks[0].Meta["assignee"] != "alice" {
		t.Errorf("Meta[assignee] = %q, want %q", parsed.Tasks[0].Meta["assignee"], "alice")
	}
	if parsed.Tasks[0].Meta["bead-id"] != "xyz-123" {
		t.Errorf("Meta[bead-id] = %q, want %q", parsed.Tasks[0].Meta["bead-id"], "xyz-123")
	}
}

func TestCSV_TombstoneIncluded(t *testing.T) {
	input := csvHeaderRow() + "1,Fix login,open,P1,bug,,,Session cookie expires,,,,{}\n" +
		"2,deleted,deleted,,,,,,,,,\n"
	f := ParseCSV(input)
	if len(f.Tasks) != 2 {
		t.Fatalf("expected 2 tasks (including tombstone), got %d", len(f.Tasks))
	}
	if f.Tasks[1].Status != "deleted" {
		t.Errorf("task 2 status = %q, want %q", f.Tasks[1].Status, "deleted")
	}
}

func TestCSV_SortedByID(t *testing.T) {
	f := File{
		Tasks: []Task{
			{ID: 3, Title: "Third", Status: "open", Meta: map[string]string{"status": "open"}},
			{ID: 1, Title: "First", Status: "open", Meta: map[string]string{"status": "open"}},
			{ID: 2, Title: "Second", Status: "open", Meta: map[string]string{"status": "open"}},
		},
	}
	csv := FormatCSV(f)
	parsed := ParseCSV(csv)
	for i, want := range []int{1, 2, 3} {
		if parsed.Tasks[i].ID != want {
			t.Errorf("task %d: ID = %d, want %d", i, parsed.Tasks[i].ID, want)
		}
	}
}

func TestParseCSV_InvalidCSV(t *testing.T) {
	// Mismatched quotes produce a CSV parse error.
	f := ParseCSV("\"unclosed quote\n")
	if len(f.Tasks) != 0 {
		t.Fatalf("expected 0 tasks for invalid CSV, got %d", len(f.Tasks))
	}
}

func TestParseCSV_InvalidID(t *testing.T) {
	input := csvHeaderRow() + "abc,Bad ID,open,P1,bug,,,desc,,,," + "{}\n" +
		"0,Zero ID,open,P1,bug,,,desc,,,," + "{}\n" +
		"-1,Negative,open,P1,bug,,,desc,,,," + "{}\n" +
		"1,Good,open,P1,bug,,,desc,,,," + "{}\n"
	f := ParseCSV(input)
	if len(f.Tasks) != 1 {
		t.Fatalf("expected 1 valid task, got %d", len(f.Tasks))
	}
	if f.Tasks[0].Title != "Good" {
		t.Errorf("task title = %q, want %q", f.Tasks[0].Title, "Good")
	}
}

func TestParseCSV_NoMetaColumn(t *testing.T) {
	// CSV with fewer columns than expected — no meta column.
	input := "id,title,status\n1,Test,open\n"
	f := ParseCSV(input)
	if len(f.Tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(f.Tasks))
	}
	if f.Tasks[0].Title != "Test" {
		t.Errorf("title = %q", f.Tasks[0].Title)
	}
}

func TestInitCSV(t *testing.T) {
	content := InitCSV()
	if !strings.HasPrefix(content, "id,") {
		t.Errorf("InitCSV() = %q, expected CSV header", content)
	}
	// Should be parseable.
	f := ParseCSV(content)
	if len(f.Tasks) != 0 {
		t.Errorf("InitCSV content should have 0 tasks, got %d", len(f.Tasks))
	}
}

func TestFormatCSV_NilMeta(t *testing.T) {
	f := File{
		Tasks: []Task{
			{ID: 1, Title: "No meta", Status: "open", Meta: nil},
		},
	}
	// Should not panic.
	csv := FormatCSV(f)
	parsed := ParseCSV(csv)
	if len(parsed.Tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(parsed.Tasks))
	}
}

// --- Store-level tests with CSV backend ---

func TestCSVStore_Add(t *testing.T) {
	s := newCSVTestStore(t, "")
	id, err := s.Add(Task{Title: "Task one", Status: "open"})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if id != 1 {
		t.Fatalf("expected ID 1, got %d", id)
	}
	tasks, err := s.Get(nil)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(tasks))
	}
	if tasks[0].Title != "Task one" {
		t.Errorf("Title = %q, want %q", tasks[0].Title, "Task one")
	}
}

func TestCSVStore_Delete(t *testing.T) {
	s := newCSVTestStore(t, "")
	id1, _ := s.Add(Task{Title: "Task 1", Status: "open"})
	id2, _ := s.Add(Task{Title: "Task 2", Status: "open"})

	if err := s.Delete(id1); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	tasks, err := s.Get(nil)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(tasks))
	}
	if tasks[0].ID != id2 {
		t.Errorf("expected task %d, got %d", id2, tasks[0].ID)
	}
}

func TestCSVStore_DeletePreservesIDs(t *testing.T) {
	s := newCSVTestStore(t, "")
	s.Add(Task{Title: "Task 1", Status: "open"})
	s.Add(Task{Title: "Task 2", Status: "open"})
	id3, _ := s.Add(Task{Title: "Task 3", Status: "open"})

	// Delete all tasks — should leave a tombstone for ID 3.
	s.Delete(1)
	s.Delete(2)
	s.Delete(id3)

	// Next task should get ID 4.
	id4, err := s.Add(Task{Title: "Task 4", Status: "open"})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if id4 != 4 {
		t.Fatalf("expected ID 4, got %d", id4)
	}
}

func TestCSVStore_Update(t *testing.T) {
	s := newCSVTestStore(t, "")
	id, _ := s.Add(Task{Title: "Update me", Status: "open"})

	err := s.Update(id, func(t *Task) {
		t.SetPriority("P1")
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	tasks, _ := s.Get([]int{id})
	if tasks[0].Priority != "P1" {
		t.Errorf("Priority = %q, want %q", tasks[0].Priority, "P1")
	}
}

func TestCSVStore_Ready(t *testing.T) {
	s := newCSVTestStore(t, "")
	id1, _ := s.Add(Task{Title: "Parent", Status: "open"})
	id2, _ := s.Add(Task{Title: "Child", Status: "open"})
	s.Update(id2, func(t *Task) {
		t.AddDep(id1)
	})

	ready, err := s.Ready()
	if err != nil {
		t.Fatalf("Ready: %v", err)
	}
	if len(ready) != 1 || ready[0].ID != id1 {
		t.Fatalf("expected [%d] ready, got %v", id1, ready)
	}
}

func TestCSVStore_NextIDAfterDeletion(t *testing.T) {
	s := newCSVTestStore(t, "")
	s.Add(Task{Title: "Task 1", Status: "open"})
	id2, _ := s.Add(Task{Title: "Task 2", Status: "open"})

	// Delete task 2 (highest). Tombstone should keep ID 2.
	s.Delete(id2)

	// Next add should get ID 3.
	id3, err := s.Add(Task{Title: "Task 3", Status: "open"})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if id3 != 3 {
		t.Fatalf("expected ID 3, got %d", id3)
	}
}

// --- Tombstone pruning tests ---

func TestPruneTombstones_NoTombstones(t *testing.T) {
	f := &File{
		Tasks: []Task{
			{ID: 1, Status: "open"},
			{ID: 2, Status: "open"},
		},
	}
	pruneTombstones(f)
	if len(f.Tasks) != 2 {
		t.Fatalf("expected 2 tasks, got %d", len(f.Tasks))
	}
}

func TestPruneTombstones_ActiveHigherThanDeleted(t *testing.T) {
	f := &File{
		Tasks: []Task{
			{ID: 1, Status: "deleted"},
			{ID: 2, Status: "deleted"},
			{ID: 3, Status: "open"},
		},
	}
	pruneTombstones(f)
	// All tombstones should be pruned since active ID 3 > deleted IDs.
	if len(f.Tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(f.Tasks))
	}
	if f.Tasks[0].ID != 3 {
		t.Errorf("expected task 3, got task %d", f.Tasks[0].ID)
	}
}

func TestPruneTombstones_DeletedHigherThanActive(t *testing.T) {
	f := &File{
		Tasks: []Task{
			{ID: 1, Status: "open"},
			{ID: 2, Status: "deleted"},
			{ID: 3, Status: "deleted"},
		},
	}
	pruneTombstones(f)
	// Only highest deleted (ID 3) should remain.
	if len(f.Tasks) != 2 {
		t.Fatalf("expected 2 tasks, got %d", len(f.Tasks))
	}
	ids := map[int]bool{}
	for _, task := range f.Tasks {
		ids[task.ID] = true
	}
	if !ids[1] || !ids[3] {
		t.Errorf("expected tasks [1, 3], got %v", ids)
	}
}

func TestPruneTombstones_AllDeleted(t *testing.T) {
	f := &File{
		Tasks: []Task{
			{ID: 1, Status: "deleted"},
			{ID: 2, Status: "deleted"},
			{ID: 3, Status: "deleted"},
		},
	}
	pruneTombstones(f)
	// Only the highest (ID 3) tombstone should remain.
	if len(f.Tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(f.Tasks))
	}
	if f.Tasks[0].ID != 3 {
		t.Errorf("expected task 3, got task %d", f.Tasks[0].ID)
	}
}
