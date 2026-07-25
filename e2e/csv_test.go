package e2e

import (
	"strings"
	"testing"

	"github.com/jpillora/meads/pkg/meads"
)

func TestParseCSV_Empty(t *testing.T) {
	f := meads.ParseCSV("")
	if len(f.Tasks) != 0 {
		t.Fatalf("expected 0 tasks, got %d", len(f.Tasks))
	}
}

func TestParseCSV_HeaderOnly(t *testing.T) {
	f := meads.ParseCSV(meads.InitCSV())
	if len(f.Tasks) != 0 {
		t.Fatalf("expected 0 tasks, got %d", len(f.Tasks))
	}
}

func TestCSV_RoundTrip(t *testing.T) {
	original := meads.File{
		Tasks: []meads.Task{
			{
				ID: 1, Title: "Fix login", Status: "open", Priority: "P1", Type: "bug",
				Tags: []string{"backend", "api"},
				Meta: map[string]string{
					"status": "open", "priority": "P1", "type": "bug",
					"tags": "backend,api", "created": "2026-01-01T00:00:00Z",
				},
				Description: "Session cookie expires prematurely.",
			},
			{
				ID: 2, Title: "Set up CI", Status: "closed", Priority: "P2", Type: "task",
				CloseReason: "done",
				Meta: map[string]string{
					"status": "closed", "priority": "P2", "type": "task",
					"close-reason": "done", "created": "2026-01-02T00:00:00Z",
					"updated": "2026-01-03T00:00:00Z",
				},
			},
		},
	}
	csv := meads.FormatCSV(original)
	parsed := meads.ParseCSV(csv)
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
	if len(parsed.Tasks[0].Tags) != 2 || parsed.Tasks[0].Tags[0] != "backend" || parsed.Tasks[0].Tags[1] != "api" {
		t.Errorf("task 0: Tags = %v, want [backend api]", parsed.Tasks[0].Tags)
	}
}

func TestCSV_DescriptionWithNewlines(t *testing.T) {
	f := meads.File{
		Tasks: []meads.Task{
			{
				ID: 1, Title: "Bug report", Status: "open",
				Meta:        map[string]string{"status": "open"},
				Description: "First paragraph.\n\nSecond paragraph with more detail.\n\nThird paragraph.",
			},
		},
	}
	csv := meads.FormatCSV(f)
	parsed := meads.ParseCSV(csv)
	if len(parsed.Tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(parsed.Tasks))
	}
	if parsed.Tasks[0].Description != f.Tasks[0].Description {
		t.Errorf("description round-trip failed:\ngot:  %q\nwant: %q", parsed.Tasks[0].Description, f.Tasks[0].Description)
	}
}

func TestCSV_DependsOnWithCommas(t *testing.T) {
	f := meads.File{
		Tasks: []meads.Task{
			{ID: 1, Title: "Parent A", Status: "open", Meta: map[string]string{"status": "open"}},
			{ID: 2, Title: "Parent B", Status: "open", Meta: map[string]string{"status": "open"}},
			{ID: 3, Title: "Child", Status: "open", Meta: map[string]string{"status": "open", "depends-on": "1,2"}, DependsOn: []int{1, 2}},
		},
	}
	csv := meads.FormatCSV(f)
	parsed := meads.ParseCSV(csv)
	if len(parsed.Tasks) != 3 {
		t.Fatalf("expected 3 tasks, got %d", len(parsed.Tasks))
	}
	child := parsed.Tasks[2]
	if len(child.DependsOn) != 2 || child.DependsOn[0] != 1 || child.DependsOn[1] != 2 {
		t.Errorf("DependsOn = %v, want [1 2]", child.DependsOn)
	}
}

func TestCSV_MetaJSONRoundTrip(t *testing.T) {
	f := meads.File{
		Tasks: []meads.Task{
			{ID: 1, Title: "Custom meta", Status: "open", Meta: map[string]string{"status": "open", "assignee": "alice", "bead-id": "xyz-123"}},
		},
	}
	csv := meads.FormatCSV(f)
	parsed := meads.ParseCSV(csv)
	if len(parsed.Tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(parsed.Tasks))
	}
	if parsed.Tasks[0].Meta["assignee"] != "alice" {
		t.Errorf("Meta[assignee] = %q", parsed.Tasks[0].Meta["assignee"])
	}
	if parsed.Tasks[0].Meta["bead-id"] != "xyz-123" {
		t.Errorf("Meta[bead-id] = %q", parsed.Tasks[0].Meta["bead-id"])
	}
}

func TestCSV_TombstoneIncluded(t *testing.T) {
	input := meads.InitCSV() + "1,Fix login,open,P1,bug,,,Session cookie expires,,,,,,,{}\n" +
		"2,Was closed,closed,,,,,,,,,,true,\n"
	f := meads.ParseCSV(input)
	if len(f.Tasks) != 2 {
		t.Fatalf("expected 2 tasks (including tombstone), got %d", len(f.Tasks))
	}
	if !f.Tasks[1].Deleted {
		t.Error("task 2 should have Deleted=true")
	}
	if f.Tasks[1].Status != "closed" {
		t.Errorf("task 2 status = %q, want %q (preserved)", f.Tasks[1].Status, "closed")
	}
}

func TestCSV_SortedByID(t *testing.T) {
	f := meads.File{
		Tasks: []meads.Task{
			{ID: 3, Title: "Third", Status: "open", Meta: map[string]string{"status": "open"}},
			{ID: 1, Title: "First", Status: "open", Meta: map[string]string{"status": "open"}},
			{ID: 2, Title: "Second", Status: "open", Meta: map[string]string{"status": "open"}},
		},
	}
	csv := meads.FormatCSV(f)
	parsed := meads.ParseCSV(csv)
	for i, want := range []int{1, 2, 3} {
		if parsed.Tasks[i].ID != want {
			t.Errorf("task %d: ID = %d, want %d", i, parsed.Tasks[i].ID, want)
		}
	}
}

func TestParseCSV_InvalidCSV(t *testing.T) {
	f := meads.ParseCSV("\"unclosed quote\n")
	if len(f.Tasks) != 0 {
		t.Fatalf("expected 0 tasks for invalid CSV, got %d", len(f.Tasks))
	}
}

func TestParseCSV_InvalidID(t *testing.T) {
	input := meads.InitCSV() + "abc,Bad ID,open,P1,bug,,,desc,,,," + "{}\n" +
		"0,Zero ID,open,P1,bug,,,desc,,,," + "{}\n" +
		"-1,Negative,open,P1,bug,,,desc,,,," + "{}\n" +
		"1,Good,open,P1,bug,,,desc,,,," + "{}\n"
	f := meads.ParseCSV(input)
	if len(f.Tasks) != 1 {
		t.Fatalf("expected 1 valid task, got %d", len(f.Tasks))
	}
	if f.Tasks[0].Title != "Good" {
		t.Errorf("task title = %q, want %q", f.Tasks[0].Title, "Good")
	}
}

func TestParseCSV_NoMetaColumn(t *testing.T) {
	input := "id,title,status\n1,Test,open\n"
	f := meads.ParseCSV(input)
	if len(f.Tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(f.Tasks))
	}
	if f.Tasks[0].Title != "Test" {
		t.Errorf("title = %q", f.Tasks[0].Title)
	}
}

func TestInitCSV(t *testing.T) {
	content := meads.InitCSV()
	if !strings.HasPrefix(content, "id,") {
		t.Errorf("InitCSV() = %q, expected CSV header", content)
	}
	f := meads.ParseCSV(content)
	if len(f.Tasks) != 0 {
		t.Errorf("InitCSV content should have 0 tasks, got %d", len(f.Tasks))
	}
}

func TestFormatCSV_NilMeta(t *testing.T) {
	f := meads.File{
		Tasks: []meads.Task{{ID: 1, Title: "No meta", Status: "open", Meta: nil}},
	}
	csv := meads.FormatCSV(f)
	parsed := meads.ParseCSV(csv)
	if len(parsed.Tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(parsed.Tasks))
	}
}

// --- Store-level CSV tests ---

func TestCSVStore_Add(t *testing.T) {
	s := newCSVStore(t)
	id, err := s.Add(meads.Task{Title: "Task one", Status: "open"})
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
	if len(tasks) != 1 || tasks[0].Title != "Task one" {
		t.Errorf("unexpected tasks: %v", tasks)
	}
}

func TestCSVStore_Delete(t *testing.T) {
	s := newCSVStore(t)
	id1, _ := s.Add(meads.Task{Title: "Task 1", Status: "open"})
	id2, _ := s.Add(meads.Task{Title: "Task 2", Status: "open"})

	if err := s.Delete(id1); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	tasks, _ := s.Get(nil)
	if len(tasks) != 1 || tasks[0].ID != id2 {
		t.Fatalf("expected task %d, got %v", id2, tasks)
	}
}

func TestCSVStore_DeletePreservesIDs(t *testing.T) {
	s := newCSVStore(t)
	s.Add(meads.Task{Title: "Task 1", Status: "open"})
	s.Add(meads.Task{Title: "Task 2", Status: "open"})
	id3, _ := s.Add(meads.Task{Title: "Task 3", Status: "open"})

	s.Delete(1)
	s.Delete(2)
	s.Delete(id3)

	id4, err := s.Add(meads.Task{Title: "Task 4", Status: "open"})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if id4 != 4 {
		t.Fatalf("expected ID 4, got %d", id4)
	}
}

func TestCSVStore_Update(t *testing.T) {
	s := newCSVStore(t)
	id, _ := s.Add(meads.Task{Title: "Update me", Status: "open"})

	err := s.Update(id, func(t *meads.Task) {
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
	s := newCSVStore(t)
	id1, _ := s.Add(meads.Task{Title: "Parent", Status: "open"})
	id2, _ := s.Add(meads.Task{Title: "Child", Status: "open"})
	s.Update(id2, func(t *meads.Task) {
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
	s := newCSVStore(t)
	s.Add(meads.Task{Title: "Task 1", Status: "open"})
	id2, _ := s.Add(meads.Task{Title: "Task 2", Status: "open"})

	s.Delete(id2)

	id3, err := s.Add(meads.Task{Title: "Task 3", Status: "open"})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if id3 != 3 {
		t.Fatalf("expected ID 3, got %d", id3)
	}
}

func TestCSV_StatusReasonRoundTrip(t *testing.T) {
	f := meads.File{
		Tasks: []meads.Task{
			{
				ID: 1, Title: "Closed task", Status: "closed", StatusReason: "deployed to prod",
				Meta: map[string]string{"status": "closed", "created": "2026-01-01T00:00:00Z"},
			},
		},
	}
	csv := meads.FormatCSV(f)
	parsed := meads.ParseCSV(csv)
	if len(parsed.Tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(parsed.Tasks))
	}
	if parsed.Tasks[0].StatusReason != "deployed to prod" {
		t.Errorf("StatusReason = %q, want %q", parsed.Tasks[0].StatusReason, "deployed to prod")
	}
}

// TestCSV_AgentIDFilesInScopeRoundTrip covers the CSV formatter's git-mode
// migration support (task 66 phase 9): AgentID/FilesInScope are set only by
// GitStore.Claim (gitmutate.go) and, unlike status/priority/etc, are never
// synced into Task.Meta by a Set* method - FormatCSV writes them from the
// struct fields directly into their own trailing columns, so `md convert
// --from-git` can carry a claimed task's fields into a fresh TASKS.csv.
func TestCSV_AgentIDFilesInScopeRoundTrip(t *testing.T) {
	f := meads.File{
		Tasks: []meads.Task{
			{
				ID: 1, Title: "Claimed task", Status: "inprogress",
				AgentID:      "agent-42",
				FilesInScope: []string{"a.go", "b.go"},
				Meta:         map[string]string{"status": "inprogress"},
			},
		},
	}
	csv := meads.FormatCSV(f)
	parsed := meads.ParseCSV(csv)
	if len(parsed.Tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(parsed.Tasks))
	}
	got := parsed.Tasks[0]
	if got.AgentID != "agent-42" {
		t.Errorf("AgentID = %q, want %q", got.AgentID, "agent-42")
	}
	if len(got.FilesInScope) != 2 || got.FilesInScope[0] != "a.go" || got.FilesInScope[1] != "b.go" {
		t.Errorf("FilesInScope = %v", got.FilesInScope)
	}
}

// TestCSV_AgentIDFilesInScope_OldHeaderStillParses proves an older CSV file
// written before agent-id/files-in-scope existed (14 columns, no trailing
// two) still parses cleanly: the new columns were appended at the very end
// of csvColumns specifically so an old header's existing column positions -
// including "meta", the last of the original 14 - never shift. Every
// existing field must resolve exactly as before, and the two new fields
// must read back empty rather than erroring or misreading a neighbour.
func TestCSV_AgentIDFilesInScope_OldHeaderStillParses(t *testing.T) {
	const oldHeader = "id,title,status,priority,type,depends-on,tags,description,close-reason,status-reason,created,updated,deleted,meta\n"
	input := oldHeader + "1,Pre-existing task,open,P1,bug,,,a description,,,,,,{}\n"
	f := meads.ParseCSV(input)
	if len(f.Tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(f.Tasks))
	}
	got := f.Tasks[0]
	if got.Title != "Pre-existing task" || got.Status != "open" || got.Priority != "P1" || got.Type != "bug" {
		t.Errorf("old-format fields mismatch: %+v", got)
	}
	if got.Description != "a description" {
		t.Errorf("Description = %q, want %q", got.Description, "a description")
	}
	if got.AgentID != "" {
		t.Errorf("AgentID = %q, want empty for a pre-migration row", got.AgentID)
	}
	if len(got.FilesInScope) != 0 {
		t.Errorf("FilesInScope = %v, want empty for a pre-migration row", got.FilesInScope)
	}
}

func TestCSV_DeletedRoundTrip(t *testing.T) {
	f := meads.File{
		Tasks: []meads.Task{
			{
				ID: 1, Title: "Preserved task", Status: "closed", Deleted: true,
				Meta: map[string]string{"status": "closed", "created": "2026-01-01T00:00:00Z"},
			},
			{
				ID: 2, Title: "Active task", Status: "open",
				Meta: map[string]string{"status": "open", "created": "2026-01-01T00:00:00Z"},
			},
		},
	}
	csv := meads.FormatCSV(f)
	parsed := meads.ParseCSV(csv)
	if len(parsed.Tasks) != 2 {
		t.Fatalf("expected 2 tasks, got %d", len(parsed.Tasks))
	}
	if !parsed.Tasks[0].Deleted {
		t.Error("task 1 should have Deleted=true")
	}
	if parsed.Tasks[0].Status != "closed" {
		t.Errorf("task 1 status = %q, want %q (preserved)", parsed.Tasks[0].Status, "closed")
	}
	if parsed.Tasks[0].Title != "Preserved task" {
		t.Errorf("task 1 title = %q, want %q (preserved)", parsed.Tasks[0].Title, "Preserved task")
	}
	if parsed.Tasks[1].Deleted {
		t.Error("task 2 should have Deleted=false")
	}
}
