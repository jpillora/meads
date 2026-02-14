package meads

import (
	"encoding/json"
	"fmt"
	"sort"
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
	ID          int               `json:"id"`
	Title       string            `json:"title"`
	Status      string            `json:"status,omitempty"`
	Priority    string            `json:"priority,omitempty"`
	Type        string            `json:"type,omitempty"`
	DependsOn   int               `json:"depends_on,omitempty"`
	CloseReason string            `json:"close_reason,omitempty"`
	Tags        []string          `json:"tags,omitempty"`
	Meta        map[string]string `json:"meta,omitempty"`
	Body        string            `json:"body,omitempty"`
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
func (t *Task) SetPriority(p string) {
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
		if n, err := strconv.Atoi(value); err == nil {
			t.DependsOn = n
		}
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

var (
	projectMetaOrder = []string{"created", "updated", "next-id"}
	taskMetaOrder    = []string{"status", "priority", "type", "depends-on", "close-reason", "tags", "created", "updated"}
)

// formatMetaBlock formats a metadata map as "* key: value" lines.
// orderedKeys controls which keys appear first, in the given order.
// The "updated" key is skipped if its value equals the "created" value.
func formatMetaBlock(meta map[string]string, orderedKeys []string) string {
	if len(meta) == 0 {
		return ""
	}
	var sb strings.Builder
	written := make(map[string]bool)
	for _, key := range orderedKeys {
		val, ok := meta[key]
		if !ok {
			continue
		}
		if key == "updated" {
			if created, ok := meta["created"]; ok && val == created {
				written[key] = true
				continue
			}
		}
		fmt.Fprintf(&sb, "* %s: %s\n", key, val)
		written[key] = true
	}
	var rest []string
	for k := range meta {
		if !written[k] {
			rest = append(rest, k)
		}
	}
	sort.Strings(rest)
	for _, k := range rest {
		fmt.Fprintf(&sb, "* %s: %s\n", k, meta[k])
	}
	return sb.String()
}

// FormatTask formats a single Task as a markdown section.
func FormatTask(t Task) string {
	var sb strings.Builder
	if t.Title != "" {
		fmt.Fprintf(&sb, "## %d %s\n", t.ID, t.Title)
	} else {
		fmt.Fprintf(&sb, "## %d\n", t.ID)
	}
	if metaBlock := formatMetaBlock(t.Meta, taskMetaOrder); metaBlock != "" {
		sb.WriteString("\n")
		sb.WriteString(metaBlock)
	}
	if t.Body != "" {
		sb.WriteString("\n")
		sb.WriteString(t.Body)
		sb.WriteString("\n")
	}
	return sb.String()
}

const fileHeader = "# TASKS\n\na [meads](https://github.com/jpillora/meads) (`md`) managed task log\n"

// FormatFile formats a complete TASKS.md file.
func FormatFile(f File) string {
	var sb strings.Builder
	sb.WriteString(fileHeader)
	if metaBlock := formatMetaBlock(f.Meta, projectMetaOrder); metaBlock != "" {
		sb.WriteString("\n")
		sb.WriteString(metaBlock)
	}
	for _, t := range f.Tasks {
		sb.WriteString("\n")
		sb.WriteString(FormatTask(t))
	}
	return sb.String()
}
