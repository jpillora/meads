package meads

import "testing"

func TestBeadToTask_BasicMapping(t *testing.T) {
	b := beadIssue{
		ID:       "rais-42",
		Title:    "Fix crash on startup",
		Description: "App crashes when no config file exists.",
		Status:   "open",
		Priority: 1,
		IssueType: "bug",
		CreatedAt: "2026-01-01T00:00:00Z",
		UpdatedAt: "2026-01-02T00:00:00Z",
	}
	task := beadToTask(b)
	if task.Title != "Fix crash on startup" {
		t.Errorf("Title = %q", task.Title)
	}
	if task.Description != "App crashes when no config file exists." {
		t.Errorf("Description = %q", task.Description)
	}
	if task.Status != "open" {
		t.Errorf("Status = %q, want %q", task.Status, "open")
	}
	if task.Priority != "P1" {
		t.Errorf("Priority = %q, want %q", task.Priority, "P1")
	}
	if task.Type != "bug" {
		t.Errorf("Type = %q, want %q", task.Type, "bug")
	}
	if task.Meta["bead-id"] != "rais-42" {
		t.Errorf("Meta[bead-id] = %q, want %q", task.Meta["bead-id"], "rais-42")
	}
	if task.Meta["created"] != "2026-01-01T00:00:00Z" {
		t.Errorf("Meta[created] = %q", task.Meta["created"])
	}
	if task.Meta["updated"] != "2026-01-02T00:00:00Z" {
		t.Errorf("Meta[updated] = %q", task.Meta["updated"])
	}
}

func TestBeadToTask_StatusMapping(t *testing.T) {
	tests := []struct {
		beadStatus string
		wantStatus string
		wantMeta   string
	}{
		{"open", "open", ""},
		{"in_progress", "inprogress", ""},
		{"closed", "closed", ""},
		{"blocked", "open", "blocked"},
		{"deferred", "open", "deferred"},
	}
	for _, tt := range tests {
		b := beadIssue{ID: "1", Title: "t", Status: tt.beadStatus}
		task := beadToTask(b)
		if task.Status != tt.wantStatus {
			t.Errorf("status %q: got Status=%q, want %q", tt.beadStatus, task.Status, tt.wantStatus)
		}
		if tt.wantMeta != "" {
			if task.Meta["bead-status"] != tt.wantMeta {
				t.Errorf("status %q: Meta[bead-status]=%q, want %q", tt.beadStatus, task.Meta["bead-status"], tt.wantMeta)
			}
		}
	}
}

func TestBeadToTask_PriorityMapping(t *testing.T) {
	for i := 0; i <= 4; i++ {
		b := beadIssue{ID: "1", Title: "t", Priority: i}
		task := beadToTask(b)
		want := "P" + string(rune('0'+i))
		if task.Priority != want {
			t.Errorf("priority %d: got %q, want %q", i, task.Priority, want)
		}
	}
}

func TestBeadToTask_LabelsToTags(t *testing.T) {
	b := beadIssue{
		ID:     "1",
		Title:  "t",
		Labels: []string{"frontend", "urgent"},
	}
	task := beadToTask(b)
	if len(task.Tags) != 2 {
		t.Fatalf("expected 2 tags, got %d", len(task.Tags))
	}
	if task.Tags[0] != "frontend" || task.Tags[1] != "urgent" {
		t.Errorf("Tags = %v", task.Tags)
	}
}

func TestBeadToTask_CloseReason(t *testing.T) {
	b := beadIssue{
		ID:          "1",
		Title:       "t",
		Status:      "closed",
		CloseReason: "won't fix",
	}
	task := beadToTask(b)
	if task.CloseReason != "won't fix" {
		t.Errorf("CloseReason = %q, want %q", task.CloseReason, "won't fix")
	}
}

func TestBeadToTask_OptionalFields(t *testing.T) {
	b := beadIssue{
		ID:       "1",
		Title:    "t",
		Owner:    "alice",
		Assignee: "bob",
		ClosedAt: "2026-01-05T00:00:00Z",
		Metadata: map[string]string{"repo": "main", "sprint": "3"},
	}
	task := beadToTask(b)
	if task.Meta["owner"] != "alice" {
		t.Errorf("Meta[owner] = %q", task.Meta["owner"])
	}
	if task.Meta["assignee"] != "bob" {
		t.Errorf("Meta[assignee] = %q", task.Meta["assignee"])
	}
	if task.Meta["closed-at"] != "2026-01-05T00:00:00Z" {
		t.Errorf("Meta[closed-at] = %q", task.Meta["closed-at"])
	}
	if task.Meta["bead-meta-repo"] != "main" {
		t.Errorf("Meta[bead-meta-repo] = %q", task.Meta["bead-meta-repo"])
	}
	if task.Meta["bead-meta-sprint"] != "3" {
		t.Errorf("Meta[bead-meta-sprint] = %q", task.Meta["bead-meta-sprint"])
	}
}

func TestBeadToTask_CustomStatus(t *testing.T) {
	b := beadIssue{ID: "1", Title: "t", Status: "custom_status"}
	task := beadToTask(b)
	if task.Status != "custom_status" {
		t.Errorf("Status = %q, want %q", task.Status, "custom_status")
	}
}

func TestBeadToTask_CustomIssueType(t *testing.T) {
	b := beadIssue{ID: "1", Title: "t", IssueType: "epic"}
	task := beadToTask(b)
	if task.Type != "" {
		t.Errorf("Type = %q, want empty", task.Type)
	}
	if task.Meta["bead-type"] != "epic" {
		t.Errorf("Meta[bead-type] = %q, want %q", task.Meta["bead-type"], "epic")
	}
}

func TestBeadToTask_EmptyOptionalFieldsOmitted(t *testing.T) {
	b := beadIssue{ID: "1", Title: "t"}
	task := beadToTask(b)
	if _, ok := task.Meta["owner"]; ok {
		t.Errorf("Meta[owner] should not be set")
	}
	if _, ok := task.Meta["assignee"]; ok {
		t.Errorf("Meta[assignee] should not be set")
	}
	if _, ok := task.Meta["closed-at"]; ok {
		t.Errorf("Meta[closed-at] should not be set")
	}
	if task.CloseReason != "" {
		t.Errorf("CloseReason should be empty")
	}
}
