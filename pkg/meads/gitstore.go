package meads

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// RefNamespace is the root of every ref meads ever writes in git mode:
// TasksRefPrefix, ConfigRef (gitconfig.go), and later a lock ref (task 57's
// phase 7). `md init --git` checks this whole namespace - not just
// TasksRefPrefix - so it refuses to clobber state a task-only check would
// miss, e.g. a config ref written by init with zero tasks created yet.
const RefNamespace = "refs/meads/"

// TasksRefPrefix is the ref namespace holding one ref per task:
// refs/meads/tasks/<id>.
const TasksRefPrefix = "refs/meads/tasks/"

// RemoteRefNamespace is where a plain `git fetch` lands the remote's whole
// refs/meads/* namespace - see cmd/md/init.go's meadsFetchRefspec - rather
// than refs/meads/* itself. This mirrors git's own convention for ordinary
// branches (a fetch refspec of "+refs/heads/*:refs/remotes/origin/*" lands
// in refs/remotes/origin/*, never overwriting refs/heads/* directly): a
// fetch must never force-update the namespace this package treats as
// local, authoritative state, or a plain `git fetch` could silently
// discard a not-yet-pushed local commit the instant it runs (see
// GitStore.Diverged's doc comment, and task 65's phase 8 notes).
const RemoteRefNamespace = "refs/meads-remote/"

// RemoteTasksRefPrefix is RemoteRefNamespace's analogue of TasksRefPrefix:
// where a fetched-but-not-yet-integrated task ref lands, read by
// GitStore.Doctor (cross-clone duplicate ids) and GitStore.Diverged
// (edit/edit conflicts) but never written by this package - it is entirely
// owned by `git fetch`.
const RemoteTasksRefPrefix = RemoteRefNamespace + "tasks/"

// TaskFileName is the path of the task JSON blob within each task ref's tree.
const TaskFileName = "task.json"

// GitStore is a read-only task store backed by git refs (see TASKS.md task
// 57 for the storage model): each task lives at refs/meads/tasks/<id>, a
// commit chain (one commit per version) whose tree holds a single
// task.json blob. Built on RefStore for the underlying plumbing.
type GitStore struct {
	refs *RefStore
	git  Git

	// configMu guards the oid-keyed config and protocol cache (see
	// gitconfig.go). GitStore is used from multiple goroutines (gitmutate.go's
	// Create/Update/Claim races already exercise that), so every access to
	// these fields goes through the mutex.
	configMu sync.RWMutex
	// configOID is the ConfigRef oid configCache was parsed from. The zero
	// value "" means "never populated" - distinct from ZeroOID, which means
	// "populated, and the ref was confirmed absent" - so a fresh GitStore
	// always misses the cache on its first Config() call.
	configOID   OID
	configCache Config
	// configProtocolVersion/configProtocolExplicit describe the protocol
	// marker parsed from the same ConfigRef oid as configCache.
	configProtocolVersion  int
	configProtocolExplicit bool
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
//
// Two git processes total, whatever the store's size: one for-each-ref to
// enumerate the refs and one cat-file --batch to read them all (see
// RefStore.ReadFilesAtCommits). It used to be a ReadFileAtRef per task -
// a for-each-ref AND a cat-file each - so a read scaled linearly in
// processes and a 100-task store spent most of a second in fork/exec.
func (g *GitStore) LoadAll() ([]Task, error) {
	return g.loadAllUnchecked()
}

// loadAllUnchecked is LoadAll after its caller has validated the protocol.
// Mutating operations use it after EnsureGitRefProtocolVersion so one logical
// operation does not repeatedly resolve and parse ConfigRef.
func (g *GitStore) loadAllUnchecked() ([]Task, error) {
	tasks, _, err := g.loadAllWithOIDs(TasksRefPrefix)
	if err != nil {
		return nil, err
	}
	if len(tasks) == 0 {
		return nil, nil
	}
	out := make([]Task, 0, len(tasks))
	for _, t := range tasks {
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
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

// taskIDFromRef extracts the numeric task id from a ref name under prefix
// (TasksRefPrefix for local refs, RemoteTasksRefPrefix for fetched
// remote-tracking refs - see loadAllWithOIDs), e.g. taskIDFromRef(
// TasksRefPrefix, "refs/meads/tasks/12") -> (12, true). It reports false
// for anything that isn't a plain integer directly under prefix - a name
// not actually under prefix at all, a nested ref (an extra "/" segment), or
// a non-numeric suffix - so callers enumerating ListRefs(prefix) can skip
// junk without parsing it as an id or crashing on it (see NextID and
// loadAllWithOIDs).
func taskIDFromRef(prefix, name string) (int, bool) {
	suffix := strings.TrimPrefix(name, prefix)
	if suffix == name {
		return 0, false // name wasn't actually under prefix
	}
	if strings.Contains(suffix, "/") {
		return 0, false // nested deeper than one segment below the prefix
	}
	id, err := strconv.Atoi(suffix)
	if err != nil {
		return 0, false // not a plain integer
	}
	return id, true
}

// TaskRefOIDs returns the current commit oid of every task ref
// (TasksRefPrefix), keyed by full ref name. Used by pkg/webui's watcher to
// detect task changes by polling rather than fsnotify: a ref moves between
// a loose file under .git/refs/meads/tasks/** and .git/packed-refs (git
// pack-refs, git gc), so it can change with no loose-file event at all once
// packed - no single file or directory can be watched reliably for git-mode
// changes the way fsnotify watches a single tasks file (see
// pkg/webui/watch.go's refSnapshotter). A plain wrapper over
// RefStore.ListRefs, exported here since RefStore itself is not.
func (g *GitStore) TaskRefOIDs() (map[string]OID, error) {
	if err := g.CheckGitRefProtocol(); err != nil {
		return nil, err
	}
	return g.refs.ListRefs(TasksRefPrefix)
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
	if err := g.CheckGitRefProtocol(); err != nil {
		return 0, err
	}
	return g.nextIDUnchecked()
}

func (g *GitStore) nextIDUnchecked() (int, error) {
	refs, err := g.refs.ListRefs(TasksRefPrefix)
	if err != nil {
		return 0, fmt.Errorf("listing task refs: %w", err)
	}
	next := 1
	for name := range refs {
		id, ok := taskIDFromRef(TasksRefPrefix, name)
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
	if err := g.CheckGitRefProtocol(); err != nil {
		return nil, err
	}
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

// FindCycles returns every circular dependency among active (non-deleted)
// tasks - the git-mode analogue of Store.FindCycles (mutate.go's file-backed
// sibling). It reuses the same unexported depGraph/findCycles helpers
// cycles.go already provides for the file backend, wrapping the active task
// list in a File{} since that's the shape those helpers expect - so the two
// backends can never disagree on what counts as a cycle.
func (g *GitStore) FindCycles() ([][]int, error) {
	active, err := g.Get(nil)
	if err != nil {
		return nil, err
	}
	ids, adj := depGraph(&File{Tasks: active})
	return findCycles(ids, adj), nil
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
