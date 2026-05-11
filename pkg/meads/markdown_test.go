package meads

import (
	"strings"
	"testing"
)

// Whitebox tests for unexported markdown helpers.

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
		{"  * status: open", "status", "open", true},
		// CommonMark also allows "-" and "+" as unordered list markers.
		{"- status: open", "status", "open", true},
		{"- depends-on: 3", "depends-on", "3", true},
		{"  - status: open", "status", "open", true},
		{"+ status: open", "status", "open", true},
		{"-status: open", "", "", false}, // no space after marker
		{"-", "", "", false},
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
		{"1. Fix the login bug", 1, "Fix the login bug"},
		{"1", 1, ""},
		{"1.", 1, ""},
		{"0001 Padded ID", 1, "Padded ID"},
		{"42 Do something", 42, "Do something"},
		{"42. Do something", 42, "Do something"},
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

// TestParseFile_DashMarkers covers the real-world case where a TASKS.md was
// authored with "-" list markers for metadata instead of "*". The parser must
// extract structured fields (status/priority/type/depends-on) and must NOT
// leak the metadata block into the description.
//
// Also asserts the auto-migration property: re-formatting the file rewrites
// dash markers as "*", so the file normalizes on the next write.
func TestParseFile_DashMarkers(t *testing.T) {
	const input = `# TASKS

- created: 2026-02-14T15:58:00Z
- updated: 2026-02-14T16:01:02Z

## 1. Scaffold project

- status: closed
- priority: P1
- type: feature
- created: 2026-02-14T15:58:00Z
- updated: 2026-02-14T16:01:02Z

Initialize with Vite, Vue 3, TypeScript.

## 2. Child task

- status: closed
- priority: P2
- type: task
- depends-on: 1
- created: 2026-02-14T15:58:07Z

Body text for the child task.
`

	f := ParseFile(input)
	if len(f.Tasks) != 2 {
		t.Fatalf("expected 2 tasks, got %d", len(f.Tasks))
	}

	// Preamble metadata also uses dash markers — make sure it parses.
	if f.Meta["created"] != "2026-02-14T15:58:00Z" {
		t.Errorf("preamble created = %q, want %q", f.Meta["created"], "2026-02-14T15:58:00Z")
	}

	t1 := f.Tasks[0]
	if t1.Status != "closed" || t1.Priority != "P1" || t1.Type != "feature" {
		t.Errorf("task 1 fields: status=%q priority=%q type=%q", t1.Status, t1.Priority, t1.Type)
	}
	if want := "Initialize with Vite, Vue 3, TypeScript."; t1.Description != want {
		t.Errorf("task 1 description = %q, want %q", t1.Description, want)
	}
	// The metadata block must not leak into the description.
	if strings.Contains(t1.Description, "status:") || strings.Contains(t1.Description, "priority:") {
		t.Errorf("task 1 description leaked metadata: %q", t1.Description)
	}

	t2 := f.Tasks[1]
	if len(t2.DependsOn) != 1 || t2.DependsOn[0] != 1 {
		t.Errorf("task 2 depends-on = %v, want [1]", t2.DependsOn)
	}

	// Auto-migration: re-formatting must rewrite "-" markers as "*".
	formatted := FormatFile(f)
	if strings.Contains(formatted, "\n- status:") || strings.Contains(formatted, "\n- priority:") {
		t.Errorf("FormatFile retained dash markers; expected normalization to '*':\n%s", formatted)
	}
	if !strings.Contains(formatted, "\n* status: closed") {
		t.Errorf("FormatFile missing expected '* status: closed' line:\n%s", formatted)
	}

	// Re-parsing the formatted output must yield the same structured fields
	// (proves the migration is lossless).
	f2 := ParseFile(formatted)
	if len(f2.Tasks) != 2 {
		t.Fatalf("re-parse: expected 2 tasks, got %d", len(f2.Tasks))
	}
	if f2.Tasks[0].Status != "closed" || f2.Tasks[0].Priority != "P1" || f2.Tasks[0].Type != "feature" {
		t.Errorf("re-parse task 1 lost fields: %+v", f2.Tasks[0])
	}
	if len(f2.Tasks[1].DependsOn) != 1 || f2.Tasks[1].DependsOn[0] != 1 {
		t.Errorf("re-parse task 2 depends-on = %v, want [1]", f2.Tasks[1].DependsOn)
	}
}

func TestNewFields_RoundTrip(t *testing.T) {
	task := Task{
		ID: 1, Title: "Round trip test", Status: "closed",
		Priority: "P1", Type: "bug", CloseReason: "fixed",
		Tags: []string{"backend", "api"},
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
	if got.Priority != "P1" || got.Type != "bug" || got.CloseReason != "fixed" {
		t.Errorf("fields mismatch: P=%q T=%q CR=%q", got.Priority, got.Type, got.CloseReason)
	}
	if len(got.Tags) != 2 || got.Tags[0] != "backend" || got.Tags[1] != "api" {
		t.Errorf("Tags = %v", got.Tags)
	}
}
