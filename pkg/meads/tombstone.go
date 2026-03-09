package meads

import (
	"fmt"
	"os"

	"github.com/go-git/go-billy/v5/util"
)

// nextID computes the next task ID from the maximum existing task ID.
func nextID(f *File) int {
	next := 1
	for _, t := range f.Tasks {
		if t.ID >= next {
			next = t.ID + 1
		}
	}
	return next
}

// validateDeps checks that all DependsOn IDs reference existing non-deleted tasks.
func validateDeps(f *File) error {
	ids := make(map[int]bool, len(f.Tasks))
	for _, t := range f.Tasks {
		if t.Status != "deleted" {
			ids[t.ID] = true
		}
	}
	for _, t := range f.Tasks {
		if t.Status == "deleted" {
			continue
		}
		for _, dep := range t.DependsOn {
			if !ids[dep] {
				return fmt.Errorf("task %d depends on non-existent task %d", t.ID, dep)
			}
		}
	}
	return nil
}

// pruneTombstones keeps at most one tombstone: the highest-ID deleted task,
// and only when no active task has a higher ID. This prevents ID reuse.
func pruneTombstones(f *File) {
	maxActive := 0
	maxDeleted := 0
	for _, t := range f.Tasks {
		if t.Status == "deleted" {
			if t.ID > maxDeleted {
				maxDeleted = t.ID
			}
		} else {
			if t.ID > maxActive {
				maxActive = t.ID
			}
		}
	}
	filtered := make([]Task, 0, len(f.Tasks))
	for _, t := range f.Tasks {
		if t.Status == "deleted" {
			// Keep only if it's the highest-ID deleted AND no active task is higher.
			if t.ID == maxDeleted && maxDeleted > maxActive {
				filtered = append(filtered, t)
			}
			continue
		}
		filtered = append(filtered, t)
	}
	f.Tasks = filtered
}

// filterDeleted returns only non-deleted tasks.
func filterDeleted(tasks []Task) []Task {
	out := make([]Task, 0, len(tasks))
	for _, t := range tasks {
		if t.Status != "deleted" {
			out = append(out, t)
		}
	}
	return out
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

// ensureFile creates the task file if it doesn't exist.
func (s *Store) ensureFile() error {
	if _, err := s.fs.Stat(s.file); os.IsNotExist(err) {
		return util.WriteFile(s.fs, s.file, []byte(s.fmt.EmptyFile()), 0644)
	}
	return nil
}
