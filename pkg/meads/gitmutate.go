package meads

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
)

// ErrAlreadyClaimed is returned by Claim when the task is no longer open -
// either another agent's claim already landed, or the task was deleted.
var ErrAlreadyClaimed = errors.New("task already claimed")

// maxCASRetries bounds every mutating GitStore method's compare-and-swap
// retry loop (task 60's "retry trap"). Each attempt re-reads the ref(s) and
// re-runs the decision from scratch (see casUpdate, and SoftDelete's own
// batched variant) - the naive alternative, replaying a stale decision
// against freshly-read oids, silently stomps a concurrent writer's change.
// Bounded so a pathologically hot ref fails loudly instead of spinning
// forever.
const maxCASRetries = 5

// readTaskAndOID reads task id's current value and its ref's tip OID in one
// read, giving mutating methods the (task, oid) pair a compare-and-swap
// needs. A missing ref is reported as "task %d not found", wrapping
// ErrRefNotFound so callers can still match the sentinel.
func (g *GitStore) readTaskAndOID(id int) (Task, OID, error) {
	ref := g.TaskRef(id)
	content, oid, err := g.refs.ReadFileAtRef(ref, TaskFileName)
	if err != nil {
		if errors.Is(err, ErrRefNotFound) {
			return Task{}, "", fmt.Errorf("task %d not found: %w", id, ErrRefNotFound)
		}
		return Task{}, "", err
	}
	var t Task
	if err := json.Unmarshal(content, &t); err != nil {
		return Task{}, "", fmt.Errorf("parsing %s at %s: %w", TaskFileName, ref, err)
	}
	return t, oid, nil
}

// loadAllWithOIDs reads every task ref's current task and commit oid via a
// single ListRefs call, keyed by id - the batch analogue of readTaskAndOID.
// SoftDelete's dependency cleanup needs the (task, oid) pair for id AND for
// every other task (to find which ones depend on it), then must CAS-write
// all the changed ones together. Re-resolving each ref individually the way
// readTaskAndOID/ReadFileAtRef does would cost an extra round trip per task
// for no benefit, since ListRefs already returns every oid up front; reading
// each blob directly at its already-known oid (readTaskAtCommit) skips that
// second resolve.
func (g *GitStore) loadAllWithOIDs() (map[int]Task, map[int]OID, error) {
	refs, err := g.refs.ListRefs(TasksRefPrefix)
	if err != nil {
		return nil, nil, fmt.Errorf("listing task refs: %w", err)
	}
	tasks := make(map[int]Task, len(refs))
	oids := make(map[int]OID, len(refs))
	for name, oid := range refs {
		id, ok := taskIDFromRef(name)
		if !ok {
			continue
		}
		t, err := g.readTaskAtCommit(oid)
		if err != nil {
			return nil, nil, err
		}
		tasks[id] = t
		oids[id] = oid
	}
	return tasks, oids, nil
}

// commitTaskCAS marshals task and CAS-writes it onto id's ref, parented on
// the commit at oid (as returned by readTaskAndOID, or ZeroOID to create the
// ref). Returns ErrCASConflict, unwrapped from CommitFile, if oid is stale.
func (g *GitStore) commitTaskCAS(id int, task Task, oid OID, message string) error {
	data, err := json.Marshal(task)
	if err != nil {
		return fmt.Errorf("marshaling task %d: %w", id, err)
	}
	_, err = g.refs.CommitFile(g.TaskRef(id), TaskFileName, data, oid, message)
	return err
}

// buildTaskVersion marshals task and writes it as a new blob/tree/commit
// chain parented on prev (ZeroOID for a brand-new ref), returning the new
// commit's oid WITHOUT moving any ref. It is commitTaskCAS/CommitFile's
// object-writing half with the final CompareAndSwap left out: SoftDelete's
// dependency cleanup must build every changed task's new commit up front so
// the whole set can be submitted to RefStore.AtomicUpdate as one
// transaction, rather than moving each ref one at a time the way
// commitTaskCAS does.
func (g *GitStore) buildTaskVersion(id int, task Task, prev OID, message string) (OID, error) {
	data, err := json.Marshal(task)
	if err != nil {
		return "", fmt.Errorf("marshaling task %d: %w", id, err)
	}
	blob, err := g.refs.WriteBlob(data)
	if err != nil {
		return "", err
	}
	tree, err := g.refs.WriteTree([]TreeEntry{{Mode: "100644", Type: "blob", OID: blob, Name: TaskFileName}})
	if err != nil {
		return "", err
	}
	var parents []OID
	if prev != ZeroOID {
		parents = []OID{prev}
	}
	return g.refs.WriteCommit(tree, parents, message)
}

// validateTaskDeps runs the shared validateDeps (tombstone.go) - the same
// check mutate.go's Add and Update run, reused here rather than duplicated -
// against the full active task set with candidate substituted for id (or
// appended, when id isn't in the set yet: a Create in flight).
func (g *GitStore) validateTaskDeps(id int, candidate Task) error {
	all, err := g.LoadAll()
	if err != nil {
		return err
	}
	tasks := make([]Task, 0, len(all)+1)
	replaced := false
	for _, t := range all {
		if t.ID == id {
			tasks = append(tasks, candidate)
			replaced = true
		} else {
			tasks = append(tasks, t)
		}
	}
	if !replaced {
		tasks = append(tasks, candidate)
	}
	f := File{Tasks: tasks}
	return validateDeps(&f)
}

// casUpdate is the shared retry loop behind Update and Claim - every method
// that reads-modifies-writes a SINGLE EXISTING task ref. (Create is
// different: there is no current value to read, so it loops separately.
// SoftDelete is also different: its dependency cleanup can touch several
// task refs at once, so it runs its own batched read-decide-write loop
// against RefStore.AtomicUpdate instead of this single-ref one.)
//
// decide is invoked with a freshly re-read task on EVERY attempt and must
// derive its answer entirely from that argument, never from state captured
// on an earlier attempt - otherwise a lost race silently overwrites the
// winner (task 60's "retry trap"). Returning ok=false aborts with no write
// and no error, yielding current back unchanged - not every abort is a
// failure (e.g. Update's mutate declining to change anything). A non-nil
// err aborts immediately without retrying: it means the precondition
// genuinely no longer holds (e.g. Claim's ErrAlreadyClaimed), not that the
// write lost a race. Only ErrCASConflict loops back to re-read; any other
// failure from the write itself is returned immediately.
func (g *GitStore) casUpdate(id int, action string, decide func(current Task) (next Task, ok bool, err error)) (Task, error) {
	message := fmt.Sprintf("%s task %d", action, id)
	var lastErr error
	for attempt := 0; attempt < maxCASRetries; attempt++ {
		current, oid, err := g.readTaskAndOID(id)
		if err != nil {
			return Task{}, err
		}
		next, ok, err := decide(current)
		if err != nil {
			return Task{}, err
		}
		if !ok {
			return current, nil
		}
		if err := g.commitTaskCAS(id, next, oid, message); err != nil {
			if !errors.Is(err, ErrCASConflict) {
				return Task{}, err
			}
			lastErr = err // lost the race: loop and re-read
			continue
		}
		return next, nil
	}
	return Task{}, fmt.Errorf("%s task %d: exhausted %d attempts: %w", action, id, maxCASRetries, lastErr)
}

// Create allocates the next id, writes the task, and returns it with ID set.
//
// The create-only CAS against ZeroOID (CommitFile, via RefStore.
// CompareAndSwap) is itself the uniqueness guarantee: if two agents compute
// the same id, exactly one wins and the loser recomputes NextID and
// retries. Deliberately no shared counter ref and no atomic multi-ref batch
// - either would force every create to queue behind one contention point.
func (g *GitStore) Create(t Task) (Task, error) {
	if t.ID != 0 {
		return Task{}, fmt.Errorf("task ID must not be set (got %d)", t.ID)
	}
	if err := validateTitle(t.Title); err != nil {
		return Task{}, err
	}
	var lastErr error
	for attempt := 0; attempt < maxCASRetries; attempt++ {
		id, err := g.NextID()
		if err != nil {
			return Task{}, err
		}
		candidate := t
		candidate.ID = id
		if err := g.validateTaskDeps(id, candidate); err != nil {
			return Task{}, err
		}
		err = g.commitTaskCAS(id, candidate, ZeroOID, fmt.Sprintf("create task %d", id))
		if err == nil {
			return candidate, nil
		}
		if !errors.Is(err, ErrCASConflict) {
			return Task{}, err
		}
		lastErr = err // another agent already took id: recompute NextID and retry
	}
	return Task{}, fmt.Errorf("create task: exhausted %d attempts: %w", maxCASRetries, lastErr)
}

// Update applies mutate to the current version of task id and commits the
// result; mutate may return false to abort with no write. Matches the file
// backend's Store.Update (mutate.go): a soft-deleted task reads as not
// found, the resulting title is validated, and DependsOn is validated
// against the full active task set via the shared validateDeps.
func (g *GitStore) Update(id int, mutate func(*Task) (bool, error)) (Task, error) {
	return g.casUpdate(id, "update", func(current Task) (Task, bool, error) {
		if current.Deleted {
			return Task{}, false, fmt.Errorf("task %d not found", id)
		}
		candidate := current
		changed, err := mutate(&candidate)
		if err != nil {
			return Task{}, false, err
		}
		if !changed {
			return Task{}, false, nil
		}
		candidate.ID = id // guard against a mutate func that clobbers ID
		if err := validateTitle(candidate.Title); err != nil {
			return Task{}, false, err
		}
		if err := g.validateTaskDeps(id, candidate); err != nil {
			return Task{}, false, err
		}
		return candidate, true, nil
	})
}

// SoftDelete marks a task deleted and, in the same atomic transaction,
// strips it from every other active task's DependsOn - the git-mode
// equivalent of the file backend's "clean dangling deps" (Store.Delete,
// mutate.go). Without this, a task blocked on id would stay blocked
// forever: DependsOn would keep referencing a ref that will forever read
// back Deleted, and readyTasks (query.go) only treats a "closed" dependency
// as satisfying - "deleted" never unblocks it.
//
// The deleted task's ref and every affected dependent's ref move together
// via RefStore.AtomicUpdate, or none of them move. This is deliberately NOT
// a sequence of independent single-ref CAS updates (what casUpdate does):
// that could fail partway through and leave some dependents cleaned and
// others still pointing at a deleted task. Task 57's design doc assumed the
// atomic batch primitive would only ever be needed for `doctor`'s duplicate-
// id renumbering; this is a second legitimate use, for the same reason -
// several refs that must change as one unit.
//
// On a lost race (ErrCASConflict) the whole read-decide-write cycle restarts
// from a fresh read, bounded by maxCASRetries, exactly like casUpdate: the
// dependent set itself may have changed between attempts (e.g. a new task
// was created depending on id, or a dependent was independently updated), so
// the decision is re-derived from scratch each attempt rather than replaying
// a stale plan against fresh oids (task 60's "retry trap").
//
// The ref is never removed - ids are never reused (task 57's storage
// model: removing a task ref would orphan its whole commit chain). Deleting
// an already-deleted task is idempotent success and writes nothing at all,
// not even a no-op commit: unlike the file backend, whose "not found" on a
// repeat delete comes from tombstone pruning dropping the row, git mode
// never prunes, so the ref is always still there to check idempotently.
func (g *GitStore) SoftDelete(id int) (Task, error) {
	var lastErr error
	for attempt := 0; attempt < maxCASRetries; attempt++ {
		tasks, oids, err := g.loadAllWithOIDs()
		if err != nil {
			return Task{}, err
		}
		target, ok := tasks[id]
		if !ok {
			return Task{}, fmt.Errorf("task %d not found", id)
		}
		if target.Deleted {
			return target, nil
		}

		// Build the new state: id itself, plus every non-deleted task that
		// depends on it with id removed via SetDependsOn (mirrors
		// Store.Delete's "clean dangling deps" loop in mutate.go exactly,
		// including skipping already-deleted tasks - matching the file
		// backend's `if f.Tasks[i].Deleted { continue }`).
		target.Deleted = true
		next := map[int]Task{id: target}
		for depID, t := range tasks {
			if depID == id || t.Deleted || len(t.DependsOn) == 0 {
				continue
			}
			var clean []int
			for _, dep := range t.DependsOn {
				if dep != id {
					clean = append(clean, dep)
				}
			}
			if len(clean) == len(t.DependsOn) {
				continue // id wasn't among its deps
			}
			t.SetDependsOn(clean)
			next[depID] = t
		}

		// Build every changed task's new commit before moving any ref, then
		// submit them all in one AtomicUpdate - see the doc comment above.
		changedIDs := make([]int, 0, len(next))
		for cid := range next {
			changedIDs = append(changedIDs, cid)
		}
		sort.Ints(changedIDs) // deterministic batch order

		updates := make([]RefUpdate, 0, len(changedIDs))
		for _, cid := range changedIDs {
			message := fmt.Sprintf("remove dependency on deleted task %d", id)
			if cid == id {
				message = fmt.Sprintf("soft delete task %d", id)
			}
			commit, err := g.buildTaskVersion(cid, next[cid], oids[cid], message)
			if err != nil {
				return Task{}, err
			}
			updates = append(updates, RefUpdate{Name: g.TaskRef(cid), Next: commit, Prev: oids[cid]})
		}

		if err := g.refs.AtomicUpdate(updates); err != nil {
			if !errors.Is(err, ErrCASConflict) {
				return Task{}, err
			}
			lastErr = err // lost the race: loop and re-read
			continue
		}
		return target, nil
	}
	return Task{}, fmt.Errorf("soft delete task %d: exhausted %d attempts: %w", id, maxCASRetries, lastErr)
}

// Claim atomically transitions task id to inprogress, recording agentID and
// filesInScope. The precondition - status must be "open" and the task not
// deleted - is re-checked against a fresh read on every retry attempt, so a
// competing agent's already-landed claim is observed and this call fails
// with ErrAlreadyClaimed rather than overwriting it.
//
// filesInScope is advisory only (task 57): stored and returned verbatim,
// never arbitrated, never checked against other tasks' claims.
func (g *GitStore) Claim(id int, agentID string, filesInScope []string) (Task, error) {
	return g.casUpdate(id, "claim", func(current Task) (Task, bool, error) {
		if current.Deleted || current.Status != "open" {
			return Task{}, false, ErrAlreadyClaimed
		}
		next := current
		next.Status = "inprogress"
		next.AgentID = agentID
		next.FilesInScope = filesInScope
		return next, true, nil
	})
}
