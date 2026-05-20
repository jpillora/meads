package meads

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// File represents a parsed TASKS.md file.
type File struct {
	Meta  map[string]string `json:"meta,omitempty"`
	Tasks []Task            `json:"tasks"`
}

// Task represents a single task parsed from a TASKS.md file.
type Task struct {
	ID           int               `json:"id"`
	Title        string            `json:"title"`
	Status       string            `json:"status,omitempty"`
	Priority     string            `json:"priority,omitempty"`
	Type         string            `json:"type,omitempty"`
	DependsOn    []int             `json:"depends_on,omitempty"`
	CloseReason  string            `json:"close_reason,omitempty"`
	Tags         []string          `json:"tags,omitempty"`
	Deleted      bool              `json:"deleted,omitempty"`
	StatusReason string            `json:"status_reason,omitempty"`
	Meta         map[string]string `json:"meta,omitempty"`
	Description  string            `json:"description,omitempty"`
}

// knownMetaKeys are metadata keys that have dedicated struct fields.
// These are excluded from the "meta" JSON field to avoid duplication.
var knownMetaKeys = map[string]bool{
	"status":       true,
	"priority":     true,
	"type":         true,
	"depends-on":   true,
	"close-reason": true,
	"tags":         true,
}

// MarshalJSON implements custom JSON marshaling to exclude known keys from meta.
func (t Task) MarshalJSON() ([]byte, error) {
	var meta map[string]string
	for k, v := range t.Meta {
		if !knownMetaKeys[k] {
			if meta == nil {
				meta = make(map[string]string)
			}
			meta[k] = v
		}
	}
	type taskJSON Task
	out := taskJSON(t)
	// Fill in inferred defaults and normalize for JSON output.
	if out.Priority == "" {
		out.Priority = "P2"
	} else if norm, err := NormalizePriority(out.Priority); err == nil {
		out.Priority = norm
	}
	if out.Type == "" {
		out.Type = "task"
	}
	return json.Marshal(struct {
		taskJSON
		Meta map[string]string `json:"meta,omitempty"`
	}{
		taskJSON: out,
		Meta:     meta,
	})
}

// SetStatus updates the task status in both the field and Meta map.
func (t *Task) SetStatus(s string) {
	t.Status = s
	t.ensureMeta()
	t.Meta["status"] = s
}

// NormalizePriority accepts priority in various formats ("P1", "p1", "1")
// and returns the canonical "P#" form, or an error for invalid input.
func NormalizePriority(s string) (string, error) {
	s = strings.TrimSpace(s)
	// Strip leading P/p prefix if present
	num := strings.TrimPrefix(strings.TrimPrefix(s, "P"), "p")
	if len(num) != 1 || num[0] < '0' || num[0] > '9' {
		return "", fmt.Errorf("invalid priority %q: must be P0-P9 or 0-9", s)
	}
	return "P" + num, nil
}

// ValidStatuses lists the recognized task statuses.
var ValidStatuses = []string{"draft", "open", "inprogress", "blocked", "closed"}

// ValidTypes lists the recognized task types.
var ValidTypes = []string{"bug", "task", "feature", "idea"}

// ValidateStatus returns an error if s is not a recognized status.
func ValidateStatus(s string) error {
	for _, v := range ValidStatuses {
		if s == v {
			return nil
		}
	}
	return fmt.Errorf("invalid status %q: must be one of %s", s, strings.Join(ValidStatuses, ", "))
}

// ValidateType returns an error if s is not a recognized type.
func ValidateType(s string) error {
	for _, v := range ValidTypes {
		if s == v {
			return nil
		}
	}
	return fmt.Errorf("invalid type %q: must be one of %s", s, strings.Join(ValidTypes, ", "))
}

// SetPriority updates the task priority in both the field and Meta map.
// It normalizes the value to canonical "P#" form if valid.
func (t *Task) SetPriority(p string) {
	if norm, err := NormalizePriority(p); err == nil {
		p = norm
	}
	t.Priority = p
	t.ensureMeta()
	t.Meta["priority"] = p
}

// SetType updates the task type in both the field and Meta map.
func (t *Task) SetType(s string) {
	t.Type = s
	t.ensureMeta()
	t.Meta["type"] = s
}

// SetCloseReason updates the close reason in both the field and Meta map.
func (t *Task) SetCloseReason(s string) {
	t.CloseReason = s
	t.ensureMeta()
	t.Meta["close-reason"] = s
}

// SetTags updates the tags in both the field and Meta map.
func (t *Task) SetTags(tags []string) {
	t.Tags = tags
	t.ensureMeta()
	t.Meta["tags"] = strings.Join(tags, ",")
}

// SetDependsOn updates the depends-on list in both the field and Meta map.
func (t *Task) SetDependsOn(ids []int) {
	t.DependsOn = ids
	t.ensureMeta()
	t.Meta["depends-on"] = formatIntSlice(ids)
}

// AddDep adds a dependency ID if not already present.
func (t *Task) AddDep(id int) {
	for _, d := range t.DependsOn {
		if d == id {
			return
		}
	}
	t.SetDependsOn(append(t.DependsOn, id))
}

// RemoveDep removes a dependency ID if present; no-op otherwise.
func (t *Task) RemoveDep(id int) {
	out := t.DependsOn[:0:0]
	for _, d := range t.DependsOn {
		if d != id {
			out = append(out, d)
		}
	}
	t.SetDependsOn(out)
}

// SetMeta sets a metadata key-value pair and syncs convenience fields.
func (t *Task) SetMeta(key, value string) {
	t.ensureMeta()
	t.Meta[key] = value
	switch key {
	case "status":
		t.Status = value
	case "priority":
		t.Priority = value
	case "type":
		t.Type = value
	case "depends-on":
		t.DependsOn = parseIntSlice(value)
	case "close-reason":
		t.CloseReason = value
	case "tags":
		t.Tags = splitTags(value)
	}
}

func (t *Task) ensureMeta() {
	if t.Meta == nil {
		t.Meta = make(map[string]string)
	}
}

// parseIntSlice parses a comma-separated string of integers.
func parseIntSlice(s string) []int {
	var ids []int
	for _, p := range strings.Split(s, ",") {
		p = strings.TrimSpace(p)
		if n, err := strconv.Atoi(p); err == nil {
			ids = append(ids, n)
		}
	}
	return ids
}

// formatIntSlice formats a slice of ints as a comma-separated string.
func formatIntSlice(ids []int) string {
	parts := make([]string, len(ids))
	for i, id := range ids {
		parts[i] = strconv.Itoa(id)
	}
	return strings.Join(parts, ",")
}

// splitTags splits a comma-separated string into trimmed non-empty tags.
func splitTags(s string) []string {
	var tags []string
	for _, t := range strings.Split(s, ",") {
		t = strings.TrimSpace(t)
		if t != "" {
			tags = append(tags, t)
		}
	}
	return tags
}

