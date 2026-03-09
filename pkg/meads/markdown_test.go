package meads

import "testing"

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
