package meads

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// TasksRefPrefix is the ref namespace holding one ref per task:
// refs/meads/tasks/<id>.
const TasksRefPrefix = "refs/meads/tasks/"

// TaskFileName is the path of the task JSON blob within each task ref's tree.
const TaskFileName = "task.json"

// GitStore is a read-only task store backed by git refs (see TASKS.md task
// 57 for the storage model): each task lives at refs/meads/tasks/<id>, a
// commit chain (one commit per version) whose tree holds a single
// task.json blob. Built on RefStore for the underlying plumbing.
type GitStore struct {
	refs *RefStore
	git  Git

	// configMu guards configOID/configCache - the Config() cache (see
	// gitconfig.go). GitStore is used from multiple goroutines (gitmutate.go's
	// Create/Update/Claim races already exercise that), so every access to
	// these two fields goes through the mutex.
	configMu sync.RWMutex
	// configOID is the ConfigRef oid configCache was parsed from. The zero
	// value "" means "never populated" - distinct from ZeroOID, which means
	// "populated, and the ref was confirmed absent" - so a fresh GitStore
	// always misses the cache on its first Config() call.
	configOID   OID
	configCache Config
}

// NewGitStore creates a GitStore backed by git.
func NewGitStore(git Git) *GitStore {
	return &GitStore{refs: NewRefStore(git), git: git}
}

// TaskRef returns the full ref name for a task id.
func (g *GitStore) TaskRef(id int) string {
	return TasksRefPrefix + strconv.Itoa(id)
}

// LoadAll returns every task, including soft-deleted ones, ascending by id.
// Returns nil, nil if there are no task refs at all.
func (g *GitStore) LoadAll() ([]Task, error) {
	refs, err := g.refs.ListRefs(TasksRefPrefix)
	if err != nil {
		return nil, fmt.Errorf("listing task refs: %w", err)
	}
	if len(refs) == 0 {
		return nil, nil
	}
	tasks := make([]Task, 0, len(refs))
	for ref := range refs {
		content, _, err := g.refs.ReadFileAtRef(ref, TaskFileName)
		if err != nil {
			return nil, fmt.Errorf("reading %s at %s: %w", TaskFileName, ref, err)
		}
		var t Task
		if err := json.Unmarshal(content, &t); err != nil {
			return nil, fmt.Errorf("parsing %s at %s: %w", TaskFileName, ref, err)
		}
		tasks = append(tasks, t)
	}
	sort.Slice(tasks, func(i, j int) bool { return tasks[i].ID < tasks[j].ID })
	return tasks, nil
}

// Get returns active (non-deleted) tasks. If ids is non-empty only the
// matching tasks are returned (in the order given); a missing or deleted id
// is an error: "task %d not found". Empty ids returns all active tasks.
func (g *GitStore) Get(ids []int) ([]Task, error) {
	all, err := g.LoadAll()
	if err != nil {
		return nil, err
	}
	return selectByIDs(filterDeleted(all), ids)
}

// GetWithHistory is the git-mode analogue of Store.GetWithHistory: like Get,
// but a requested id that has been soft-deleted is returned rather than
// erroring, so `md get <id>` still resolves a deleted task. Unlike the file
// backend it walks no history — soft delete keeps the task's ref, so the
// deleted task's current version is a direct read. Empty ids returns only
// active tasks, matching Get.
func (g *GitStore) GetWithHistory(ids []int) ([]Task, error) {
	if len(ids) == 0 {
		return g.Get(ids)
	}
	all, err := g.LoadAll()
	if err != nil {
		return nil, err
	}
	return selectByIDs(all, ids)
}

// Ready returns open, unblocked, non-deleted tasks sorted by priority.
func (g *GitStore) Ready() ([]Task, error) {
	all, err := g.LoadAll()
	if err != nil {
		return nil, err
	}
	return readyTasks(filterDeleted(all)), nil
}

// taskIDFromRef extracts the numeric task id from a ref name under
// TasksRefPrefix, e.g. "refs/meads/tasks/12" -> (12, true). It reports false
// for anything that isn't a plain integer directly under the prefix - a
// nested ref (an extra "/" segment) or a non-numeric suffix - so callers
// enumerating ListRefs(TasksRefPrefix) can skip junk without parsing it as
// an id or crashing on it (see NextID and loadAllWithOIDs).
func taskIDFromRef(name string) (int, bool) {
	suffix := strings.TrimPrefix(name, TasksRefPrefix)
	if strings.Contains(suffix, "/") {
		return 0, false // nested deeper than one segment below the prefix
	}
	id, err := strconv.Atoi(suffix)
	if err != nil {
		return 0, false // not a plain integer
	}
	return id, true
}

// NextID returns max(existing task id) + 1, or 1 when there are no tasks.
//
// It reads ref NAMES ONLY, never blob contents: parsing the trailing id off
// each refname under TasksRefPrefix is measurably faster than reading every
// task's content just to inspect its id (15ms vs 22ms+ at 500 tasks; see
// TASKS.md task 57). Refnames whose suffix isn't a plain integer, or that are
// nested more than one segment below the prefix, are ignored.
//
// Soft-deleted tasks still occupy their id: refs are never removed in git
// mode, so ids must never be reused, and the max is taken over every ref
// regardless of the task's deleted status.
func (g *GitStore) NextID() (int, error) {
	refs, err := g.refs.ListRefs(TasksRefPrefix)
	if err != nil {
		return 0, fmt.Errorf("listing task refs: %w", err)
	}
	next := 1
	for name := range refs {
		id, ok := taskIDFromRef(name)
		if !ok {
			continue
		}
		if id >= next {
			next = id + 1
		}
	}
	return next, nil
}

// History returns every version of task id, newest first, by walking the
// commit chain of its ref. Returns ErrRefNotFound if the task ref is absent.
func (g *GitStore) History(id int) ([]Task, error) {
	ref := g.TaskRef(id)
	// Resolve first so a missing ref reports ErrRefNotFound: RefStore.History
	// runs "rev-list <ref>" directly, which fails with a plain git error (not
	// one wrapping ErrRefNotFound) when the ref doesn't exist.
	if _, err := g.refs.ResolveRef(ref); err != nil {
		return nil, err
	}
	commits, err := g.refs.History(ref)
	if err != nil {
		return nil, err
	}
	tasks := make([]Task, 0, len(commits))
	for _, commit := range commits {
		t, err := g.readTaskAtCommit(commit)
		if err != nil {
			return nil, err
		}
		tasks = append(tasks, t)
	}
	return tasks, nil
}

// readTaskAtCommit reads and parses task.json from commit's tree, addressed
// directly as "<commit-oid>:<path>". ReadFileAtRef only accepts refnames (it
// resolves one internally via ResolveRef), so History - which walks raw
// commit oids from RefStore.History - needs this instead.
func (g *GitStore) readTaskAtCommit(commit OID) (Task, error) {
	content, err := g.git.OutputRaw("cat-file", "blob", string(commit)+":"+TaskFileName)
	if err != nil {
		return Task{}, fmt.Errorf("reading %s at %s: %w", TaskFileName, commit, err)
	}
	var t Task
	if err := json.Unmarshal(content, &t); err != nil {
		return Task{}, fmt.Errorf("parsing %s at %s: %w", TaskFileName, commit, err)
	}
	return t, nil
}
