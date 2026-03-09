package meads

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/go-git/go-billy/v5/util"
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
	f := s.parseFn(content)
	now := time.Now().UTC().Format(time.RFC3339)
	// Assign next ID.
	t.ID = nextID(&f)
	// Set task created timestamp.
	t.ensureMeta()
	t.Meta["created"] = now
	f.Tasks = append(f.Tasks, t)
	pruneTombstones(&f)
	// Update project meta.
	if !s.csvMode {
		ensureProjectMeta(&f, now)
		f.Meta["updated"] = now
	}
	if err := s.releaseLock(s.formatFn(f)); err != nil {
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
	f := s.parseFn(content)
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
	if !s.csvMode {
		ensureProjectMeta(&f, now)
		f.Meta["updated"] = now
	}
	if err := s.releaseLock(s.formatFn(f)); err != nil {
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
	f := s.parseFn(content)
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
	if !s.csvMode {
		ensureProjectMeta(&f, now)
		f.Meta["updated"] = now
	}
	if err := s.releaseLock(s.formatFn(f)); err != nil {
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
	f := s.parseFn(content)
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
	if !s.csvMode {
		ensureProjectMeta(&f, now)
		f.Meta["updated"] = now
	}
	if err := s.releaseLock(s.formatFn(f)); err != nil {
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
	f := s.parseFn(content)
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
	if !s.csvMode {
		ensureProjectMeta(&f, now)
		f.Meta["updated"] = now
	}
	if err := s.releaseLock(s.formatFn(f)); err != nil {
		return fmt.Errorf("writing %s: %w", s.file, err)
	}
	return nil
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
	f := s.parseFn(content)
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
	f := s.parseFn(content)
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
		f := s.parseFn(content)
		if len(f.Tasks) == 0 {
			if s.csvMode {
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
		if t.Status != "deleted" {
			tasks = append(tasks, t)
		}
	}
	sort.Slice(tasks, func(i, j int) bool {
		return tasks[i].ID < tasks[j].ID
	})
	return tasks, nil
}

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

// ensureProjectMeta ensures the project meta map is initialized and has a created timestamp.
func ensureProjectMeta(f *File, now string) {
	if f.Meta == nil {
		f.Meta = make(map[string]string)
	}
	if _, ok := f.Meta["created"]; !ok {
		f.Meta["created"] = now
	}
}

func (s *Store) ensureFile() error {
	if _, err := s.fs.Stat(s.file); os.IsNotExist(err) {
		initial := ""
		if s.csvMode {
			initial = csvHeaderRow()
		}
		return util.WriteFile(s.fs, s.file, []byte(initial), 0644)
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

