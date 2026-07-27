package meads

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"

	"github.com/go-git/go-billy/v5"
)

// Backend identifies which storage backend a Tasks implementation wraps.
// Format was already taken (the md/csv parser interface, format.go), so the
// enum is named Backend instead.
type Backend int

const (
	// BackendMarkdown is a TASKS.md file backend (*Store with markdownFormat).
	BackendMarkdown Backend = iota
	// BackendCSV is a TASKS.csv file backend (*Store with csvFormat).
	BackendCSV
	// BackendGit is the ref-backed backend (*GitStore, refs/meads/*).
	BackendGit
)

// String returns the short label for the backend: "md", "csv", or "git".
func (b Backend) String() string {
	switch b {
	case BackendMarkdown:
		return "md"
	case BackendCSV:
		return "csv"
	case BackendGit:
		return "git"
	}
	return fmt.Sprintf("Backend(%d)", int(b))
}

// Tasks is the single interface all three storage backends are unified
// behind: markdown and CSV files (FileTasks, adapting *Store) and git refs
// (GitTasks, adapting *GitStore). It exists so consumers - cmd/md, pkg/mcp,
// pkg/webui, and library users like rais - can be written once against one
// shape and run correctly against whichever backend a project uses, with no
// per-consumer adapter (this interface replaces both cmd/md's old private
// taskStore seam and this package's old narrow TaskStore, which had already
// been hand-duplicated into three test doubles).
//
// Two implementations cover three backends: *Store already models md+csv
// behind one type, with Backend() reporting which; splitting FileTasks
// further would be churn with no caller-visible gain. *Store and *GitStore
// stay exported for backend-specific extras that don't belong on Tasks
// (RunImport/AutoClean/ImportAll; Diverged/Claim/Config/Acquire).
//
// GetWithHistory/GetHistory on a file backend need to walk git history,
// which *Store takes as a per-call argument; FileTasks captures the Git at
// construction so the interface doesn't carry it through every call. Git
// needs no walk at all: soft deletion keeps a task's ref forever, so
// GetWithHistory is a direct read and GetHistory is simply LoadAll.
type Tasks interface {
	// Backend reports which storage backend this Tasks wraps.
	Backend() Backend
	// Location describes where the tasks live: the absolute file path for
	// file backends, "refs/meads/tasks/*" for git.
	Location() string
	// Exists reports whether the backend has been initialised: file present
	// for file backends, any ref under refs/meads/ for git. "Nothing
	// initialised yet" is Exists() == false, NOT an error, so consumers can
	// tell the two apart cheaply.
	Exists() (bool, error)
	// Revision returns a cheap change token that differs iff the tasks
	// changed: fnv64a of the raw file bytes for file backends (the exact
	// hash rais's ProjectMeads.Hash already computes, so its values are
	// preserved), fnv64a of the sorted "refname oid" lines of every task
	// ref for git.
	Revision() (string, error)

	// Get returns active (non-deleted) tasks, all of them if ids is empty,
	// else exactly the requested ids in the order given (error if any is
	// missing or deleted).
	Get(ids []int) ([]Task, error)
	// GetWithHistory is like Get but a requested id that no longer resolves
	// as active (deleted, in git mode; deleted-and-history-recovered, in
	// file mode) is still returned rather than erroring.
	GetWithHistory(ids []int) ([]Task, error)
	// GetHistory returns every task ever created, including deleted ones -
	// the file backend reconstructs this from git log over the tasks file;
	// git keeps it directly (LoadAll).
	GetHistory() ([]Task, error)
	// Ready returns open, unblocked, non-deleted tasks sorted by priority.
	Ready() ([]Task, error)
	// FindCycles returns every circular dependency among active tasks.
	FindCycles() ([][]int, error)
	// Doctor detects and fixes duplicate task ids, returning what it fixed.
	Doctor() ([]DoctorFix, error)

	// Add creates a new task (ID must be zero) and returns its assigned id.
	Add(t Task) (int, error)
	// Update applies fn to task id's current value and persists the result.
	// fn mutates in place; there is no "decline to change anything" signal
	// at this seam (every caller so far always intends a write) - see
	// GitTasks.Update for how GitStore's richer shape is adapted.
	Update(id int, fn func(*Task)) error
	// Delete soft-deletes task id.
	Delete(id int) error

	// Sync publishes local state: git pushes refs/meads/* to origin; file
	// backends have nothing to publish and no-op.
	Sync(ctx context.Context) error
}

// FileTasks adapts *Store to Tasks. git is the Git implementation
// GetWithHistory/GetHistory need to walk commit history - Store takes it as
// a per-call argument (Store.GetWithHistory(git, ids)); the adapter captures
// it once at construction so the interface itself doesn't have to carry it
// through every call. git may be nil for consumers that never call the
// history methods (Store.GetWithHistory guards on nil; Store.GetHistory
// would fail, and must not be called).
type FileTasks struct {
	store *Store
	git   Git
}

// NewFileTasks wraps store as a Tasks, capturing git for the history walks.
func NewFileTasks(store *Store, git Git) FileTasks {
	return FileTasks{store: store, git: git}
}

// Backend reports BackendCSV or BackendMarkdown, matching the Format
// detectFormat picked from the file extension.
func (t FileTasks) Backend() Backend {
	if _, ok := t.store.fmt.(csvFormat); ok {
		return BackendCSV
	}
	return BackendMarkdown
}

// Location returns the tasks file's absolute path (the filesystem root
// joined with the path within it).
func (t FileTasks) Location() string {
	return filepath.Join(t.store.FS().Root(), t.store.Path())
}

// Exists reports whether the tasks file is present.
func (t FileTasks) Exists() (bool, error) {
	if _, err := t.store.fs.Stat(t.store.file); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// Revision returns fnv64a of the raw file bytes, hex-encoded - deliberately
// the same value rais's ProjectMeads.Hash already computes, so rais keeps
// its current hashes.
func (t FileTasks) Revision() (string, error) {
	f, err := t.store.fs.Open(t.store.file)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := fnv.New64a()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return strconv.FormatUint(h.Sum64(), 16), nil
}

func (t FileTasks) Get(ids []int) ([]Task, error) { return t.store.Get(ids) }

func (t FileTasks) GetWithHistory(ids []int) ([]Task, error) {
	return t.store.GetWithHistory(t.git, ids)
}

func (t FileTasks) GetHistory() ([]Task, error) { return t.store.GetHistory(t.git) }

func (t FileTasks) Ready() ([]Task, error) { return t.store.Ready() }

func (t FileTasks) FindCycles() ([][]int, error) { return t.store.FindCycles() }

func (t FileTasks) Doctor() ([]DoctorFix, error) { return t.store.Doctor() }

func (t FileTasks) Add(task Task) (int, error) { return t.store.Add(task) }

func (t FileTasks) Update(id int, fn func(*Task)) error { return t.store.Update(id, fn) }

func (t FileTasks) Delete(id int) error { return t.store.Delete(id) }

// Sync is a no-op: a file backend has nothing to publish.
func (t FileTasks) Sync(context.Context) error { return nil }

// FS returns the underlying filesystem, delegated from *Store so file-mode
// consumers that discover capabilities structurally (pkg/webui's
// fileLocator: the fsnotify watcher and startup banner) keep working
// unchanged through the adapter.
func (t FileTasks) FS() billy.Filesystem { return t.store.FS() }

// Path returns the file path within the filesystem - see FS.
func (t FileTasks) Path() string { return t.store.Path() }

// GitTasks adapts *GitStore to Tasks, reconciling the few places its API
// shape differs from *Store's:
//   - Add: GitStore.Create returns the full Task; only the id is needed here,
//     matching Store.Add's (int, error).
//   - Update: GitStore.Update's mutate func reports (changed bool, err error)
//     so a decision can abort with no write (used by e.g. Claim, which has
//     no CLI command yet); every caller wired through Tasks so far always
//     intends a write, so the shim below always returns true, and the
//     resulting Task is discarded to match Store.Update's error-only return.
//   - Delete: GitStore.SoftDelete returns the deleted Task; discarded here,
//     matching Store.Delete's error-only signature.
//   - GetWithHistory: no history walk needed - soft deletion keeps the ref
//     forever, so GitStore.GetWithHistory(ids) alone already resolves a
//     deleted id straight from its current value.
//   - GetHistory: no per-commit walk in git mode either; LoadAll's "every
//     task ref ever created, including soft-deleted" is the closest
//     analogue of "every task that ever existed".
type GitTasks struct {
	gs *GitStore
}

// NewGitTasks wraps gs as a Tasks.
func NewGitTasks(gs *GitStore) GitTasks {
	return GitTasks{gs: gs}
}

// Backend reports BackendGit.
func (t GitTasks) Backend() Backend { return BackendGit }

// Location returns the task-ref namespace glob: "refs/meads/tasks/*".
func (t GitTasks) Location() string { return TasksRefPrefix + "*" }

// Exists reports whether the refs/meads/* namespace is non-empty. It probes
// the WHOLE namespace, not just refs/meads/tasks/*: a fresh `init --git` has
// no tasks (only the config ref), and a tasks-only probe would mean git mode
// could never bootstrap (the first add would start a divergent tasks file).
func (t GitTasks) Exists() (bool, error) {
	refs, err := t.gs.refs.ListRefs(RefNamespace)
	if err != nil {
		return false, err
	}
	return len(refs) > 0, nil
}

// Revision returns fnv64a of the sorted "refname oid" lines of every task
// ref (GitStore.TaskRefOIDs, one for-each-ref), hex-encoded - a cheap change
// token that differs iff any task ref was added, removed, or moved.
func (t GitTasks) Revision() (string, error) {
	refs, err := t.gs.TaskRefOIDs()
	if err != nil {
		return "", err
	}
	lines := make([]string, 0, len(refs))
	for name, oid := range refs {
		lines = append(lines, name+" "+string(oid))
	}
	sort.Strings(lines)
	h := fnv.New64a()
	for _, line := range lines {
		h.Write([]byte(line))
		h.Write([]byte("\n"))
	}
	return strconv.FormatUint(h.Sum64(), 16), nil
}

func (t GitTasks) Get(ids []int) ([]Task, error) { return t.gs.Get(ids) }

func (t GitTasks) GetWithHistory(ids []int) ([]Task, error) { return t.gs.GetWithHistory(ids) }

func (t GitTasks) GetHistory() ([]Task, error) { return t.gs.LoadAll() }

func (t GitTasks) Ready() ([]Task, error) { return t.gs.Ready() }

func (t GitTasks) FindCycles() ([][]int, error) { return t.gs.FindCycles() }

func (t GitTasks) Doctor() ([]DoctorFix, error) { return t.gs.Doctor() }

func (t GitTasks) Add(task Task) (int, error) {
	created, err := t.gs.Create(task)
	if err != nil {
		return 0, err
	}
	return created.ID, nil
}

func (t GitTasks) Update(id int, fn func(*Task)) error {
	_, err := t.gs.Update(id, func(task *Task) (bool, error) {
		fn(task)
		return true, nil
	})
	return err
}

func (t GitTasks) Delete(id int) error {
	_, err := t.gs.SoftDelete(id)
	return err
}

// Sync pushes refs/meads/* to origin with an explicit refspec at push time -
// never a configured remote.origin.push, which would replace git's default
// matching/simple push behaviour for ordinary branches too (see
// cmd/md/push.go's pushRefspec, the CLI auto-push path this shares the
// refspec shape with).
func (t GitTasks) Sync(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	refspec := RefNamespace + "*:" + RefNamespace + "*"
	if err := t.gs.git.Run("push", "--porcelain", "origin", refspec); err != nil {
		return fmt.Errorf("pushing %s to origin: %w", RefNamespace+"*", err)
	}
	return nil
}

// TaskRefOIDs delegates GitStore.TaskRefOIDs so git-mode consumers that
// discover capabilities structurally (pkg/webui's refSnapshotter: the
// ref-polling change watcher) keep working unchanged through the adapter.
// It is not part of Tasks itself: no CLI command needs it, only the web
// UI's change-detection watcher.
func (t GitTasks) TaskRefOIDs() (map[string]OID, error) { return t.gs.TaskRefOIDs() }

// GitStore returns the underlying *GitStore, for callers that need
// git-specific extras (Diverged, Claim, Config, Acquire) that don't belong
// on Tasks.
func (t GitTasks) GitStore() *GitStore { return t.gs }

// Store returns the underlying *Store, for callers that need file-specific
// extras (RunImport, AutoClean, ImportAll) that don't belong on Tasks.
func (t FileTasks) Store() *Store { return t.store }

var (
	_ Tasks = FileTasks{}
	_ Tasks = GitTasks{}
)
