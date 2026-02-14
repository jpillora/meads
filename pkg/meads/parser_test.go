package meads

import (
	"testing"
)

func TestParseFile_SingleTask(t *testing.T) {
	input := `## 1 Fix the login bug

* status: open
* priority: 1
* depends-on: 3

The login page throws a 500 when the session cookie is expired.`

	f := ParseFile(input)
	if len(f.Tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(f.Tasks))
	}
	task := f.Tasks[0]
	if task.ID != 1 {
		t.Errorf("ID = %d, want %d", task.ID, 1)
	}
	if task.Title != "Fix the login bug" {
		t.Errorf("Title = %q, want %q", task.Title, "Fix the login bug")
	}
	if task.Status != "open" {
		t.Errorf("Status = %q, want %q", task.Status, "open")
	}
	if task.Priority != "1" {
		t.Errorf("Priority = %q, want %q", task.Priority, "1")
	}
	if task.DependsOn != 3 {
		t.Errorf("DependsOn = %d, want %d", task.DependsOn, 3)
	}
	if task.Body != "The login page throws a 500 when the session cookie is expired." {
		t.Errorf("Body = %q, want %q", task.Body, "The login page throws a 500 when the session cookie is expired.")
	}
}

func TestParseFile_MultipleTasks(t *testing.T) {
	input := `## 1 First task

* status: open
* priority: 2

Do the first thing.

## 2 Second task

* status: closed
* priority: 1

Do the second thing.

## 3 Third task

* status: inprogress

Working on it.`

	f := ParseFile(input)
	if len(f.Tasks) != 3 {
		t.Fatalf("expected 3 tasks, got %d", len(f.Tasks))
	}
	if f.Tasks[0].ID != 1 || f.Tasks[0].Title != "First task" {
		t.Errorf("task 0: ID=%d Title=%q", f.Tasks[0].ID, f.Tasks[0].Title)
	}
	if f.Tasks[0].Status != "open" || f.Tasks[0].Priority != "2" {
		t.Errorf("task 0: Status=%q Priority=%q", f.Tasks[0].Status, f.Tasks[0].Priority)
	}
	if f.Tasks[1].ID != 2 || f.Tasks[1].Status != "closed" {
		t.Errorf("task 1: ID=%d Status=%q", f.Tasks[1].ID, f.Tasks[1].Status)
	}
	if f.Tasks[2].ID != 3 || f.Tasks[2].Status != "inprogress" {
		t.Errorf("task 2: ID=%d Status=%q", f.Tasks[2].ID, f.Tasks[2].Status)
	}
	if f.Tasks[2].Body != "Working on it." {
		t.Errorf("task 2: Body=%q", f.Tasks[2].Body)
	}
}

func TestParseFile_NoMetadata(t *testing.T) {
	input := `## 1 Bare task

Just a description with no metadata.`

	f := ParseFile(input)
	if len(f.Tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(f.Tasks))
	}
	task := f.Tasks[0]
	if task.ID != 1 {
		t.Errorf("ID = %d", task.ID)
	}
	if task.Status != "" {
		t.Errorf("Status = %q, want empty", task.Status)
	}
	if task.Priority != "" {
		t.Errorf("Priority = %q, want empty", task.Priority)
	}
	if task.Body != "Just a description with no metadata." {
		t.Errorf("Body = %q", task.Body)
	}
}

func TestParseFile_CustomMeta(t *testing.T) {
	input := `## 1 Custom fields

* status: open
* priority: 3
* assignee: alice
* component: backend

Fix the API.`

	f := ParseFile(input)
	if len(f.Tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(f.Tasks))
	}
	task := f.Tasks[0]
	if task.Meta["assignee"] != "alice" {
		t.Errorf("Meta[assignee] = %q, want %q", task.Meta["assignee"], "alice")
	}
	if task.Meta["component"] != "backend" {
		t.Errorf("Meta[component] = %q, want %q", task.Meta["component"], "backend")
	}
}

func TestParseFile_Empty(t *testing.T) {
	f := ParseFile("")
	if len(f.Tasks) != 0 {
		t.Fatalf("expected 0 tasks, got %d", len(f.Tasks))
	}
}

func TestParseFile_NoBody(t *testing.T) {
	input := `## 1 Metadata only

* status: open
* priority: 1`

	f := ParseFile(input)
	if len(f.Tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(f.Tasks))
	}
	if f.Tasks[0].Body != "" {
		t.Errorf("Body = %q, want empty", f.Tasks[0].Body)
	}
}

func TestParseFile_MultilineBody(t *testing.T) {
	input := `## 1 Multi-line body

* status: open

First paragraph.

Second paragraph with more detail.`

	f := ParseFile(input)
	if len(f.Tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(f.Tasks))
	}
	want := "First paragraph.\n\nSecond paragraph with more detail."
	if f.Tasks[0].Body != want {
		t.Errorf("Body = %q, want %q", f.Tasks[0].Body, want)
	}
}

func TestParseFile_IDOnly(t *testing.T) {
	input := `## 1

* status: open`

	f := ParseFile(input)
	if len(f.Tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(f.Tasks))
	}
	if f.Tasks[0].ID != 1 {
		t.Errorf("ID = %d", f.Tasks[0].ID)
	}
	if f.Tasks[0].Title != "" {
		t.Errorf("Title = %q, want empty", f.Tasks[0].Title)
	}
}

func TestParseFile_LeadingZeros(t *testing.T) {
	input := `## 0001 Task with leading zeros

* status: open
* depends-on: 0003`

	f := ParseFile(input)
	if len(f.Tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(f.Tasks))
	}
	if f.Tasks[0].ID != 1 {
		t.Errorf("ID = %d, want 1", f.Tasks[0].ID)
	}
	if f.Tasks[0].DependsOn != 3 {
		t.Errorf("DependsOn = %d, want 3", f.Tasks[0].DependsOn)
	}
	if f.Tasks[0].Meta["depends-on"] != "3" {
		t.Errorf("Meta[depends-on] = %q, want %q", f.Tasks[0].Meta["depends-on"], "3")
	}
}

func TestParseFile_ProjectMeta(t *testing.T) {
	input := `# TASKS

a description

* created: 2026-01-01T00:00:00Z
* next-id: 5

## 1 First task

* status: open`

	f := ParseFile(input)
	if f.Meta["created"] != "2026-01-01T00:00:00Z" {
		t.Errorf("Meta[created] = %q, want %q", f.Meta["created"], "2026-01-01T00:00:00Z")
	}
	if f.Meta["next-id"] != "5" {
		t.Errorf("Meta[next-id] = %q, want %q", f.Meta["next-id"], "5")
	}
	if len(f.Tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(f.Tasks))
	}
}

func TestParseFile_NonIntegerIDSkipped(t *testing.T) {
	input := `## abc Not a valid task

* status: open

## 1 Valid task

* status: open`

	f := ParseFile(input)
	if len(f.Tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(f.Tasks))
	}
	if f.Tasks[0].ID != 1 {
		t.Errorf("ID = %d, want 1", f.Tasks[0].ID)
	}
}

func TestParseMetaLine(t *testing.T) {
	tests := []struct {
		line    string
		wantKey string
		wantVal string
		wantOK  bool
	}{
		{"* status: open", "status", "open", true},
		{"* priority: 1", "priority", "1", true},
		{"* depends-on: 3", "depends-on", "3", true},
		{"* assignee: alice bob", "assignee", "alice bob", true},
		{"not a meta line", "", "", false},
		{"* no-colon-space", "", "", false},
		{"  * status: open", "status", "open", true}, // indented
	}
	for _, tt := range tests {
		key, val, ok := parseMetaLine(tt.line)
		if ok != tt.wantOK || key != tt.wantKey || val != tt.wantVal {
			t.Errorf("parseMetaLine(%q) = (%q, %q, %v), want (%q, %q, %v)",
				tt.line, key, val, ok, tt.wantKey, tt.wantVal, tt.wantOK)
		}
	}
}

func TestSplitHeading(t *testing.T) {
	tests := []struct {
		input     string
		wantID    int
		wantTitle string
	}{
		{"1 Fix the login bug", 1, "Fix the login bug"},
		{"1", 1, ""},
		{"0001 Padded ID", 1, "Padded ID"},
		{"42 Do something", 42, "Do something"},
	}
	for _, tt := range tests {
		id, title := splitHeading(tt.input)
		if id != tt.wantID || title != tt.wantTitle {
			t.Errorf("splitHeading(%q) = (%d, %q), want (%d, %q)",
				tt.input, id, title, tt.wantID, tt.wantTitle)
		}
	}
}

func TestSplitHeading_Invalid(t *testing.T) {
	id, _ := splitHeading("abc Do something")
	if id != -1 {
		t.Errorf("splitHeading(\"abc Do something\") = %d, want -1", id)
	}
}

func TestFormatTask_Full(t *testing.T) {
	task := Task{
		ID:     1,
		Title:  "Fix the login bug",
		Status: "open",
		Meta:   map[string]string{"status": "open", "priority": "3"},
		Body:   "Some description.",
	}
	got := FormatTask(task)
	want := "## 1 Fix the login bug\n\n* status: open\n* priority: 3\n\nSome description.\n"
	if got != want {
		t.Errorf("FormatTask =\n%q\nwant\n%q", got, want)
	}
}

func TestFormatTask_NoBody(t *testing.T) {
	task := Task{
		ID:    2,
		Title: "No body",
		Meta:  map[string]string{"status": "closed"},
	}
	got := FormatTask(task)
	want := "## 2 No body\n\n* status: closed\n"
	if got != want {
		t.Errorf("FormatTask =\n%q\nwant\n%q", got, want)
	}
}

func TestFormatTask_NoMeta(t *testing.T) {
	task := Task{
		ID:    3,
		Title: "Bare task",
		Meta:  map[string]string{},
		Body:  "Just a body.",
	}
	got := FormatTask(task)
	want := "## 3 Bare task\n\nJust a body.\n"
	if got != want {
		t.Errorf("FormatTask =\n%q\nwant\n%q", got, want)
	}
}

func TestFormatTask_UpdatedSameAsCreated(t *testing.T) {
	task := Task{
		ID:    1,
		Title: "Test",
		Meta: map[string]string{
			"status":  "open",
			"created": "2026-01-01T00:00:00Z",
			"updated": "2026-01-01T00:00:00Z",
		},
	}
	got := FormatTask(task)
	// updated should be omitted since it equals created
	want := "## 1 Test\n\n* status: open\n* created: 2026-01-01T00:00:00Z\n"
	if got != want {
		t.Errorf("FormatTask =\n%q\nwant\n%q", got, want)
	}
}

func TestFormatTask_UpdatedDifferentFromCreated(t *testing.T) {
	task := Task{
		ID:    1,
		Title: "Test",
		Meta: map[string]string{
			"status":  "open",
			"created": "2026-01-01T00:00:00Z",
			"updated": "2026-01-02T00:00:00Z",
		},
	}
	got := FormatTask(task)
	want := "## 1 Test\n\n* status: open\n* created: 2026-01-01T00:00:00Z\n* updated: 2026-01-02T00:00:00Z\n"
	if got != want {
		t.Errorf("FormatTask =\n%q\nwant\n%q", got, want)
	}
}

func TestFormatFile_WithProjectMeta(t *testing.T) {
	f := File{
		Meta: map[string]string{
			"created": "2026-01-01T00:00:00Z",
			"next-id": "3",
		},
		Tasks: []Task{
			{
				ID:    1,
				Title: "First",
				Meta:  map[string]string{"status": "open"},
			},
		},
	}
	got := FormatFile(f)
	want := "# TASKS\n\na [meads](https://github.com/jpillora/meads) (`md`) managed task log\n\n* created: 2026-01-01T00:00:00Z\n* next-id: 3\n\n## 1 First\n\n* status: open\n"
	if got != want {
		t.Errorf("FormatFile =\n%q\nwant\n%q", got, want)
	}
}

func TestFormatFile_ProjectMetaUpdatedSkipped(t *testing.T) {
	f := File{
		Meta: map[string]string{
			"created": "2026-01-01T00:00:00Z",
			"updated": "2026-01-01T00:00:00Z",
			"next-id": "2",
		},
		Tasks: []Task{
			{ID: 1, Title: "Test", Meta: map[string]string{"status": "open"}},
		},
	}
	got := FormatFile(f)
	// updated should be omitted since it equals created
	want := "# TASKS\n\na [meads](https://github.com/jpillora/meads) (`md`) managed task log\n\n* created: 2026-01-01T00:00:00Z\n* next-id: 2\n\n## 1 Test\n\n* status: open\n"
	if got != want {
		t.Errorf("FormatFile =\n%q\nwant\n%q", got, want)
	}
}

func TestFormatFile_RoundTrip(t *testing.T) {
	input := "# TASKS\n\na [meads](https://github.com/jpillora/meads) (`md`) managed task log\n\n* created: 2026-01-01T00:00:00Z\n* next-id: 3\n\n## 1 First task\n\n* status: open\n* priority: 2\n\nDo the first thing.\n\n## 2 Second task\n\n* status: closed\n* priority: 1\n\nDo the second thing.\n"

	f := ParseFile(input)
	if len(f.Tasks) != 2 {
		t.Fatalf("expected 2 tasks, got %d", len(f.Tasks))
	}
	output := FormatFile(f)
	// Re-parse the output and verify tasks are preserved.
	f2 := ParseFile(output)
	if len(f2.Tasks) != 2 {
		t.Fatalf("round-trip: expected 2 tasks, got %d", len(f2.Tasks))
	}
	for i := range f.Tasks {
		if f.Tasks[i].ID != f2.Tasks[i].ID {
			t.Errorf("round-trip task %d: ID %d != %d", i, f.Tasks[i].ID, f2.Tasks[i].ID)
		}
		if f.Tasks[i].Title != f2.Tasks[i].Title {
			t.Errorf("round-trip task %d: Title %q != %q", i, f.Tasks[i].Title, f2.Tasks[i].Title)
		}
		if f.Tasks[i].Status != f2.Tasks[i].Status {
			t.Errorf("round-trip task %d: Status %q != %q", i, f.Tasks[i].Status, f2.Tasks[i].Status)
		}
		if f.Tasks[i].Body != f2.Tasks[i].Body {
			t.Errorf("round-trip task %d: Body %q != %q", i, f.Tasks[i].Body, f2.Tasks[i].Body)
		}
	}
	// Verify project meta round-trip.
	if f.Meta["created"] != f2.Meta["created"] {
		t.Errorf("round-trip: created %q != %q", f.Meta["created"], f2.Meta["created"])
	}
	if f.Meta["next-id"] != f2.Meta["next-id"] {
		t.Errorf("round-trip: next-id %q != %q", f.Meta["next-id"], f2.Meta["next-id"])
	}
}

func TestParseFile_TypeField(t *testing.T) {
	input := `## 1 Add login

* status: open
* type: feature`

	f := ParseFile(input)
	if len(f.Tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(f.Tasks))
	}
	if f.Tasks[0].Type != "feature" {
		t.Errorf("Type = %q, want %q", f.Tasks[0].Type, "feature")
	}
}

func TestParseFile_CloseReasonField(t *testing.T) {
	input := `## 1 Old bug

* status: closed
* close-reason: duplicate`

	f := ParseFile(input)
	if len(f.Tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(f.Tasks))
	}
	if f.Tasks[0].CloseReason != "duplicate" {
		t.Errorf("CloseReason = %q, want %q", f.Tasks[0].CloseReason, "duplicate")
	}
}

func TestParseFile_TagsField(t *testing.T) {
	input := `## 1 Tagged task

* status: open
* tags: backend,api,urgent`

	f := ParseFile(input)
	if len(f.Tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(f.Tasks))
	}
	tags := f.Tasks[0].Tags
	if len(tags) != 3 {
		t.Fatalf("expected 3 tags, got %d", len(tags))
	}
	if tags[0] != "backend" || tags[1] != "api" || tags[2] != "urgent" {
		t.Errorf("Tags = %v, want [backend api urgent]", tags)
	}
}

func TestNewFields_RoundTrip(t *testing.T) {
	task := Task{
		ID:          1,
		Title:       "Round trip test",
		Status:      "closed",
		Priority:    "P1",
		Type:        "bug",
		CloseReason: "fixed",
		Tags:        []string{"backend", "api"},
	}
	task.ensureMeta()
	task.Meta["status"] = "closed"
	task.Meta["priority"] = "P1"
	task.Meta["type"] = "bug"
	task.Meta["close-reason"] = "fixed"
	task.Meta["tags"] = "backend,api"

	formatted := FormatTask(task)
	f := ParseFile(formatted)
	if len(f.Tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(f.Tasks))
	}
	got := f.Tasks[0]
	if got.Priority != "P1" {
		t.Errorf("Priority = %q, want %q", got.Priority, "P1")
	}
	if got.Type != "bug" {
		t.Errorf("Type = %q, want %q", got.Type, "bug")
	}
	if got.CloseReason != "fixed" {
		t.Errorf("CloseReason = %q, want %q", got.CloseReason, "fixed")
	}
	if len(got.Tags) != 2 || got.Tags[0] != "backend" || got.Tags[1] != "api" {
		t.Errorf("Tags = %v, want [backend api]", got.Tags)
	}
}

func TestParseFile_StringPriority(t *testing.T) {
	input := `## 1 P-string task

* status: open
* priority: P0`

	f := ParseFile(input)
	if len(f.Tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(f.Tasks))
	}
	if f.Tasks[0].Priority != "P0" {
		t.Errorf("Priority = %q, want %q", f.Tasks[0].Priority, "P0")
	}
}
