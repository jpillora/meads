package meads

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
)

func init() {
	imp := &beadsImporter{}
	RegisterImporter(imp)
	// Also register under plural form for convenience.
	importers["beads"] = imp
}

type beadsImporter struct{}

func (b *beadsImporter) Name() string { return "bead" }

func (b *beadsImporter) Import() ([]Task, error) {
	out, err := exec.Command("bd", "list", "--json", "--all", "-n", "0").Output()
	if err != nil {
		return nil, fmt.Errorf("running bd list: %w", err)
	}
	var issues []beadIssue
	if err := json.Unmarshal(out, &issues); err != nil {
		return nil, fmt.Errorf("parsing bd output: %w", err)
	}
	tasks := make([]Task, 0, len(issues))
	for _, issue := range issues {
		tasks = append(tasks, beadToTask(issue))
	}
	return tasks, nil
}

type beadIssue struct {
	ID              string            `json:"id"`
	Title           string            `json:"title"`
	Description     string            `json:"description"`
	Status          string            `json:"status"`
	Priority        int               `json:"priority"`
	IssueType       string            `json:"issue_type"`
	CreatedAt       string            `json:"created_at"`
	CreatedBy       string            `json:"created_by"`
	UpdatedAt       string            `json:"updated_at"`
	ClosedAt        string            `json:"closed_at"`
	CloseReason     string            `json:"close_reason"`
	Owner           string            `json:"owner"`
	Assignee        string            `json:"assignee"`
	Labels          []string          `json:"labels"`
	Metadata        map[string]string `json:"metadata"`
	DependencyCount int               `json:"dependency_count"`
	DependentCount  int               `json:"dependent_count"`
}

func beadToTask(b beadIssue) Task {
	t := Task{
		Title:       b.Title,
		Description: b.Description,
	}
	t.ensureMeta()
	// Status mapping
	switch b.Status {
	case "open":
		t.SetStatus("open")
	case "in_progress":
		t.SetStatus("inprogress")
	case "closed":
		t.SetStatus("closed")
	case "blocked", "deferred":
		t.SetStatus("open")
		t.Meta["bead-status"] = b.Status
	default:
		if b.Status != "" {
			t.SetStatus(b.Status)
		}
	}
	// Priority mapping: beads int 0-4 → P0-P4
	t.SetPriority("P" + strconv.Itoa(b.Priority))
	// Type mapping
	switch b.IssueType {
	case "task", "feature", "bug", "idea":
		t.SetType(b.IssueType)
	default:
		if b.IssueType != "" {
			t.Meta["bead-type"] = b.IssueType
		}
	}
	// Dedup key
	t.Meta["bead-id"] = b.ID
	// Timestamps
	if b.CreatedAt != "" {
		t.Meta["created"] = b.CreatedAt
	}
	if b.UpdatedAt != "" {
		t.Meta["updated"] = b.UpdatedAt
	}
	if b.ClosedAt != "" {
		t.Meta["closed-at"] = b.ClosedAt
	}
	// Close reason
	if b.CloseReason != "" {
		t.SetCloseReason(b.CloseReason)
	}
	// People
	if b.Owner != "" {
		t.Meta["owner"] = b.Owner
	}
	if b.Assignee != "" {
		t.Meta["assignee"] = b.Assignee
	}
	// Labels → Tags
	if len(b.Labels) > 0 {
		t.SetTags(b.Labels)
	}
	// Metadata (prefixed)
	for k, v := range b.Metadata {
		t.Meta["bead-meta-"+k] = v
	}
	return t
}
