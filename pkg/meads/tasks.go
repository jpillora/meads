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
	// Restore clears task id's deleted flag, returning it to the active
	// set. Restoring an already-active task is idempotent success. The two
	// backends differ in reach, not in meaning: git mode keeps every
	// tombstone forever and can restore any of them, while file mode prunes
	// tombstones on write and so can only reach one it still carries - see
	// GitStore.Restore and Store.Restore.
	Restore(id int) error
	// HardDelete erases task id instead of tombstoning it, giving up the
	// guarantee that its id is never reused - a later Add can hand the same
	// number to a different task. It is unrecoverable: unlike Delete, no
	// Restore, GetWithHistory or GetHistory can reach the task afterwards.
	// See GitStore.HardDelete and Store.HardDelete.
	HardDelete(id int) error

	// Sync synchronises with origin: git PULLS first (fetch + integrate,
	// re-homing contended local tasks at fresh ids - see GitStore.Pull and
	// GitStore.Doctor), then pushes refs/meads/*; file backends have
	// nothing to sync and no-op.
	//
	// Mutations never call Sync implicitly. Embedders decide when network I/O
	// is appropriate and invoke it themselves; the md CLI layers its own
	// best-effort detached scheduler on top.
	//
	// The report says what happened, and is never nil - including
	// alongside a non-nil error, so a caller can still tell a divergence
	// (SyncReport.Rejected: local refs intact, the next sync reconciles)
	// from a genuine failure. Reporting it is the caller's job: nothing
	// under pkg/meads prints.
	Sync(ctx context.Context) (*SyncReport, error)
}

// SyncReport describes what one Sync did, as data, so a caller can report
// or react to it without Sync itself printing anything: cmd/md renders it
// to stderr on the command that caused it, and a server embedding meads
// reacts to the id re-homes below, which rename a task out from under
// anything holding its id.
type SyncReport struct {
	// Integrate is what the pull half reconciled - adopted ids,
	// fast-forwarded ids, the config ref, and Doctor's fixes including the
	// re-homes. Never nil, even when the pull did nothing at all (no
	// origin, or nothing new), so callers never have to nil-check it.
	Integrate *IntegrateReport
	// PushOutput is the push attempt's combined output, verbatim and
	// regardless of success: for a rejected push the output IS the
	// diagnosis (see PushRejected). Empty for file backends, and when the
	// push never ran at all because the context was already done.
	PushOutput string
	// Rejected reports the push was refused as non-fast-forward
	// (PushRejected over PushOutput). It is deliberately NOT an error on
	// its own: the local refs are committed and safe, and the next sync's
	// pull normally reconciles it - so a caller should log it and carry
	// on, not treat it as a failed write. The push error is still
	// returned alongside, so a caller that only checks err is never
	// silently misled.
	Rejected bool
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

func (t FileTasks) Restore(id int) error { return t.store.Restore(id) }

func (t FileTasks) HardDelete(id int) error { return t.store.HardDelete(id) }

// Sync is a no-op: a file backend has nothing to publish. The report is
// still non-nil and empty, so a caller can inspect it unconditionally
// without caring which backend it holds.
func (t FileTasks) Sync(context.Context) (*SyncReport, error) {
	return &SyncReport{Integrate: &IntegrateReport{}}, nil
}

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

func (t GitTasks) Restore(id int) error {
	_, err := t.gs.Restore(id)
	return err
}

func (t GitTasks) HardDelete(id int) error {
	_, err := t.gs.HardDelete(id)
	return err
}

// Sync pulls, then pushes. The pull (GitStore.Pull) fetches origin and
// integrates what arrived - adopting new tasks, fast-forwarding unmoved
// ones, and re-homing contended local tasks at fresh ids via Doctor - so
// the push that follows converges instead of rejecting non-fast-forward.
// The push uses an explicit refspec at push time - never a configured
// remote.origin.push, which would replace git's default matching/simple
// push behaviour for ordinary branches too.
//
// ctx bounds both network halves for real - the fetch inside PullContext
// and the push below are killed the moment it is done, so a caller passing
// a deadline gets one (a remote git command is otherwise unbounded; see
// ContextGit). The integration between them is local git work and is not
// individually cancellable, so ctx is checked at that boundary instead.
func (t GitTasks) Sync(ctx context.Context) (*SyncReport, error) {
	report := &SyncReport{Integrate: &IntegrateReport{}}
	if err := ctx.Err(); err != nil {
		return report, err
	}
	if _, err := t.gs.EnsureGitRefProtocolVersion(); err != nil {
		return report, err
	}
	integrated, pullErr := t.gs.PullContext(ctx)
	if integrated != nil {
		report.Integrate = integrated
	}
	// A failed pull does NOT skip the push. Publishing work this clone
	// already committed is useful on its own, and the fetch is the half
	// most likely to fail for a reason the push does not share; the worst
	// case is a rejection, which is now reported and reconciled by the
	// next sync rather than being a dead end. What a failed pull DOES skip
	// is the integration, inside PullContext: reconciling against
	// remote-tracking refs that a failed fetch just left stale could
	// renumber against facts that are no longer true.
	//
	// An expired context is the one exception - there is no point starting
	// a second network command against a deadline that has already passed.
	if err := ctx.Err(); err != nil {
		return report, errors.Join(pullErr, err)
	}
	// A newer fetched protocol is not an ordinary pull failure. Pushing this
	// older binary's refs after detecting it could publish data with semantics
	// the repository has explicitly moved beyond.
	if errors.Is(pullErr, ErrGitRefProtocolUpgradeRequired) {
		return report, pullErr
	}
	// Integration may have adopted an older/missing config marker. Re-stamp
	// the local protocol immediately before advertising any refs.
	if _, err := t.gs.EnsureGitRefProtocolVersion(); err != nil {
		return report, errors.Join(pullErr, err)
	}
	// The push output is captured rather than discarded because a
	// rejection is only diagnosable from git's own porcelain status lines
	// (see PushRejected) - and it is captured on the FAILURE path too,
	// which is the only path that has any.
	refspec := RefNamespace + "*:" + RefNamespace + "*"
	out, pushErr := combinedOutputContext(ctx, t.gs.git, "push", "--porcelain", "origin", refspec)
	report.PushOutput = out
	report.Rejected = PushRejected(out)
	// exec.ErrWaitDelay is the one "failure" that means the push SUCCEEDED.
	// os/exec only ever reports it when the process exited zero (see its own
	// doc comment) but a descendant still held the output pipes past
	// WaitDelay - git's transport helpers outliving git is the ordinary case,
	// an SSH ControlMaster most of all. Left alone it turns a healthy push
	// into `md sync` exiting non-zero with `exec: WaitDelay expired before I/O
	// complete` while the refs are demonstrably on origin.
	//
	// It is swallowed HERE rather than inside ExecGit.CombinedOutputContext
	// because only this caller knows the truncation it warns about is
	// harmless: `out` feeds PushRejected alone, and a zero exit means git did
	// not reject anything, so "not rejected" is the right answer whether or
	// not the porcelain lines arrived in full. OutputContext deliberately
	// keeps the error - clone.go's ls-remote DOES decide on its output, and a
	// short read there is cached in a marker ref (resolveCloneBackend).
	if successfulWaitDelay(ctx, pushErr) {
		pushErr = nil
	}
	if pushErr != nil {
		pushErr = fmt.Errorf("pushing %s to origin: %w", RefNamespace+"*", pushErr)
	}
	return report, errors.Join(pullErr, pushErr)
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
