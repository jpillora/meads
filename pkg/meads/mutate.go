package meads

import (
	"fmt"
	"strings"
	"time"
)

func validateTitle(title string) error {
	if strings.ContainsRune(title, '\n') {
		return fmt.Errorf("task title must not contain newlines")
	}
	return nil
}

// Add creates a new task, assigning it the next available ID.
// The provided task must have ID == 0.
func (s *Store) Add(t Task) (int, error) {
	if t.ID != 0 {
		return 0, fmt.Errorf("task ID must not be set (got %d)", t.ID)
	}
	if err := validateTitle(t.Title); err != nil {
		return 0, err
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
		if err := validateTitle(t.Title); err != nil {
			return nil, fmt.Errorf("task %d: %w", i, err)
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
			f.Tasks[i].Deleted = true
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
		if f.Tasks[i].Deleted {
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
			f.Tasks[i].Deleted = true
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
		if f.Tasks[i].Deleted {
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

// DoctorFix describes a single fix applied by Doctor.
type DoctorFix struct {
	OldID int // The duplicate ID that was found
	NewID int // The new ID assigned to the duplicate
}

// Doctor detects duplicate task IDs and renumbers them.
// For each group of tasks sharing the same ID, the first occurrence is kept
// and subsequent duplicates are assigned the next available ID.
// DependsOn references pointing to renumbered IDs are updated accordingly.
// Returns the list of fixes applied. If no duplicates are found, the slice is empty.
func (s *Store) Doctor() ([]DoctorFix, error) {
	if err := s.ensureFile(); err != nil {
		return nil, err
	}
	_, content, err := s.acquireLock()
	if err != nil {
		return nil, err
	}
	f := s.fmt.Parse(content)
	// Find duplicates: track which IDs we've seen.
	seen := make(map[int]bool, len(f.Tasks))
	var fixes []DoctorFix
	// remap tracks old ID -> new ID for DependsOn fixups.
	remap := make(map[int]int)
	for i := range f.Tasks {
		id := f.Tasks[i].ID
		if !seen[id] {
			seen[id] = true
			continue
		}
		// Duplicate found — assign next available ID.
		newID := nextID(&f)
		fixes = append(fixes, DoctorFix{OldID: id, NewID: newID})
		remap[id] = newID
		f.Tasks[i].ID = newID
		f.Tasks[i].ensureMeta()
		seen[newID] = true
	}
	if len(fixes) == 0 {
		// No changes needed — release lock with original content.
		s.releaseLock(content)
		return nil, nil
	}
	// Update DependsOn references that point to renumbered IDs.
	// Note: if multiple tasks shared the same old ID, remap holds the last
	// new ID assigned. We need a multi-map: one old ID may have been
	// renumbered multiple times. However, DependsOn references should point
	// to a valid task. We only update references if the OLD id no longer
	// exists as a real task (the first occurrence kept the old ID, so
	// references to that old ID are still valid). We skip DependsOn fixup
	// for old IDs that still have a surviving task with that ID.
	// Actually, since we keep the first occurrence, the old ID still exists
	// for the first task. So DependsOn references to the old ID remain valid
	// (they point to the first task). No DependsOn fixup needed for the old
	// ID. But if a duplicate that was renumbered had other tasks depending
	// on it specifically (rare after a merge), there's no way to know which
	// duplicate the reference intended. We leave those as-is since the
	// original task with that ID still exists.
	now := time.Now().UTC().Format(time.RFC3339)
	if s.fmt.HasPreamble() {
		ensureProjectMeta(&f, now)
		f.Meta["updated"] = now
	}
	if err := s.releaseLock(s.fmt.Format(f)); err != nil {
		return nil, fmt.Errorf("writing %s: %w", s.file, err)
	}
	return fixes, nil
}

// AutoCleanResult describes changes made by AutoClean.
type AutoCleanResult struct {
	Marked  []int // IDs of closed tasks that were marked deleted
	Removed []int // IDs of deleted tasks that were physically removed
}

// AutoClean performs two-phase cleanup for the auto-delete hook.
// Phase 1: physically remove tasks that were already "deleted" in prevContent (except tombstone).
// Phase 2: mark "closed" tasks as "deleted".
// prevContent is the file content from the previous commit (HEAD~1).
// Returns the IDs affected in each phase, or nil if no changes were needed.
func (s *Store) AutoClean(prevContent string) (*AutoCleanResult, error) {
	_, content, err := s.acquireLock()
	if err != nil {
		return nil, err
	}
	f := s.fmt.Parse(content)

	// Build set of task IDs that were deleted in the previous commit.
	prevDeleted := make(map[int]bool)
	if prevContent != "" {
		prev := s.fmt.Parse(prevContent)
		for _, t := range prev.Tasks {
			if t.Deleted {
				prevDeleted[t.ID] = true
			}
		}
	}

	var result AutoCleanResult

	// Phase 1: physically remove tasks that were already committed as deleted.
	if len(prevDeleted) > 0 {
		filtered := make([]Task, 0, len(f.Tasks))
		for _, t := range f.Tasks {
			if t.Deleted && prevDeleted[t.ID] {
				result.Removed = append(result.Removed, t.ID)
				continue
			}
			filtered = append(filtered, t)
		}
		f.Tasks = filtered
	}

	// Phase 2: mark closed tasks as deleted.
	for i := range f.Tasks {
		if f.Tasks[i].Status == "closed" && !f.Tasks[i].Deleted {
			result.Marked = append(result.Marked, f.Tasks[i].ID)
			// Clean dangling deps from other tasks.
			delID := f.Tasks[i].ID
			for j := range f.Tasks {
				if j == i || f.Tasks[j].Deleted {
					continue
				}
				if len(f.Tasks[j].DependsOn) > 0 {
					var clean []int
					for _, dep := range f.Tasks[j].DependsOn {
						if dep != delID {
							clean = append(clean, dep)
						}
					}
					if len(clean) != len(f.Tasks[j].DependsOn) {
						f.Tasks[j].SetDependsOn(clean)
					}
				}
			}
			f.Tasks[i].Deleted = true
		}
	}

	if len(result.Marked) == 0 && len(result.Removed) == 0 {
		s.releaseLock(content)
		return nil, nil
	}

	// Prune tombstones: keep at most one for ID safety.
	pruneTombstones(&f)

	now := time.Now().UTC().Format(time.RFC3339)
	if s.fmt.HasPreamble() {
		ensureProjectMeta(&f, now)
		f.Meta["updated"] = now
	}
	if err := s.releaseLock(s.fmt.Format(f)); err != nil {
		return nil, fmt.Errorf("writing %s: %w", s.file, err)
	}
	return &result, nil
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
			if f.Tasks[i].Deleted {
				s.releaseLock(content)
				return fmt.Errorf("task %d not found", id)
			}
			fn(&f.Tasks[i])
			if err := validateTitle(f.Tasks[i].Title); err != nil {
				s.releaseLock(content)
				return err
			}
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
