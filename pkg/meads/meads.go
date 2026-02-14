package meads

import (
	"fmt"
	"os"
	"sort"
	"strconv"
	"time"
)

// Add creates a new task, assigning it the next available ID.
// The provided task must have ID == 0.
func Add(file string, t Task) (int, error) {
	if t.ID != 0 {
		return 0, fmt.Errorf("task ID must not be set (got %d)", t.ID)
	}
	if err := ensureFile(file); err != nil {
		return 0, err
	}
	_, content, err := acquireLock(file)
	if err != nil {
		return 0, err
	}
	f := ParseFile(content)
	now := time.Now().UTC().Format(time.RFC3339)
	// Assign next ID.
	t.ID = nextID(&f)
	// Set task created timestamp.
	t.ensureMeta()
	t.Meta["created"] = now
	f.Tasks = append(f.Tasks, t)
	// Update project meta.
	ensureProjectMeta(&f, now)
	f.Meta["updated"] = now
	f.Meta["next-id"] = strconv.Itoa(t.ID + 1)
	if err := releaseLock(file, FormatFile(f)); err != nil {
		return 0, fmt.Errorf("writing %s: %w", file, err)
	}
	return t.ID, nil
}

// AddMany creates multiple tasks in a single lock acquisition.
// Each task must have ID == 0. Returns the assigned IDs.
func AddMany(file string, tasks []Task) ([]int, error) {
	if len(tasks) == 0 {
		return nil, nil
	}
	for i, t := range tasks {
		if t.ID != 0 {
			return nil, fmt.Errorf("task %d: ID must not be set (got %d)", i, t.ID)
		}
	}
	if err := ensureFile(file); err != nil {
		return nil, err
	}
	_, content, err := acquireLock(file)
	if err != nil {
		return nil, err
	}
	f := ParseFile(content)
	now := time.Now().UTC().Format(time.RFC3339)
	ids := make([]int, len(tasks))
	for i := range tasks {
		tasks[i].ID = nextID(&f)
		tasks[i].ensureMeta()
		// Only set created if not already provided (e.g. from import).
		if tasks[i].Meta["created"] == "" {
			tasks[i].Meta["created"] = now
		}
		f.Tasks = append(f.Tasks, tasks[i])
		ids[i] = tasks[i].ID
	}
	ensureProjectMeta(&f, now)
	f.Meta["updated"] = now
	f.Meta["next-id"] = strconv.Itoa(tasks[len(tasks)-1].ID + 1)
	if err := releaseLock(file, FormatFile(f)); err != nil {
		return nil, fmt.Errorf("writing %s: %w", file, err)
	}
	return ids, nil
}

// Delete removes a task by ID.
func Delete(file string, id int) error {
	_, content, err := acquireLock(file)
	if err != nil {
		return err
	}
	f := ParseFile(content)
	filtered := make([]Task, 0, len(f.Tasks))
	found := false
	for _, t := range f.Tasks {
		if t.ID == id {
			found = true
			continue
		}
		filtered = append(filtered, t)
	}
	if !found {
		releaseLock(file, content)
		return fmt.Errorf("task %d not found", id)
	}
	f.Tasks = filtered
	now := time.Now().UTC().Format(time.RFC3339)
	ensureProjectMeta(&f, now)
	f.Meta["updated"] = now
	if err := releaseLock(file, FormatFile(f)); err != nil {
		return fmt.Errorf("writing %s: %w", file, err)
	}
	return nil
}

// Update modifies a task by ID. The provided function receives a pointer
// to the task for mutation.
func Update(file string, id int, fn func(*Task)) error {
	_, content, err := acquireLock(file)
	if err != nil {
		return err
	}
	f := ParseFile(content)
	found := false
	now := time.Now().UTC().Format(time.RFC3339)
	for i := range f.Tasks {
		if f.Tasks[i].ID == id {
			fn(&f.Tasks[i])
			f.Tasks[i].ensureMeta()
			f.Tasks[i].Meta["updated"] = now
			found = true
			break
		}
	}
	if !found {
		releaseLock(file, content)
		return fmt.Errorf("task %d not found", id)
	}
	ensureProjectMeta(&f, now)
	f.Meta["updated"] = now
	if err := releaseLock(file, FormatFile(f)); err != nil {
		return fmt.Errorf("writing %s: %w", file, err)
	}
	return nil
}

// Get returns tasks from the file. If ids is non-empty only the matching
// tasks are returned (in the order given). An error is returned for any
// id that does not exist. If ids is empty all tasks are returned.
func Get(file string, ids []int) ([]Task, error) {
	data, err := os.ReadFile(file)
	if err != nil {
		if os.IsNotExist(err) {
			if len(ids) > 0 {
				return nil, fmt.Errorf("task %d not found", ids[0])
			}
			return nil, nil
		}
		return nil, fmt.Errorf("reading %s: %w", file, err)
	}
	content := stripLockLines(string(data))
	f := ParseFile(content)
	if len(ids) == 0 {
		return f.Tasks, nil
	}
	byID := make(map[int]Task, len(f.Tasks))
	for _, t := range f.Tasks {
		byID[t.ID] = t
	}
	out := make([]Task, 0, len(ids))
	for _, id := range ids {
		t, ok := byID[id]
		if !ok {
			return nil, fmt.Errorf("task %d not found", id)
		}
		out = append(out, t)
	}
	return out, nil
}

// Ready returns open tasks not blocked by unclosed dependencies, sorted by priority descending.
func Ready(file string) ([]Task, error) {
	data, err := os.ReadFile(file)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading %s: %w", file, err)
	}
	content := stripLockLines(string(data))
	f := ParseFile(content)
	statusByID := make(map[int]string, len(f.Tasks))
	for _, t := range f.Tasks {
		statusByID[t.ID] = t.Status
	}
	var ready []Task
	for _, t := range f.Tasks {
		if t.Status != "open" {
			continue
		}
		if t.DependsOn > 0 {
			depStatus, exists := statusByID[t.DependsOn]
			if exists && depStatus != "closed" {
				continue
			}
		}
		ready = append(ready, t)
	}
	sort.Slice(ready, func(i, j int) bool {
		pi, pj := ready[i].Priority, ready[j].Priority
		if pi == "" {
			pi = "P2"
		}
		if pj == "" {
			pj = "P2"
		}
		return pi < pj
	})
	return ready, nil
}

// nextID returns the next task ID from project metadata, or computes it from tasks.
func nextID(f *File) int {
	next := 1
	if v, ok := f.Meta["next-id"]; ok {
		if n, err := strconv.Atoi(v); err == nil && n > next {
			next = n
		}
	}
	// Ensure next-id is higher than any existing task ID.
	for _, t := range f.Tasks {
		if t.ID >= next {
			next = t.ID + 1
		}
	}
	return next
}

// ensureProjectMeta ensures the project meta map is initialized and has a created timestamp.
func ensureProjectMeta(f *File, now string) {
	if f.Meta == nil {
		f.Meta = make(map[string]string)
	}
	if _, ok := f.Meta["created"]; !ok {
		f.Meta["created"] = now
	}
}

func ensureFile(file string) error {
	if _, err := os.Stat(file); os.IsNotExist(err) {
		return os.WriteFile(file, []byte(""), 0644)
	}
	return nil
}
