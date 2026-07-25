package meads

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// markdownFormat implements Format for TASKS.md files.
type markdownFormat struct{}

func (markdownFormat) Parse(content string) File { return ParseFile(content) }
func (markdownFormat) Format(f File) string      { return FormatFile(f) }
func (markdownFormat) HasPreamble() bool         { return true }
func (markdownFormat) EmptyFile() string         { return "" }

// --- Parsing ---

// ParseFile parses the TASKS.md content and returns a File.
func ParseFile(content string) File {
	preamble, sections := splitSections(content)
	f := File{
		Meta:  parsePreambleMeta(preamble),
		Tasks: make([]Task, 0, len(sections)),
	}
	for _, sec := range sections {
		if t, ok := parseTask(sec); ok {
			f.Tasks = append(f.Tasks, t)
		}
	}
	return f
}

// splitSections splits markdown content into the preamble (before first ## section)
// and individual sections starting with "## ".
func splitSections(content string) (string, []string) {
	lines := strings.Split(content, "\n")
	var sections []string
	var preambleEnd int
	start := -1
	for i, line := range lines {
		if strings.HasPrefix(line, "## ") {
			if start >= 0 {
				sections = append(sections, strings.Join(lines[start:i], "\n"))
			} else {
				preambleEnd = i
			}
			start = i
		}
	}
	if start >= 0 {
		sections = append(sections, strings.Join(lines[start:], "\n"))
	}
	var preamble string
	if preambleEnd > 0 {
		preamble = strings.Join(lines[:preambleEnd], "\n")
	} else if start < 0 {
		// No ## sections at all, entire content is preamble.
		preamble = content
	}
	return preamble, sections
}

// parsePreambleMeta extracts "* key: value" lines from the preamble.
func parsePreambleMeta(preamble string) map[string]string {
	meta := make(map[string]string)
	for _, line := range strings.Split(preamble, "\n") {
		if key, val, ok := parseMetaLine(line); ok {
			meta[key] = val
		}
	}
	return meta
}

// parseTask parses a single task section into a Task.
func parseTask(section string) (Task, bool) {
	lines := strings.Split(section, "\n")
	if len(lines) == 0 || !strings.HasPrefix(lines[0], "## ") {
		return Task{}, false
	}
	heading := strings.TrimPrefix(lines[0], "## ")
	id, title := splitHeading(heading)
	if id < 0 {
		return Task{}, false
	}
	meta := make(map[string]string)
	bodyStart := 1
	// Skip blank line after heading.
	if bodyStart < len(lines) && strings.TrimSpace(lines[bodyStart]) == "" {
		bodyStart++
	}
	// Parse "* key: value" metadata lines, allowing blank lines between groups.
	for bodyStart < len(lines) {
		line := lines[bodyStart]
		if key, val, ok := parseMetaLine(line); ok {
			meta[key] = val
			bodyStart++
		} else if strings.TrimSpace(line) == "" && bodyStart+1 < len(lines) {
			if _, _, ok := parseMetaLine(lines[bodyStart+1]); ok {
				bodyStart++
				continue
			}
			break
		} else {
			break
		}
	}
	// Skip blank line between metadata and body.
	if bodyStart < len(lines) && strings.TrimSpace(lines[bodyStart]) == "" {
		bodyStart++
	}
	// Remaining lines form the description. Trim trailing whitespace.
	// Lower headings back to natural levels for in-memory representation.
	description := LowerHeadings(strings.TrimRight(strings.Join(lines[bodyStart:], "\n"), "\n \t"))
	t := Task{
		ID:          id,
		Title:       title,
		Meta:        meta,
		Description: description,
	}
	if v, ok := meta["status"]; ok {
		t.Status = v
	}
	if v, ok := meta["priority"]; ok {
		t.Priority = v
	}
	if v, ok := meta["type"]; ok {
		t.Type = v
	}
	if v, ok := meta["depends-on"]; ok {
		t.DependsOn = parseIntSlice(v)
		meta["depends-on"] = formatIntSlice(t.DependsOn) // normalize
	}
	if v, ok := meta["close-reason"]; ok {
		t.CloseReason = v
	}
	if v, ok := meta["tags"]; ok {
		t.Tags = splitTags(v)
	}
	if v, ok := meta["deleted"]; ok {
		t.Deleted = v == "true"
		delete(meta, "deleted")
	}
	if v, ok := meta["status-reason"]; ok {
		t.StatusReason = v
		delete(meta, "status-reason")
	}
	if v, ok := meta["agent-id"]; ok {
		t.AgentID = v
		delete(meta, "agent-id")
	}
	if v, ok := meta["files-in-scope"]; ok {
		t.FilesInScope = splitTags(v) // reuse: a plain comma-separated list, same shape as tags
		delete(meta, "files-in-scope")
	}
	return t, true
}

// splitHeading splits "1. Fix the login bug" into id=1 and title="Fix the login bug".
// Also accepts "1 Fix the login bug" without the dot for backwards compatibility.
// Returns id=-1 if the heading does not start with a valid integer.
func splitHeading(s string) (id int, title string) {
	s = strings.TrimSpace(s)
	i := strings.IndexByte(s, ' ')
	var idStr string
	if i < 0 {
		idStr = s
	} else {
		idStr = s[:i]
		title = s[i+1:]
	}
	// Strip trailing dot so both "42." and "42" parse as ID 42.
	idStr = strings.TrimSuffix(idStr, ".")
	n, err := strconv.Atoi(idStr)
	if err != nil {
		return -1, ""
	}
	return n, title
}

// parseMetaLine parses a CommonMark list-marker line like "* key: value", "- key: value",
// or "+ key: value" and returns key, value, true. Returns false if not a meta line.
// Also accepts the marker form with no value (empty string).
func parseMetaLine(line string) (string, string, bool) {
	trimmed := strings.TrimSpace(line)
	var kv string
	switch {
	case strings.HasPrefix(trimmed, "* "):
		kv = trimmed[2:]
	case strings.HasPrefix(trimmed, "- "):
		kv = trimmed[2:]
	case strings.HasPrefix(trimmed, "+ "):
		kv = trimmed[2:]
	default:
		return "", "", false
	}
	i := strings.Index(kv, ": ")
	if i >= 0 {
		return kv[:i], kv[i+2:], true
	}
	if strings.HasSuffix(kv, ":") {
		return kv[:len(kv)-1], "", true
	}
	return "", "", false
}

// --- Formatting ---

var (
	projectMetaOrder = []string{"created", "updated", "max-id"}
	taskMetaOrder    = []string{"status", "priority", "type", "depends-on", "close-reason", "status-reason", "tags", "agent-id", "files-in-scope", "created", "updated", "deleted"}
)

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
		t.Description = RaiseHeadings(t.Description, 3)
		sb.WriteString(FormatTask(t))
	}
	return sb.String()
}

// FormatTask formats a single Task as a markdown section.
func FormatTask(t Task) string {
	var sb strings.Builder
	if t.Title != "" {
		fmt.Fprintf(&sb, "## %d. %s\n", t.ID, t.Title)
	} else {
		fmt.Fprintf(&sb, "## %d.\n", t.ID)
	}
	// Build meta map including struct-only fields for formatting. AgentID/
	// FilesInScope are set only by GitStore.Claim (git mode) - they are
	// never synced into t.Meta the way Set* methods sync e.g. status, so
	// they are synthesized here exactly like Deleted/StatusReason, letting
	// a git-mode task round-trip through `md convert --from-git`.
	meta := t.Meta
	if t.Deleted || t.StatusReason != "" || t.AgentID != "" || len(t.FilesInScope) > 0 {
		meta = make(map[string]string, len(t.Meta)+4)
		for k, v := range t.Meta {
			meta[k] = v
		}
		if t.Deleted {
			meta["deleted"] = "true"
		}
		if t.StatusReason != "" {
			meta["status-reason"] = t.StatusReason
		}
		if t.AgentID != "" {
			meta["agent-id"] = t.AgentID
		}
		if len(t.FilesInScope) > 0 {
			meta["files-in-scope"] = strings.Join(t.FilesInScope, ",")
		}
	}
	if metaBlock := formatMetaBlock(meta, taskMetaOrder); metaBlock != "" {
		sb.WriteString("\n")
		sb.WriteString(metaBlock)
	}
	if t.Description != "" {
		sb.WriteString("\n")
		sb.WriteString(t.Description)
		sb.WriteString("\n")
	}
	return sb.String()
}

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
		fmt.Fprintf(&sb, "* %s: %s\n", key, flattenMetaValue(val))
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
		fmt.Fprintf(&sb, "* %s: %s\n", k, flattenMetaValue(meta[k]))
	}
	return sb.String()
}

// flattenMetaValue replaces newlines with spaces so meta values stay on one line.
func flattenMetaValue(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
