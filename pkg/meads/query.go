package meads

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/go-git/go-billy/v5/util"
)

// selectByIDs returns the tasks in active matching ids, in the order given.
// Empty ids returns all of active. Missing id -> error "task %d not found".
func selectByIDs(active []Task, ids []int) ([]Task, error) {
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

// readyTasks returns open, unblocked tasks sorted by priority ascending
// (P0 first), treating empty priority as "P2".
func readyTasks(active []Task) []Task {
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
	return ready
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
	f := s.fmt.Parse(content)
	active := filterDeleted(f.Tasks)
	return selectByIDs(active, ids)
}

// GetAll returns every task currently in the file, ascending by id and with
// deleted (tombstone) rows included - unlike Get, which always filters them
// out. In practice a live file rarely holds a tombstone row at all
// (pruneTombstones drops markdown ones on every mutation, and keeps at most
// the single highest-id one for CSV - see tombstone.go), so this mainly
// exists for `md convert`'s file->git migration, where whatever
// soft-deleted rows a file DOES currently hold must be preserved rather than
// silently dropped (see cmd/md/convert.go and GitStore.ImportTask). A
// missing file yields no tasks, matching Get.
func (s *Store) GetAll() ([]Task, error) {
	data, err := util.ReadFile(s.fs, s.file)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading %s: %w", s.file, err)
	}
	content := stripLockLines(string(data))
	f := s.fmt.Parse(content)
	return f.Tasks, nil
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
	return readyTasks(active), nil
}

// FindCycles returns every circular dependency in the active (non-deleted)
// task graph, one representative cycle per cyclic cluster, each as a path
// start → … → start. It is read-only: unlike validateDeps (which blocks
// mutations), this surfaces cycles that are already present — e.g. introduced
// by a git merge of two individually-valid edits, or a hand-edit — so callers
// can warn about them. A missing file yields no cycles.
func (s *Store) FindCycles() ([][]int, error) {
	data, err := util.ReadFile(s.fs, s.file)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading %s: %w", s.file, err)
	}
	content := stripLockLines(string(data))
	f := s.fmt.Parse(content)
	ids, adj := depGraph(&f)
	return findCycles(ids, adj), nil
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
		for _, t := range s.parseHistorical(content).Tasks {
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

// parseHistorical parses file content from a historical commit, trying the
// store's primary format first and falling back to the other for mid-history
// format migrations (md↔csv).
func (s *Store) parseHistorical(content string) File {
	f := s.fmt.Parse(content)
	if len(f.Tasks) == 0 {
		if _, ok := s.fmt.(csvFormat); ok {
			f = ParseFile(content)
		} else {
			f = ParseCSV(content)
		}
	}
	return f
}

// activeByID reads the working file and returns its non-deleted tasks keyed by
// ID. A missing file yields an empty map (not an error), matching Get.
func (s *Store) activeByID() (map[int]Task, error) {
	data, err := util.ReadFile(s.fs, s.file)
	if err != nil {
		if os.IsNotExist(err) {
			return map[int]Task{}, nil
		}
		return nil, fmt.Errorf("reading %s: %w", s.file, err)
	}
	content := stripLockLines(string(data))
	f := s.fmt.Parse(content)
	active := make(map[int]Task, len(f.Tasks))
	for _, t := range filterDeleted(f.Tasks) {
		active[t.ID] = t
	}
	return active, nil
}

// recoverFromHistory walks the tasks file's git history newest→oldest and
// returns the most-recent committed version of each wanted ID, stopping once
// all are found. git errors are swallowed so a non-git directory simply yields
// no recoveries.
func (s *Store) recoverFromHistory(git Git, want map[int]bool) map[int]Task {
	found := make(map[int]Task)
	out, err := git.Output("log", "--all", "--format=%H", "--", s.file)
	if err != nil || out == "" {
		return found
	}
	for _, hash := range strings.Split(out, "\n") {
		if len(found) == len(want) {
			break
		}
		content, err := git.Output("show", hash+":"+s.file)
		if err != nil {
			continue // file may not exist in this commit
		}
		for _, t := range s.parseHistorical(content).Tasks {
			if !want[t.ID] {
				continue
			}
			if _, seen := found[t.ID]; !seen {
				found[t.ID] = t
			}
		}
	}
	return found
}

// committedIDs returns the set of task IDs present in the committed (HEAD)
// version of the tasks file. AutoClean uses this to avoid deleting tasks that
// have never been committed: with no committed version to fall back on, deleting
// such a task would lose it for good. A missing HEAD or file (e.g. the very
// first commit), a nil git, or any git error yields an empty set — so an
// uncommitted task is never auto-deleted.
func (s *Store) committedIDs(git Git) map[int]bool {
	ids := map[int]bool{}
	if git == nil {
		return ids
	}
	content, err := git.Output("show", "HEAD:"+s.file)
	if err != nil {
		return ids
	}
	for _, t := range s.parseHistorical(content).Tasks {
		ids[t.ID] = true
	}
	return ids
}

// GetWithHistory behaves like Get, but any requested ID missing from the active
// (non-deleted) working file is recovered from git history and returned as its
// most-recent committed version. It errors only if an ID exists in neither the
// working file nor history. git failures (e.g. not a git repo) degrade to the
// plain "task N not found" rather than surfacing a git error.
func (s *Store) GetWithHistory(git Git, ids []int) ([]Task, error) {
	if len(ids) == 0 {
		return s.Get(ids)
	}
	active, err := s.activeByID()
	if err != nil {
		return nil, err
	}
	// Collect IDs missing from the working file.
	missing := make(map[int]bool)
	for _, id := range ids {
		if _, ok := active[id]; !ok {
			missing[id] = true
		}
	}
	// Only consult git when something is actually missing.
	var recovered map[int]Task
	if len(missing) > 0 && git != nil {
		recovered = s.recoverFromHistory(git, missing)
	}
	out := make([]Task, 0, len(ids))
	for _, id := range ids {
		if t, ok := active[id]; ok {
			out = append(out, t)
			continue
		}
		if t, ok := recovered[id]; ok {
			out = append(out, t)
			continue
		}
		return nil, fmt.Errorf("task %d not found", id)
	}
	return out, nil
}
