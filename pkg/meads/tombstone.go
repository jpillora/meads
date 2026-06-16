package meads

import (
	"fmt"
	"os"
	"strconv"

	"github.com/go-git/go-billy/v5/util"
)

// nextID computes the next task ID from the maximum existing task ID and the
// optional "max-id" project-meta high-water mark (set when the most recently
// deleted task had a higher ID than any surviving active task).
func nextID(f *File) int {
	next := 1
	for _, t := range f.Tasks {
		if t.ID >= next {
			next = t.ID + 1
		}
	}
	if v, ok := f.Meta["max-id"]; ok {
		if n, err := strconv.Atoi(v); err == nil && n >= next {
			next = n + 1
		}
	}
	return next
}

// validateDeps checks that all DependsOn IDs reference existing non-deleted tasks
// and detects circular dependencies. It is the gate that prevents a mutation
// from creating (or leaving behind) a cycle.
func validateDeps(f *File) error {
	ids, adj := depGraph(f)
	for _, t := range f.Tasks {
		if t.Deleted {
			continue
		}
		for _, dep := range t.DependsOn {
			if !ids[dep] {
				return fmt.Errorf("task %d depends on non-existent task %d", t.ID, dep)
			}
		}
	}
	if cycle := findCycle(ids, adj); cycle != nil {
		return fmt.Errorf("circular dependency detected: %s", FormatCycle(cycle))
	}
	return nil
}

// pruneTombstones removes tombstone rows and persists the high-water mark
// needed to prevent ID reuse.
//
// When hasPreamble is true (e.g. markdown), tombstone rows are always dropped
// and f.Meta["max-id"] records the highest deleted ID — but only when it
// exceeds the highest active ID. Otherwise the meta key is cleared.
//
// When hasPreamble is false (e.g. CSV, which can't carry project meta), a
// single tombstone row for the highest deleted task is retained when it
// exceeds the highest active ID.
func pruneTombstones(f *File, hasPreamble bool) {
	maxActive := 0
	maxDeleted := 0
	for _, t := range f.Tasks {
		if t.Deleted {
			if t.ID > maxDeleted {
				maxDeleted = t.ID
			}
		} else {
			if t.ID > maxActive {
				maxActive = t.ID
			}
		}
	}
	if hasPreamble {
		filtered := make([]Task, 0, len(f.Tasks))
		for _, t := range f.Tasks {
			if !t.Deleted {
				filtered = append(filtered, t)
			}
		}
		f.Tasks = filtered
		// Combine the existing meta high-water mark with any new deletion so
		// that non-highest deletes (or non-delete mutations) don't regress it.
		existing := 0
		if v, ok := f.Meta["max-id"]; ok {
			existing, _ = strconv.Atoi(v)
		}
		newMax := maxDeleted
		if existing > newMax {
			newMax = existing
		}
		if newMax > maxActive {
			if f.Meta == nil {
				f.Meta = make(map[string]string)
			}
			f.Meta["max-id"] = strconv.Itoa(newMax)
		} else {
			delete(f.Meta, "max-id")
		}
		return
	}
	filtered := make([]Task, 0, len(f.Tasks))
	for _, t := range f.Tasks {
		if t.Deleted {
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
		if !t.Deleted {
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
