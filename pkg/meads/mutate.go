package meads

import (
	"fmt"
	"time"
)

// Add creates a new task, assigning it the next available ID.
// The provided task must have ID == 0.
func (s *Store) Add(t Task) (int, error) {
	if t.ID != 0 {
		return 0, fmt.Errorf("task ID must not be set (got %d)", t.ID)
	}
	if err := s.ensureFile(); err != nil {
		return 0, err
	}
	_, content, err := s.acquireLock()
	if err != nil {
		return 0, err
	}
	f := s.fmt.Parse(content)
	now := time.Now().UTC().Format(time.RFC3339)
	// Assign next ID.
	t.ID = nextID(&f)
	// Set task created timestamp.
	t.ensureMeta()
	t.Meta["created"] = now
	f.Tasks = append(f.Tasks, t)
	pruneTombstones(&f)
	// Update project meta.
	if s.fmt.HasPreamble() {
		ensureProjectMeta(&f, now)
		f.Meta["updated"] = now
	}
	if err := s.releaseLock(s.fmt.Format(f)); err != nil {
		return 0, fmt.Errorf("writing %s: %w", s.file, err)
	}
	return t.ID, nil
}

// AddMany creates multiple tasks in a single lock acquisition.
// Each task must have ID == 0. Returns the assigned IDs.
func (s *Store) AddMany(tasks []Task) ([]int, error) {
	if len(tasks) == 0 {
		return nil, nil
	}
	for i, t := range tasks {
		if t.ID != 0 {
			return nil, fmt.Errorf("task %d: ID must not be set (got %d)", i, t.ID)
		}
	}
	if err := s.ensureFile(); err != nil {
		return nil, err
	}
	_, content, err := s.acquireLock()
	if err != nil {
		return nil, err
	}
	f := s.fmt.Parse(content)
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
	pruneTombstones(&f)
	if s.fmt.HasPreamble() {
		ensureProjectMeta(&f, now)
		f.Meta["updated"] = now
	}
	if err := s.releaseLock(s.fmt.Format(f)); err != nil {
		return nil, fmt.Errorf("writing %s: %w", s.file, err)
	}
	return ids, nil
}

// Delete soft-deletes a task by ID, replacing it with a tombstone.
func (s *Store) Delete(id int) error {
	_, content, err := s.acquireLock()
	if err != nil {
		return err
	}
	f := s.fmt.Parse(content)
	found := false
	for i := range f.Tasks {
		if f.Tasks[i].ID == id {
			f.Tasks[i] = Task{
				ID:     id,
				Title:  "deleted",
				Status: "deleted",
				Meta:   map[string]string{"status": "deleted"},
			}
			found = true
			break
		}
	}
	if !found {
		s.releaseLock(content)
		return fmt.Errorf("task %d not found", id)
	}
	// Clean dangling deps.
	for i := range f.Tasks {
		if f.Tasks[i].Status == "deleted" {
			continue
		}
		if len(f.Tasks[i].DependsOn) > 0 {
			var clean []int
			for _, dep := range f.Tasks[i].DependsOn {
				if dep != id {
					clean = append(clean, dep)
				}
			}
			if len(clean) != len(f.Tasks[i].DependsOn) {
				f.Tasks[i].SetDependsOn(clean)
			}
		}
	}
	pruneTombstones(&f)
	now := time.Now().UTC().Format(time.RFC3339)
	if s.fmt.HasPreamble() {
		ensureProjectMeta(&f, now)
		f.Meta["updated"] = now
	}
	if err := s.releaseLock(s.fmt.Format(f)); err != nil {
		return fmt.Errorf("writing %s: %w", s.file, err)
	}
	return nil
}

// DeleteMany soft-deletes multiple tasks by ID in a single atomic operation.
// It also removes deleted IDs from other tasks' DependsOn lists.
func (s *Store) DeleteMany(ids []int) error {
	if len(ids) == 0 {
		return nil
	}
	_, content, err := s.acquireLock()
	if err != nil {
		return err
	}
	f := s.fmt.Parse(content)
	deleteSet := make(map[int]bool, len(ids))
	for _, id := range ids {
		deleteSet[id] = true
	}
	// Mark tasks as deleted and count found.
	found := 0
	for i := range f.Tasks {
		if deleteSet[f.Tasks[i].ID] {
			found++
			f.Tasks[i] = Task{
				ID:     f.Tasks[i].ID,
				Title:  "deleted",
				Status: "deleted",
				Meta:   map[string]string{"status": "deleted"},
			}
		}
	}
	if found != len(ids) {
		s.releaseLock(content)
		// Find first missing ID for the error message.
		existing := make(map[int]bool, len(f.Tasks))
		for _, t := range f.Tasks {
			existing[t.ID] = true
		}
		for _, id := range ids {
			if !existing[id] {
				return fmt.Errorf("task %d not found", id)
			}
		}
	}
	// Clean up dangling deps on non-deleted tasks.
	for i := range f.Tasks {
		if f.Tasks[i].Status == "deleted" {
			continue
		}
		if len(f.Tasks[i].DependsOn) > 0 {
			var cleanDeps []int
			for _, dep := range f.Tasks[i].DependsOn {
				if !deleteSet[dep] {
					cleanDeps = append(cleanDeps, dep)
				}
			}
			if len(cleanDeps) != len(f.Tasks[i].DependsOn) {
				f.Tasks[i].SetDependsOn(cleanDeps)
			}
		}
	}
	pruneTombstones(&f)
	now := time.Now().UTC().Format(time.RFC3339)
	if s.fmt.HasPreamble() {
		ensureProjectMeta(&f, now)
		f.Meta["updated"] = now
	}
	if err := s.releaseLock(s.fmt.Format(f)); err != nil {
		return fmt.Errorf("writing %s: %w", s.file, err)
	}
	return nil
}

// Update modifies a task by ID. The provided function receives a pointer
// to the task for mutation. After mutation, any DependsOn IDs are validated
// to ensure the referenced tasks exist.
func (s *Store) Update(id int, fn func(*Task)) error {
	_, content, err := s.acquireLock()
	if err != nil {
		return err
	}
	f := s.fmt.Parse(content)
	found := false
	now := time.Now().UTC().Format(time.RFC3339)
	for i := range f.Tasks {
		if f.Tasks[i].ID == id {
			if f.Tasks[i].Status == "deleted" {
				s.releaseLock(content)
				return fmt.Errorf("task %d not found", id)
			}
			fn(&f.Tasks[i])
			f.Tasks[i].ensureMeta()
			f.Tasks[i].Meta["updated"] = now
			found = true
			break
		}
	}
	if !found {
		s.releaseLock(content)
		return fmt.Errorf("task %d not found", id)
	}
	// Validate DependsOn references.
	if err := validateDeps(&f); err != nil {
		s.releaseLock(content)
		return err
	}
	if s.fmt.HasPreamble() {
		ensureProjectMeta(&f, now)
		f.Meta["updated"] = now
	}
	if err := s.releaseLock(s.fmt.Format(f)); err != nil {
		return fmt.Errorf("writing %s: %w", s.file, err)
	}
	return nil
}
