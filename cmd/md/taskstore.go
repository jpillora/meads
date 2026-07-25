package main

import (
	"fmt"

	"github.com/jpillora/meads/pkg/meads"
)

// taskStore is the seam between CLI commands and the two storage backends -
// the file-backed *meads.Store and the ref-backed *meads.GitStore - so a
// command can be written once against this interface and run correctly
// under either mode (see globals.tasks in main.go).
//
// Method shapes mirror *meads.Store's existing signatures, the shape every
// command wired to this seam already called before git mode existed, NOT
// *meads.GitStore's: GitStore's extra return values (e.g. Create/Update/
// SoftDelete all return the resulting Task) aren't needed by any command
// wired in this phase, so gitTaskStore below discards them rather than
// widening every call site to match GitStore. Neither backend's own
// exported API changes - each adapter is a thin translation, not a
// reimplementation.
type taskStore interface {
	// Get returns active (non-deleted) tasks, all of them if ids is empty,
	// else exactly the requested ids in the order given (error if any is
	// missing or deleted).
	Get(ids []int) ([]meads.Task, error)
	// GetWithHistory is like Get but a requested id that no longer resolves
	// as active (deleted, in git mode; deleted-and-history-recovered, in
	// file mode) is still returned rather than erroring. Used by `md get`.
	GetWithHistory(ids []int) ([]meads.Task, error)
	// GetHistory returns every task ever created, including deleted ones -
	// the file backend reconstructs this from git log over TasksFile;
	// GitStore already keeps it directly (LoadAll). Used by `md list
	// --history`.
	GetHistory() ([]meads.Task, error)
	// Ready returns open, unblocked, non-deleted tasks sorted by priority.
	Ready() ([]meads.Task, error)
	// FindCycles returns every circular dependency among active tasks.
	FindCycles() ([][]int, error)
	// Add creates a new task (ID must be zero) and returns its assigned id.
	Add(t meads.Task) (int, error)
	// Update applies fn to task id's current value and persists the result.
	// fn mutates in place; there is no "decline to change anything" signal
	// at this seam (every command wired so far always intends a write) - see
	// gitTaskStore.Update.
	Update(id int, fn func(*meads.Task)) error
	// Delete soft-deletes task id.
	Delete(id int) error
}

// fileTaskStore adapts *meads.Store to taskStore. git is the meads.Git
// implementation GetWithHistory/GetHistory need to walk commit history - the
// file backend takes it as a per-call argument (Store.GetWithHistory(git,
// ids)); the adapter captures it once at construction so the interface
// itself doesn't have to carry it through every call.
type fileTaskStore struct {
	store *meads.Store
	git   meads.Git
}

func (a fileTaskStore) Get(ids []int) ([]meads.Task, error) { return a.store.Get(ids) }

func (a fileTaskStore) GetWithHistory(ids []int) ([]meads.Task, error) {
	return a.store.GetWithHistory(a.git, ids)
}

func (a fileTaskStore) GetHistory() ([]meads.Task, error) { return a.store.GetHistory(a.git) }

func (a fileTaskStore) Ready() ([]meads.Task, error) { return a.store.Ready() }

func (a fileTaskStore) FindCycles() ([][]int, error) { return a.store.FindCycles() }

func (a fileTaskStore) Add(t meads.Task) (int, error) { return a.store.Add(t) }

func (a fileTaskStore) Update(id int, fn func(*meads.Task)) error { return a.store.Update(id, fn) }

func (a fileTaskStore) Delete(id int) error { return a.store.Delete(id) }

// gitTaskStore adapts *meads.GitStore to taskStore, reconciling the few
// places its API shape differs from *meads.Store's (see taskStore's doc
// comment):
//   - Add: GitStore.Create returns the full Task; only the id is needed here,
//     matching Store.Add's (int, error).
//   - Update: GitStore.Update's mutate func reports (changed bool, err error)
//     so a decision can abort with no write (used by e.g. Claim, which has
//     no CLI command yet); every command wired through this seam so far
//     always intends a write, so the shim below always returns true, and the
//     resulting Task is discarded to match Store.Update's error-only return.
//   - Delete: GitStore.SoftDelete returns the deleted Task; discarded here,
//     matching Store.Delete's error-only signature.
//   - GetWithHistory: no history walk needed - soft deletion keeps the ref
//     forever, so GitStore.GetWithHistory(ids) alone already resolves a
//     deleted id straight from its current value.
//   - GetHistory: no per-commit walk in git mode either; LoadAll's "every
//     task ref ever created, including soft-deleted" is the closest
//     analogue of "every task that ever existed".
type gitTaskStore struct {
	gs *meads.GitStore
}

func (a gitTaskStore) Get(ids []int) ([]meads.Task, error) { return a.gs.Get(ids) }

func (a gitTaskStore) GetWithHistory(ids []int) ([]meads.Task, error) {
	return a.gs.GetWithHistory(ids)
}

func (a gitTaskStore) GetHistory() ([]meads.Task, error) { return a.gs.LoadAll() }

func (a gitTaskStore) Ready() ([]meads.Task, error) { return a.gs.Ready() }

func (a gitTaskStore) FindCycles() ([][]int, error) { return a.gs.FindCycles() }

func (a gitTaskStore) Add(t meads.Task) (int, error) {
	created, err := a.gs.Create(t)
	if err != nil {
		return 0, err
	}
	return created.ID, nil
}

func (a gitTaskStore) Update(id int, fn func(*meads.Task)) error {
	_, err := a.gs.Update(id, func(t *meads.Task) (bool, error) {
		fn(t)
		return true, nil
	})
	return err
}

func (a gitTaskStore) Delete(id int) error {
	_, err := a.gs.SoftDelete(id)
	return err
}

// errGitModeUnsupported reports that cmd's file-backend implementation has
// no GitStore equivalent yet (e.g. Store.Doctor, Store.RunImport, or the
// long-running MCP/webui servers), so running it against a git-mode repo
// would silently operate on an unrelated or nonexistent TasksFile rather
// than the active refs/meads/* store. Commands not wired to the taskStore
// seam above call this instead of guessing.
func errGitModeUnsupported(cmd string) error {
	return fmt.Errorf("%s: not supported in git mode yet", cmd)
}
