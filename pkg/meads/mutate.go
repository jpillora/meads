package meads

import (
	"fmt"
	"strings"
	"time"

	"github.com/go-git/go-billy/v5/util"
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
	// Validate DependsOn references.
	if err := validateDeps(&f); err != nil {
		s.releaseLock(content)
		return 0, err
	}
	pruneTombstones(&f, s.fmt.HasPreamble())
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
	// Validate DependsOn references.
	if err := validateDeps(&f); err != nil {
		s.releaseLock(content)
		return nil, err
	}
	pruneTombstones(&f, s.fmt.HasPreamble())
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
	pruneTombstones(&f, s.fmt.HasPreamble())
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
	pruneTombstones(&f, s.fmt.HasPreamble())
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

// DoctorFixKind identifies which kind of repair a DoctorFix describes. The
// zero value is DoctorFixDuplicate - the file backend's only kind, so its
// fixes need no explicit Kind.
type DoctorFixKind int

const (
	// DoctorFixDuplicate: an id held by two unrelated tasks (the file
	// backend's in-file duplicate, or git mode's create/create collision
	// across clones); the duplicate was renumbered to NewID.
	DoctorFixDuplicate DoctorFixKind = iota
	// DoctorFixMismatch: git mode only - a task ref's stored content
	// disagreed with its own ref name; repaired in place (OldID == NewID,
	// not a renumber).
	DoctorFixMismatch
	// DoctorFixDiverged: git mode only - one task edited on both sides of a
	// partition; the local version was re-homed at NewID and the id itself
	// took the fetched-remote version (see GitStore.Doctor).
	DoctorFixDiverged
)

// DoctorFix describes a single fix applied by Doctor.
type DoctorFix struct {
	OldID int // The duplicate ID that was found
	NewID int // The new ID assigned to the duplicate
	// Kind classifies the fix (zero for the file backend's duplicates - see
	// DoctorFixKind).
	Kind DoctorFixKind
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
		// Update DependsOn references using the remap built so far.
		// After a merge, tasks from the same branch appear contiguously,
		// so the remap state at this point reflects the correct mappings
		// for sibling tasks from the same branch.
		if len(f.Tasks[i].DependsOn) > 0 {
			changed := false
			for j, dep := range f.Tasks[i].DependsOn {
				if newDep, ok := remap[dep]; ok {
					f.Tasks[i].DependsOn[j] = newDep
					changed = true
				}
			}
			if changed {
				f.Tasks[i].SetDependsOn(f.Tasks[i].DependsOn)
			}
		}
		f.Tasks[i].ensureMeta()
		seen[newID] = true
	}
	if len(fixes) == 0 {
		// No changes needed — release lock with original content.
		s.releaseLock(content)
		return nil, nil
	}
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
	Removed []int // IDs of closed tasks that were removed this run
}

// AutoClean marks "closed" tasks as deleted, then drops tombstone rows
// (persisting the high-water mark via pruneTombstones). Only tasks already
// captured in a commit are removed: a closed task that has never been committed
// is left untouched, since deleting it would lose work that git history cannot
// recover. Returns the IDs removed, or nil if there was nothing to do.
func (s *Store) AutoClean(git Git) (*AutoCleanResult, error) {
	committed := s.committedIDs(git)

	_, content, err := s.acquireLock()
	if err != nil {
		return nil, err
	}
	f := s.fmt.Parse(content)

	var result AutoCleanResult
	for i := range f.Tasks {
		if f.Tasks[i].Status == "closed" && !f.Tasks[i].Deleted && committed[f.Tasks[i].ID] {
			result.Removed = append(result.Removed, f.Tasks[i].ID)
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

	if len(result.Removed) == 0 {
		s.releaseLock(content)
		return nil, nil
	}

	pruneTombstones(&f, s.fmt.HasPreamble())

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

// ImportAll writes tasks verbatim into a brand-new tasks file, preserving
// every id exactly (including soft-deleted ones) rather than reassigning
// ids the way Add/AddMany do - used by `md convert`'s git->file migration,
// where refs/meads/tasks/* ids must carry over unchanged (see
// cmd/md/convert.go and GitStore.LoadAll, its source of tasks).
//
// The target file must not already exist: ids from the source are meant to
// be authoritative for a fresh file, so this refuses to merge into or
// overwrite existing data rather than guessing how to reconcile the two id
// spaces (mirrors `md init`'s plain file mode, and initCmd.runGit's refusal
// to initialize over existing git-mode refs).
//
// No pruning runs here (unlike Add/Update/Delete, which always call
// pruneTombstones): a soft-deleted task is written as an ordinary tombstone
// row/section, exactly like Get sees deleted rows a file happens to already
// contain. The very next mutation through the normal Store API prunes it
// down to the format's usual steady state (dropped for markdown, collapsed
// to the single highest tombstone for CSV) the same way it always does; see
// pruneTombstones. nextID also scans every row regardless of Deleted, so
// future ID allocation is already correct even before that first prune.
func (s *Store) ImportAll(tasks []Task) error {
	if _, err := s.fs.Stat(s.file); err == nil {
		return fmt.Errorf("%s already exists", s.file)
	}
	f := File{Tasks: tasks}
	if s.fmt.HasPreamble() {
		now := time.Now().UTC().Format(time.RFC3339)
		ensureProjectMeta(&f, now)
		f.Meta["updated"] = now
	}
	if err := util.WriteFile(s.fs, s.file, []byte(s.fmt.Format(f)), 0644); err != nil {
		return fmt.Errorf("writing %s: %w", s.file, err)
	}
	return nil
}
