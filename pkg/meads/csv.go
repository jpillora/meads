package meads

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"sort"
	"strconv"
	"strings"
)

// csvFormat implements Format for TASKS.csv files.
type csvFormat struct{}

func (csvFormat) Parse(content string) File { return ParseCSV(content) }
func (csvFormat) Format(f File) string      { return FormatCSV(f) }
func (csvFormat) HasPreamble() bool         { return false }
func (csvFormat) EmptyFile() string          { return csvHeaderRow() }

// CSV column order — header row is static.
var csvColumns = []string{
	"id", "title", "status", "priority", "type",
	"depends-on", "tags", "description", "close-reason",
	"status-reason", "created", "updated", "deleted", "meta",
}

// ParseCSV parses CSV content into a File. Deleted rows are included.
func ParseCSV(content string) File {
	f := File{
		Meta:  make(map[string]string),
		Tasks: make([]Task, 0),
	}
	content = strings.TrimSpace(content)
	if content == "" {
		return f
	}
	r := csv.NewReader(strings.NewReader(content))
	r.FieldsPerRecord = -1 // allow variable-length rows for backward compatibility
	records, err := r.ReadAll()
	if err != nil || len(records) < 1 {
		return f
	}
	// Build column index from header row.
	header := records[0]
	colIdx := make(map[string]int, len(header))
	for i, h := range header {
		colIdx[h] = i
	}
	get := func(row []string, col string) string {
		idx, ok := colIdx[col]
		if !ok || idx >= len(row) {
			return ""
		}
		return row[idx]
	}
	for _, row := range records[1:] {
		idStr := get(row, "id")
		id, err := strconv.Atoi(idStr)
		if err != nil || id <= 0 {
			continue
		}
		t := Task{
			ID:           id,
			Title:        get(row, "title"),
			Status:       get(row, "status"),
			Priority:     get(row, "priority"),
			Type:         get(row, "type"),
			CloseReason:  get(row, "close-reason"),
			StatusReason: get(row, "status-reason"),
			Deleted:      get(row, "deleted") == "true",
			Description:  unescapeNewlines(get(row, "description")),
		}
		if depsStr := get(row, "depends-on"); depsStr != "" {
			t.DependsOn = parseIntSlice(depsStr)
		}
		if tagsStr := get(row, "tags"); tagsStr != "" {
			t.Tags = splitTags(tagsStr)
		}
		// Parse meta JSON column.
		meta := make(map[string]string)
		if metaStr := get(row, "meta"); metaStr != "" && metaStr != "{}" {
			json.Unmarshal([]byte(metaStr), &meta)
		}
		// Sync known fields into meta for consistency.
		if t.Status != "" {
			meta["status"] = t.Status
		}
		if t.Priority != "" {
			meta["priority"] = t.Priority
		}
		if t.Type != "" {
			meta["type"] = t.Type
		}
		if len(t.DependsOn) > 0 {
			meta["depends-on"] = formatIntSlice(t.DependsOn)
		}
		if t.CloseReason != "" {
			meta["close-reason"] = t.CloseReason
		}
		if len(t.Tags) > 0 {
			meta["tags"] = strings.Join(t.Tags, ",")
		}
		if v := get(row, "created"); v != "" {
			meta["created"] = v
		}
		if v := get(row, "updated"); v != "" {
			meta["updated"] = v
		}
		t.Meta = meta
		f.Tasks = append(f.Tasks, t)
	}
	return f
}

// FormatCSV formats a File as CSV content. Rows are sorted by ID ascending.
func FormatCSV(f File) string {
	var buf bytes.Buffer
	w := csv.NewWriter(&buf)
	// Write header.
	w.Write(csvColumns)
	// Sort tasks by ID.
	tasks := make([]Task, len(f.Tasks))
	copy(tasks, f.Tasks)
	sort.Slice(tasks, func(i, j int) bool {
		return tasks[i].ID < tasks[j].ID
	})
	for _, t := range tasks {
		// Build meta JSON from non-known keys.
		extraMeta := make(map[string]string)
		for k, v := range t.Meta {
			if !knownMetaKeys[k] && k != "created" && k != "updated" && k != "deleted" && k != "status-reason" {
				extraMeta[k] = v
			}
		}
		metaJSON := "{}"
		if len(extraMeta) > 0 {
			b, _ := json.Marshal(extraMeta)
			metaJSON = string(b)
		}
		created := ""
		if t.Meta != nil {
			created = t.Meta["created"]
		}
		updated := ""
		if t.Meta != nil {
			updated = t.Meta["updated"]
		}
		deletedStr := ""
		if t.Deleted {
			deletedStr = "true"
		}
		row := []string{
			strconv.Itoa(t.ID),
			t.Title,
			t.Status,
			t.Priority,
			t.Type,
			formatIntSlice(t.DependsOn),
			strings.Join(t.Tags, ","),
			escapeNewlines(t.Description),
			t.CloseReason,
			t.StatusReason,
			created,
			updated,
			deletedStr,
			metaJSON,
		}
		w.Write(row)
	}
	w.Flush()
	return buf.String()
}

// escapeNewlines replaces actual newlines with literal \n for CSV storage.
func escapeNewlines(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, "\n", `\n`)
	return s
}

// unescapeNewlines replaces literal \n with actual newlines from CSV storage.
func unescapeNewlines(s string) string {
	var result strings.Builder
	i := 0
	for i < len(s) {
		if i+1 < len(s) && s[i] == '\\' {
			switch s[i+1] {
			case 'n':
				result.WriteByte('\n')
				i += 2
				continue
			case '\\':
				result.WriteByte('\\')
				i += 2
				continue
			}
		}
		result.WriteByte(s[i])
		i++
	}
	return result.String()
}

// csvHeaderRow returns the CSV header line for use in ensureFile.
func csvHeaderRow() string {
	var buf bytes.Buffer
	w := csv.NewWriter(&buf)
	w.Write(csvColumns)
	w.Flush()
	return buf.String()
}

// InitCSV creates an empty TASKS.csv file content (header only).
func InitCSV() string {
	return csvHeaderRow()
}
