package meads

import (
	"testing"
)

func TestParseTasks_SingleTask(t *testing.T) {
	input := `## 0001 Fix the login bug

* status: open
* priority: 1
* depends-on: 0003

The login page throws a 500 when the session cookie is expired.`

	tasks := ParseTasks(input)
	if len(tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(tasks))
	}
	task := tasks[0]
	if task.ID != "0001" {
		t.Errorf("ID = %q, want %q", task.ID, "0001")
	}
	if task.Title != "Fix the login bug" {
		t.Errorf("Title = %q, want %q", task.Title, "Fix the login bug")
	}
	if task.Status != "open" {
		t.Errorf("Status = %q, want %q", task.Status, "open")
	}
	if task.Priority != 1 {
		t.Errorf("Priority = %d, want %d", task.Priority, 1)
	}
	if task.DependsOn != "0003" {
		t.Errorf("DependsOn = %q, want %q", task.DependsOn, "0003")
	}
	if task.Body != "The login page throws a 500 when the session cookie is expired." {
		t.Errorf("Body = %q, want %q", task.Body, "The login page throws a 500 when the session cookie is expired.")
	}
}

func TestParseTasks_MultipleTasks(t *testing.T) {
	input := `## 0001 First task

* status: open
* priority: 2

Do the first thing.

## 0002 Second task

* status: closed
* priority: 1

Do the second thing.

## 0003 Third task

* status: inprogress

Working on it.`

	tasks := ParseTasks(input)
	if len(tasks) != 3 {
		t.Fatalf("expected 3 tasks, got %d", len(tasks))
	}
	if tasks[0].ID != "0001" || tasks[0].Title != "First task" {
		t.Errorf("task 0: ID=%q Title=%q", tasks[0].ID, tasks[0].Title)
	}
	if tasks[0].Status != "open" || tasks[0].Priority != 2 {
		t.Errorf("task 0: Status=%q Priority=%d", tasks[0].Status, tasks[0].Priority)
	}
	if tasks[1].ID != "0002" || tasks[1].Status != "closed" {
		t.Errorf("task 1: ID=%q Status=%q", tasks[1].ID, tasks[1].Status)
	}
	if tasks[2].ID != "0003" || tasks[2].Status != "inprogress" {
		t.Errorf("task 2: ID=%q Status=%q", tasks[2].ID, tasks[2].Status)
	}
	if tasks[2].Body != "Working on it." {
		t.Errorf("task 2: Body=%q", tasks[2].Body)
	}
}

func TestParseTasks_NoMetadata(t *testing.T) {
	input := `## 0001 Bare task

Just a description with no metadata.`

	tasks := ParseTasks(input)
	if len(tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(tasks))
	}
	task := tasks[0]
	if task.ID != "0001" {
		t.Errorf("ID = %q", task.ID)
	}
	if task.Status != "" {
		t.Errorf("Status = %q, want empty", task.Status)
	}
	if task.Priority != 0 {
		t.Errorf("Priority = %d, want 0", task.Priority)
	}
	if task.Body != "Just a description with no metadata." {
		t.Errorf("Body = %q", task.Body)
	}
}

func TestParseTasks_CustomMeta(t *testing.T) {
	input := `## 0001 Custom fields

* status: open
* priority: 3
* assignee: alice
* component: backend

Fix the API.`

	tasks := ParseTasks(input)
	if len(tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(tasks))
	}
	task := tasks[0]
	if task.Meta["assignee"] != "alice" {
		t.Errorf("Meta[assignee] = %q, want %q", task.Meta["assignee"], "alice")
	}
	if task.Meta["component"] != "backend" {
		t.Errorf("Meta[component] = %q, want %q", task.Meta["component"], "backend")
	}
}

func TestParseTasks_Empty(t *testing.T) {
	tasks := ParseTasks("")
	if len(tasks) != 0 {
		t.Fatalf("expected 0 tasks, got %d", len(tasks))
	}
}

func TestParseTasks_NoBody(t *testing.T) {
	input := `## 0001 Metadata only

* status: open
* priority: 1`

	tasks := ParseTasks(input)
	if len(tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(tasks))
	}
	if tasks[0].Body != "" {
		t.Errorf("Body = %q, want empty", tasks[0].Body)
	}
}

func TestParseTasks_MultilineBody(t *testing.T) {
	input := `## 0001 Multi-line body

* status: open

First paragraph.

Second paragraph with more detail.`

	tasks := ParseTasks(input)
	if len(tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(tasks))
	}
	want := "First paragraph.\n\nSecond paragraph with more detail."
	if tasks[0].Body != want {
		t.Errorf("Body = %q, want %q", tasks[0].Body, want)
	}
}

func TestParseTasks_IDOnly(t *testing.T) {
	input := `## 0001

* status: open`

	tasks := ParseTasks(input)
	if len(tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(tasks))
	}
	if tasks[0].ID != "0001" {
		t.Errorf("ID = %q", tasks[0].ID)
	}
	if tasks[0].Title != "" {
		t.Errorf("Title = %q, want empty", tasks[0].Title)
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
		{"* depends-on: 0003", "depends-on", "0003", true},
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
		wantID    string
		wantTitle string
	}{
		{"0001 Fix the login bug", "0001", "Fix the login bug"},
		{"0001", "0001", ""},
		{"abc Do something", "abc", "Do something"},
	}
	for _, tt := range tests {
		id, title := splitHeading(tt.input)
		if id != tt.wantID || title != tt.wantTitle {
			t.Errorf("splitHeading(%q) = (%q, %q), want (%q, %q)",
				tt.input, id, title, tt.wantID, tt.wantTitle)
		}
	}
}

func TestFormatTask_Full(t *testing.T) {
	task := Task{
		ID:     "0001",
		Title:  "Fix the login bug",
		Status: "open",
		Meta:   map[string]string{"status": "open", "priority": "3"},
		Body:   "Some description.",
	}
	got := FormatTask(task)
	want := "## 0001 Fix the login bug\n\n* status: open\n* priority: 3\n\nSome description.\n"
	if got != want {
		t.Errorf("FormatTask =\n%q\nwant\n%q", got, want)
	}
}

func TestFormatTask_NoBody(t *testing.T) {
	task := Task{
		ID:    "0002",
		Title: "No body",
		Meta:  map[string]string{"status": "closed"},
	}
	got := FormatTask(task)
	want := "## 0002 No body\n\n* status: closed\n"
	if got != want {
		t.Errorf("FormatTask =\n%q\nwant\n%q", got, want)
	}
}

func TestFormatTask_NoMeta(t *testing.T) {
	task := Task{
		ID:    "0003",
		Title: "Bare task",
		Meta:  map[string]string{},
		Body:  "Just a body.",
	}
	got := FormatTask(task)
	want := "## 0003 Bare task\n\nJust a body.\n"
	if got != want {
		t.Errorf("FormatTask =\n%q\nwant\n%q", got, want)
	}
}

func TestFormatTasks_RoundTrip(t *testing.T) {
	input := `## 0001 First task

* status: open
* priority: 2

Do the first thing.

## 0002 Second task

* status: closed
* priority: 1

Do the second thing.`

	tasks := ParseTasks(input)
	if len(tasks) != 2 {
		t.Fatalf("expected 2 tasks, got %d", len(tasks))
	}
	output := FormatTasks(tasks)
	// Re-parse the output and verify tasks are preserved.
	tasks2 := ParseTasks(output)
	if len(tasks2) != 2 {
		t.Fatalf("round-trip: expected 2 tasks, got %d", len(tasks2))
	}
	for i := range tasks {
		if tasks[i].ID != tasks2[i].ID {
			t.Errorf("round-trip task %d: ID %q != %q", i, tasks[i].ID, tasks2[i].ID)
		}
		if tasks[i].Title != tasks2[i].Title {
			t.Errorf("round-trip task %d: Title %q != %q", i, tasks[i].Title, tasks2[i].Title)
		}
		if tasks[i].Status != tasks2[i].Status {
			t.Errorf("round-trip task %d: Status %q != %q", i, tasks[i].Status, tasks2[i].Status)
		}
		if tasks[i].Body != tasks2[i].Body {
			t.Errorf("round-trip task %d: Body %q != %q", i, tasks[i].Body, tasks2[i].Body)
		}
	}
}
