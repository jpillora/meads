package e2e

import (
	"fmt"
	"testing"

	"github.com/jpillora/meads/pkg/meads"
)

func TestParseFile_SingleTask(t *testing.T) {
	input := "## 1 Fix the login bug\n\n* status: open\n* priority: 1\n* depends-on: 3\n\nThe login page throws a 500 when the session cookie is expired."
	f := meads.ParseFile(input)
	if len(f.Tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(f.Tasks))
	}
	task := f.Tasks[0]
	if task.ID != 1 || task.Title != "Fix the login bug" {
		t.Errorf("ID=%d Title=%q", task.ID, task.Title)
	}
	if task.Status != "open" || task.Priority != "1" {
		t.Errorf("Status=%q Priority=%q", task.Status, task.Priority)
	}
	if len(task.DependsOn) != 1 || task.DependsOn[0] != 3 {
		t.Errorf("DependsOn = %v, want [3]", task.DependsOn)
	}
	if task.Description != "The login page throws a 500 when the session cookie is expired." {
		t.Errorf("Description = %q", task.Description)
	}
}

func TestParseFile_MultipleTasks(t *testing.T) {
	input := "## 1 First task\n\n* status: open\n* priority: 2\n\nDo the first thing.\n\n## 2 Second task\n\n* status: closed\n* priority: 1\n\nDo the second thing.\n\n## 3 Third task\n\n* status: inprogress\n\nWorking on it."
	f := meads.ParseFile(input)
	if len(f.Tasks) != 3 {
		t.Fatalf("expected 3 tasks, got %d", len(f.Tasks))
	}
	if f.Tasks[0].ID != 1 || f.Tasks[0].Status != "open" {
		t.Errorf("task 0: ID=%d Status=%q", f.Tasks[0].ID, f.Tasks[0].Status)
	}
	if f.Tasks[1].ID != 2 || f.Tasks[1].Status != "closed" {
		t.Errorf("task 1: ID=%d Status=%q", f.Tasks[1].ID, f.Tasks[1].Status)
	}
	if f.Tasks[2].ID != 3 || f.Tasks[2].Description != "Working on it." {
		t.Errorf("task 2: ID=%d Description=%q", f.Tasks[2].ID, f.Tasks[2].Description)
	}
}

func TestParseFile_NoMetadata(t *testing.T) {
	f := meads.ParseFile("## 1 Bare task\n\nJust a description with no metadata.")
	if len(f.Tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(f.Tasks))
	}
	if f.Tasks[0].Status != "" {
		t.Errorf("Status = %q, want empty", f.Tasks[0].Status)
	}
	if f.Tasks[0].Description != "Just a description with no metadata." {
		t.Errorf("Description = %q", f.Tasks[0].Description)
	}
}

func TestParseFile_Empty(t *testing.T) {
	f := meads.ParseFile("")
	if len(f.Tasks) != 0 {
		t.Fatalf("expected 0 tasks, got %d", len(f.Tasks))
	}
}

func TestParseFile_ProjectMeta(t *testing.T) {
	input := "# TASKS\n\na description\n\n* created: 2026-01-01T00:00:00Z\n* next-id: 5\n\n## 1 First task\n\n* status: open"
	f := meads.ParseFile(input)
	if f.Meta["created"] != "2026-01-01T00:00:00Z" {
		t.Errorf("Meta[created] = %q", f.Meta["created"])
	}
	if len(f.Tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(f.Tasks))
	}
}

// A parsed task carries every field-backed value in BOTH its dedicated field
// and Meta (parseTask sets both), which is the shape these fixtures mirror.
// FormatTask reads the field either way - see TestFormatTask_FieldsOnly.
func TestFormatTask_Full(t *testing.T) {
	task := meads.Task{
		ID: 1, Title: "Fix the login bug", Status: "open", Priority: "3",
		Meta:        map[string]string{"status": "open", "priority": "3"},
		Description: "Some description.",
	}
	got := meads.FormatTask(task)
	want := "## 1. Fix the login bug\n\n* status: open\n* priority: 3\n\nSome description.\n"
	if got != want {
		t.Errorf("FormatTask =\n%q\nwant\n%q", got, want)
	}
}

func TestFormatTask_NoDescription(t *testing.T) {
	task := meads.Task{ID: 2, Title: "No body", Status: "closed"}
	got := meads.FormatTask(task)
	want := "## 2. No body\n\n* status: closed\n"
	if got != want {
		t.Errorf("FormatTask =\n%q\nwant\n%q", got, want)
	}
}

func TestFormatTask_EmptyTitle(t *testing.T) {
	task := meads.Task{ID: 4, Status: "open"}
	got := meads.FormatTask(task)
	want := "## 4.\n\n* status: open\n"
	if got != want {
		t.Errorf("FormatTask =\n%q\nwant\n%q", got, want)
	}
}

func TestFormatFile_WithProjectMeta(t *testing.T) {
	f := meads.File{
		Meta:  map[string]string{"created": "2026-01-01T00:00:00Z", "next-id": "3"},
		Tasks: []meads.Task{{ID: 1, Title: "First", Status: "open"}},
	}
	got := meads.FormatFile(f)
	want := "# TASKS\n\na [meads](https://github.com/jpillora/meads) (`md`) managed task log\n\n* created: 2026-01-01T00:00:00Z\n* next-id: 3\n\n## 1. First\n\n* status: open\n"
	if got != want {
		t.Errorf("FormatFile =\n%q\nwant\n%q", got, want)
	}
}

func TestFormatFile_RoundTrip(t *testing.T) {
	input := "# TASKS\n\na [meads](https://github.com/jpillora/meads) (`md`) managed task log\n\n* created: 2026-01-01T00:00:00Z\n* next-id: 3\n\n## 1 First task\n\n* status: open\n* priority: 2\n\nDo the first thing.\n\n## 2 Second task\n\n* status: closed\n* priority: 1\n\nDo the second thing.\n"
	f := meads.ParseFile(input)
	if len(f.Tasks) != 2 {
		t.Fatalf("expected 2 tasks, got %d", len(f.Tasks))
	}
	output := meads.FormatFile(f)
	f2 := meads.ParseFile(output)
	if len(f2.Tasks) != 2 {
		t.Fatalf("round-trip: expected 2 tasks, got %d", len(f2.Tasks))
	}
	for i := range f.Tasks {
		if f.Tasks[i].ID != f2.Tasks[i].ID || f.Tasks[i].Title != f2.Tasks[i].Title {
			t.Errorf("round-trip task %d mismatch", i)
		}
	}
}

// TASKS #92: a git-sourced Task has an empty Meta for every field-backed key -
// MarshalJSON excludes them (knownMetaKeys) and Task has no UnmarshalJSON to
// put them back - so FormatTask must render all ten from the dedicated fields.
// It used to read status/priority/type/depends-on/close-reason from Meta alone,
// which made `md get` in git mode print tasks with no status at all.
func TestFormatTask_FieldsOnly(t *testing.T) {
	task := meads.Task{
		ID: 7, Title: "Git-sourced", Status: "closed", Priority: "P1", Type: "bug",
		DependsOn: []int{3, 5}, CloseReason: "shipped", StatusReason: "blocked on review",
		Tags: meads.Tags{"api", "web-ui"}, AgentID: "agent-1",
		FilesInScope: []string{"a.go", "b.go"}, Deleted: true,
		Description: "Body.",
	}
	got := meads.FormatTask(task)
	want := "## 7. Git-sourced\n\n" +
		"* status: closed\n* priority: P1\n* type: bug\n* depends-on: 3,5\n" +
		"* close-reason: shipped\n* status-reason: blocked on review\n" +
		"* tags: api,web-ui\n* agent-id: agent-1\n* files-in-scope: a.go,b.go\n" +
		"* deleted: true\n\nBody.\n"
	if got != want {
		t.Errorf("FormatTask =\n%q\nwant\n%q", got, want)
	}
	// ...and the render must parse back into the same fields, so `md convert
	// --from-git` round-trips without convert.go's syncMetaFromFields pre-pass.
	f := meads.ParseFile(got)
	if len(f.Tasks) != 1 {
		t.Fatalf("re-parsed %d tasks, want 1", len(f.Tasks))
	}
	rt := f.Tasks[0]
	if rt.Status != task.Status || rt.Priority != task.Priority || rt.Type != task.Type ||
		rt.CloseReason != task.CloseReason || rt.StatusReason != task.StatusReason ||
		rt.AgentID != task.AgentID || rt.Deleted != task.Deleted ||
		rt.Tags.String() != task.Tags.String() ||
		fmt.Sprint(rt.DependsOn) != fmt.Sprint(task.DependsOn) ||
		fmt.Sprint(rt.FilesInScope) != fmt.Sprint(task.FilesInScope) {
		t.Errorf("round-trip =\n%+v\nwant\n%+v", rt, task)
	}
}

// A field cleared directly (not via SetTags/SetStatus/...) leaves a stale
// value behind in Meta. The field wins, so the meta line is dropped - the
// behaviour Tags alone used to get, now uniform across all ten keys.
func TestFormatTask_ClearedFieldDropsStaleMeta(t *testing.T) {
	task := meads.Task{
		ID: 8, Title: "Cleared",
		Meta: map[string]string{"status": "open", "priority": "P1", "type": "bug",
			"depends-on": "3", "close-reason": "old", "tags": "api", "created": "2026-01-01T00:00:00Z"},
	}
	got := meads.FormatTask(task)
	want := "## 8. Cleared\n\n* created: 2026-01-01T00:00:00Z\n"
	if got != want {
		t.Errorf("FormatTask =\n%q\nwant\n%q", got, want)
	}
}
