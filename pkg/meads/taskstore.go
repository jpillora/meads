package meads

// TaskStore is the minimal read/write surface both the MCP server (pkg/mcp)
// and the web UI (pkg/webui) need from a task backend: enough to list, read,
// create, mutate, and soft-delete tasks, but none of the extra history/
// cycle-detection methods cmd/md/taskstore.go's fuller CLI-command seam adds
// on top (GetWithHistory, GetHistory, FindCycles) - neither consumer here
// needs them.
//
// *Store already satisfies this directly (its Get/Ready/Add/Update/Delete
// methods match exactly). *GitStore does not: its Create/Update/SoftDelete
// shapes differ (extra return values, an extra bool from the mutate func -
// see cmd/md/taskstore.go's doc comment on gitTaskStore for the exact
// mismatch), so git mode wires a small adapter with this exact shape
// instead of implementing it on GitStore itself.
type TaskStore interface {
	// Get returns active (non-deleted) tasks, all of them if ids is empty,
	// else exactly the requested ids in the order given (error if any is
	// missing or deleted).
	Get(ids []int) ([]Task, error)
	// Ready returns open, unblocked, non-deleted tasks sorted by priority.
	Ready() ([]Task, error)
	// Add creates a new task (ID must be zero) and returns its assigned id.
	Add(t Task) (int, error)
	// Update applies fn to task id's current value and persists the result.
	Update(id int, fn func(*Task)) error
	// Delete soft-deletes task id.
	Delete(id int) error
}
