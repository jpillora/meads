package meads

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// Task represents a single task parsed from a TASKS.md file.
type Task struct {
	ID        string
	Title     string
	Status    string
	Priority  int
	DependsOn string
	Meta      map[string]string // all key-value pairs including status, priority, depends-on
	Body      string            // freeform description after metadata
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

// FormatTasks formats all tasks as a complete TASKS.md file.
func FormatTasks(tasks []Task) string {
	if len(tasks) == 0 {
		return ""
	}
	var parts []string
	for _, t := range tasks {
		parts = append(parts, FormatTask(t))
	}
	return strings.Join(parts, "\n")
}
