package meads

import (
	"strconv"
	"strings"
)

// ParseTasks parses the TASKS.md content and returns all tasks.
func ParseTasks(content string) []Task {
	sections := splitSections(content)
	tasks := make([]Task, 0, len(sections))
	for _, sec := range sections {
		if t, ok := parseTask(sec); ok {
			tasks = append(tasks, t)
		}
	}
	return tasks
}

// splitSections splits markdown content into sections, each starting with "## ".
func splitSections(content string) []string {
	lines := strings.Split(content, "\n")
	var sections []string
	start := -1
	for i, line := range lines {
		if strings.HasPrefix(line, "## ") {
			if start >= 0 {
				sections = append(sections, strings.Join(lines[start:i], "\n"))
			}
			start = i
		}
	}
	if start >= 0 {
		sections = append(sections, strings.Join(lines[start:], "\n"))
	}
	return sections
}

// parseTask parses a single task section into a Task.
func parseTask(section string) (Task, bool) {
	lines := strings.Split(section, "\n")
	if len(lines) == 0 || !strings.HasPrefix(lines[0], "## ") {
		return Task{}, false
	}
	heading := strings.TrimPrefix(lines[0], "## ")
	id, title := splitHeading(heading)
	if id == "" {
		return Task{}, false
	}
	meta := make(map[string]string)
	bodyStart := 1
	// Skip blank line after heading.
	if bodyStart < len(lines) && strings.TrimSpace(lines[bodyStart]) == "" {
		bodyStart++
	}
	// Parse "* key: value" metadata lines.
	for bodyStart < len(lines) {
		line := lines[bodyStart]
		if key, val, ok := parseMetaLine(line); ok {
			meta[key] = val
			bodyStart++
		} else {
			break
		}
	}
	// Skip blank line between metadata and body.
	if bodyStart < len(lines) && strings.TrimSpace(lines[bodyStart]) == "" {
		bodyStart++
	}
	// Remaining lines form the body. Trim trailing whitespace.
	body := strings.TrimRight(strings.Join(lines[bodyStart:], "\n"), "\n \t")
	t := Task{
		ID:    id,
		Title: title,
		Meta:  meta,
		Body:  body,
	}
	if v, ok := meta["status"]; ok {
		t.Status = v
	}
	if v, ok := meta["priority"]; ok {
		if n, err := strconv.Atoi(v); err == nil {
			t.Priority = n
		}
	}
	if v, ok := meta["depends-on"]; ok {
		t.DependsOn = v
	}
	return t, true
}

// splitHeading splits "0001 Fix the login bug" into id="0001" and title="Fix the login bug".
func splitHeading(s string) (id, title string) {
	s = strings.TrimSpace(s)
	i := strings.IndexByte(s, ' ')
	if i < 0 {
		return s, ""
	}
	return s[:i], s[i+1:]
}

// parseMetaLine parses "* key: value" and returns key, value, true. Returns false if not a meta line.
func parseMetaLine(line string) (string, string, bool) {
	trimmed := strings.TrimSpace(line)
	if !strings.HasPrefix(trimmed, "* ") {
		return "", "", false
	}
	kv := strings.TrimPrefix(trimmed, "* ")
	i := strings.Index(kv, ": ")
	if i < 0 {
		return "", "", false
	}
	return kv[:i], kv[i+2:], true
}
