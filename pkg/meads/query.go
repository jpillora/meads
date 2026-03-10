package meads

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/go-git/go-billy/v5/util"
)

// Get returns tasks from the file. If ids is non-empty only the matching
// tasks are returned (in the order given). An error is returned for any
// id that does not exist. If ids is empty all tasks are returned.
// Deleted (tombstone) tasks are always excluded.
func (s *Store) Get(ids []int) ([]Task, error) {
	data, err := util.ReadFile(s.fs, s.file)
	if err != nil {
		if os.IsNotExist(err) {
			if len(ids) > 0 {
				return nil, fmt.Errorf("task %d not found", ids[0])
			}
			return nil, nil
		}
		return nil, fmt.Errorf("reading %s: %w", s.file, err)
	}
	content := stripLockLines(string(data))
	f := s.fmt.Parse(content)
	// Filter out deleted tasks.
	active := filterDeleted(f.Tasks)
	if len(ids) == 0 {
		return active, nil
	}
	byID := make(map[int]Task, len(active))
	for _, t := range active {
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
// Deleted tasks are excluded.
func (s *Store) Ready() ([]Task, error) {
	data, err := util.ReadFile(s.fs, s.file)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading %s: %w", s.file, err)
	}
	content := stripLockLines(string(data))
	f := s.fmt.Parse(content)
	active := filterDeleted(f.Tasks)
	statusByID := make(map[int]string, len(active))
	for _, t := range active {
		statusByID[t.ID] = t.Status
	}
	var ready []Task
	for _, t := range active {
		if t.Status != "open" {
			continue
		}
		blocked := false
		for _, dep := range t.DependsOn {
			depStatus, exists := statusByID[dep]
			if exists && depStatus != "closed" {
				blocked = true
				break
			}
		}
		if blocked {
			continue
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

// GetHistory returns all tasks that have ever existed across git history,
// using the most recent version of each task. Tasks are sorted by ID ascending.
// Deleted tasks are excluded.
func (s *Store) GetHistory(git Git) ([]Task, error) {
	// Get all commits that touched the tasks file.
	out, err := git.Output("log", "--all", "--format=%H", "--", s.file)
	if err != nil {
		return nil, fmt.Errorf("git log: %w", err)
	}
	if out == "" {
		return nil, nil
	}
	commits := strings.Split(out, "\n")
	// Iterate from most recent to oldest; keep first seen version of each task.
	byID := make(map[int]Task)
	for _, hash := range commits {
		content, err := git.Output("show", hash+":"+s.file)
		if err != nil {
			continue // file may not exist in this commit
		}
		// Try primary parser first; fallback to the other for mid-history format migrations.
		f := s.fmt.Parse(content)
		if len(f.Tasks) == 0 {
			if _, ok := s.fmt.(csvFormat); ok {
				f = ParseFile(content)
			} else {
				f = ParseCSV(content)
			}
		}
		for _, t := range f.Tasks {
			if _, exists := byID[t.ID]; !exists {
				byID[t.ID] = t
			}
		}
	}
	tasks := make([]Task, 0, len(byID))
	for _, t := range byID {
		if !t.Deleted {
			tasks = append(tasks, t)
		}
	}
	sort.Slice(tasks, func(i, j int) bool {
		return tasks[i].ID < tasks[j].ID
	})
	return tasks, nil
}
