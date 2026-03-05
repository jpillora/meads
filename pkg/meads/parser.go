package meads

import (
	"strconv"
	"strings"
)

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

// parseMetaLine parses "* key: value" and returns key, value, true. Returns false if not a meta line.
// Also accepts "* key:" with no value (empty string).
func parseMetaLine(line string) (string, string, bool) {
	trimmed := strings.TrimSpace(line)
	if !strings.HasPrefix(trimmed, "* ") {
		return "", "", false
	}
	kv := strings.TrimPrefix(trimmed, "* ")
	i := strings.Index(kv, ": ")
	if i >= 0 {
		return kv[:i], kv[i+2:], true
	}
	if strings.HasSuffix(kv, ":") {
		return kv[:len(kv)-1], "", true
	}
	return "", "", false
}
