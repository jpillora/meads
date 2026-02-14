package meads

import (
	"fmt"
	"os"
	"sort"
	"strconv"
)

// Add creates a new task, assigning it the next available ID.
// The provided task must not have an ID set.
func Add(file string, t Task) (string, error) {
	if t.ID != "" {
		return "", fmt.Errorf("task ID must not be set (got %q)", t.ID)
	}
	if err := ensureFile(file); err != nil {
		return "", err
	}
	_, content, err := acquireLock(file)
	if err != nil {
		return "", err
	}
	tasks := ParseTasks(content)
	t.ID = nextTaskID(tasks)
	tasks = append(tasks, t)
	if err := releaseLock(file, FormatTasks(tasks)); err != nil {
		return "", fmt.Errorf("writing %s: %w", file, err)
	}
	return t.ID, nil
}

// Delete removes a task by ID.
func Delete(file, id string) error {
	_, content, err := acquireLock(file)
	if err != nil {
		return err
	}
	tasks := ParseTasks(content)
	filtered := make([]Task, 0, len(tasks))
	found := false
	for _, t := range tasks {
		if t.ID == id {
			found = true
			continue
		}
		filtered = append(filtered, t)
	}
	if !found {
		releaseLock(file, content)
		return fmt.Errorf("task %s not found", id)
	}
	if err := releaseLock(file, FormatTasks(filtered)); err != nil {
		return fmt.Errorf("writing %s: %w", file, err)
	}
	return nil
}

// Update modifies a task by ID. The provided function receives a pointer
// to the task for mutation.
func Update(file, id string, fn func(*Task)) error {
	_, content, err := acquireLock(file)
	if err != nil {
		return err
	}
	tasks := ParseTasks(content)
	found := false
	for i := range tasks {
		if tasks[i].ID == id {
			fn(&tasks[i])
			found = true
			break
		}
	}
	if !found {
		releaseLock(file, content)
		return fmt.Errorf("task %s not found", id)
	}
	if err := releaseLock(file, FormatTasks(tasks)); err != nil {
		return fmt.Errorf("writing %s: %w", file, err)
	}
	return nil
}

// Get returns tasks from the file. If ids is non-empty only the matching
// tasks are returned (in the order given). An error is returned for any
// id that does not exist. If ids is empty all tasks are returned.
func Get(file string, ids []string) ([]Task, error) {
	data, err := os.ReadFile(file)
	if err != nil {
		if os.IsNotExist(err) {
			if len(ids) > 0 {
				return nil, fmt.Errorf("task %s not found", ids[0])
			}
			return nil, nil
		}
		return nil, fmt.Errorf("reading %s: %w", file, err)
	}
	content := stripLockLines(string(data))
	tasks := ParseTasks(content)
	if len(ids) == 0 {
		return tasks, nil
	}
	byID := make(map[string]Task, len(tasks))
	for _, t := range tasks {
		byID[t.ID] = t
	}
	out := make([]Task, 0, len(ids))
	for _, id := range ids {
		t, ok := byID[id]
		if !ok {
			return nil, fmt.Errorf("task %s not found", id)
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
	tasks := ParseTasks(content)
	statusByID := make(map[string]string, len(tasks))
	for _, t := range tasks {
		statusByID[t.ID] = t.Status
	}
	var ready []Task
	for _, t := range tasks {
		if t.Status != "open" {
			continue
		}
		if t.DependsOn != "" {
			depStatus, exists := statusByID[t.DependsOn]
			if exists && depStatus != "closed" {
				continue
			}
		}
		ready = append(ready, t)
	}
	sort.Slice(ready, func(i, j int) bool {
		return ready[i].Priority > ready[j].Priority
	})
	return ready, nil
}

func nextTaskID(tasks []Task) string {
	max := 0
	for _, t := range tasks {
		if n, err := strconv.Atoi(t.ID); err == nil && n > max {
			max = n
		}
	}
	return fmt.Sprintf("%04d", max+1)
}

func ensureFile(file string) error {
	if _, err := os.Stat(file); os.IsNotExist(err) {
		return os.WriteFile(file, []byte(""), 0644)
	}
	return nil
}
