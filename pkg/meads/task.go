package meads

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// Task represents a single task parsed from a TASKS.md file.
type Task struct {
	ID        string            `json:"id"`
	Title     string            `json:"title"`
	Status    string            `json:"status,omitempty"`
	Priority  int               `json:"priority,omitempty"`
	DependsOn string            `json:"depends_on,omitempty"`
	Meta      map[string]string `json:"meta,omitempty"`
	Body      string            `json:"body,omitempty"`
}

// knownMetaKeys are metadata keys that have dedicated struct fields.
// These are excluded from the "meta" JSON field to avoid duplication.
var knownMetaKeys = map[string]bool{
	"status":     true,
	"priority":   true,
	"depends-on": true,
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
	return json.Marshal(struct {
		taskJSON
		Meta map[string]string `json:"meta,omitempty"`
	}{
		taskJSON: taskJSON(t),
		Meta:     meta,
	})
}

// SetStatus updates the task status in both the field and Meta map.
func (t *Task) SetStatus(s string) {
	t.Status = s
	t.ensureMeta()
	t.Meta["status"] = s
}

// SetPriority updates the task priority in both the field and Meta map.
func (t *Task) SetPriority(p int) {
	t.Priority = p
	t.ensureMeta()
	t.Meta["priority"] = strconv.Itoa(p)
}

// SetMeta sets a metadata key-value pair and syncs convenience fields.
func (t *Task) SetMeta(key, value string) {
	t.ensureMeta()
	t.Meta[key] = value
	switch key {
	case "status":
		t.Status = value
	case "priority":
		if n, err := strconv.Atoi(value); err == nil {
			t.Priority = n
		}
	case "depends-on":
		t.DependsOn = value
	}
}

func (t *Task) ensureMeta() {
	if t.Meta == nil {
		t.Meta = make(map[string]string)
	}
}

// FormatTask formats a single Task as a markdown section.
func FormatTask(t Task) string {
	var sb strings.Builder
	if t.Title != "" {
		fmt.Fprintf(&sb, "## %s %s\n", t.ID, t.Title)
	} else {
		fmt.Fprintf(&sb, "## %s\n", t.ID)
	}
	if len(t.Meta) > 0 {
		sb.WriteString("\n")
		// Write well-known keys in a fixed order.
		ordered := []string{"status", "priority", "depends-on"}
		written := make(map[string]bool)
		for _, key := range ordered {
			if val, ok := t.Meta[key]; ok {
				fmt.Fprintf(&sb, "* %s: %s\n", key, val)
				written[key] = true
			}
		}
		// Remaining keys sorted alphabetically.
		var rest []string
		for key := range t.Meta {
			if !written[key] {
				rest = append(rest, key)
			}
		}
		sort.Strings(rest)
		for _, key := range rest {
			fmt.Fprintf(&sb, "* %s: %s\n", key, t.Meta[key])
		}
	}
	if t.Body != "" {
		sb.WriteString("\n")
		sb.WriteString(t.Body)
		sb.WriteString("\n")
	}
	return sb.String()
}

const fileHeader = "# TASKS\n\na [meads](https://github.com/jpillora/meads) (`md`) managed task log\n"

// FormatTasks formats all tasks as a complete TASKS.md file.
func FormatTasks(tasks []Task) string {
	if len(tasks) == 0 {
		return fileHeader
	}
	var parts []string
	for _, t := range tasks {
		parts = append(parts, FormatTask(t))
	}
	return fileHeader + "\n" + strings.Join(parts, "\n")
}
