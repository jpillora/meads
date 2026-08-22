package meads

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"
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
// single ListRefs call under prefix, keyed by id - the batch analogue of
// readTaskAndOID. SoftDelete's dependency cleanup needs the (task, oid) pair
// for id AND for every other task (to find which ones depend on it), then
// must CAS-write all the changed ones together; GitStore.Doctor and
// GitStore.Diverged need the same batch shape for BOTH TasksRefPrefix
// (local) and RemoteTasksRefPrefix (fetched remote-tracking), which is why
// prefix is a parameter rather than hardcoded. Re-resolving each ref
// individually the way readTaskAndOID/ReadFileAtRef does would cost an
// extra round trip per task for no benefit, since ListRefs already returns
// every oid up front.
//
// Exactly two git processes regardless of task count: the ListRefs above
// and one cat-file --batch for every blob (RefStore.ReadFilesAtCommits).
// Reading each blob with its own readTaskAtCommit was one process per task,
// which mattered doubly here - a single auto-sync calls this FOUR times
// (local and remote-tracking, in planIntegration and again in
// planDoctorFixes), inside an interactive command.
//
// The ids are sorted before reading so the batch's positional results can be
// mapped back to them: ListRefs returns a map, whose iteration order is
// deliberately randomised by Go and would otherwise differ between building
// the request and reading the response.
func (g *GitStore) loadAllWithOIDs(prefix string) (map[int]Task, map[int]OID, error) {
	refs, err := g.refs.ListRefs(prefix)
	if err != nil {
		return nil, nil, fmt.Errorf("listing task refs: %w", err)
	}
	oids := make(map[int]OID, len(refs))
	for name, oid := range refs {
		if id, ok := taskIDFromRef(prefix, name); ok {
			oids[id] = oid
		}
	}
	ids := make([]int, 0, len(oids))
	for id := range oids {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	commits := make([]OID, len(ids))
	for i, id := range ids {
		commits[i] = oids[id]
	}
	blobs, err := g.refs.ReadFilesAtCommits(commits, TaskFileName)
	if err != nil {
		return nil, nil, err
	}
	tasks := make(map[int]Task, len(ids))
	for i, id := range ids {
		var t Task
		if err := json.Unmarshal(blobs[i], &t); err != nil {
			return nil, nil, fmt.Errorf("parsing %s at %s: %w", TaskFileName, commits[i], err)
		}
		tasks[id] = t
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

// nowStamp is the timestamp format the meta "created"/"updated" keys carry,
// matching the file backend's Store.Add/Store.Update (mutate.go) exactly, so
// `md get --json` reads the same either side of a `md convert`.
//
// A var so tests can stub it: the alternative is sleeping past a second
// boundary to make two stamps differ, which is slow and only ever proves an
// ordering where a stub proves the value.
var nowStamp = func() string { return time.Now().UTC().Format(time.RFC3339) }

// cloneMeta returns a copy of m, never nil.
//
// Task is copied by value but its Meta map is NOT, so two Task values assigned
// from one another share it: a write through either is a write to both. Every
// place a Task is copied and then written to needs this.
func cloneMeta(m map[string]string) map[string]string {
	out := make(map[string]string, len(m)+1)
	for k, v := range m {
		out[k] = v
	}
	return out
}

// withMeta returns t with meta[key] = value, leaving t's own map untouched.
//
// Named withMeta, not setMeta, to keep its distance from the (*Task).SetMeta
// method in task.go, which is a different thing: in place, and it syncs the
// convenience fields for the keys that have them.
//
// The copy earns its keep twice over. Create would otherwise write "created"
// into the CALLER's input task, and the Task casUpdate returns would share its
// map with the one the decide callback saw - so a later write through either
// would silently edit the other.
func withMeta(t Task, key, value string) Task {
	meta := cloneMeta(t.Meta)
	meta[key] = value
	t.Meta = meta
	return t
}

// withClonedMeta returns t with its own private copy of Meta, so a caller
// handed the result cannot write back into the original.
func withClonedMeta(t Task) Task {
	t.Meta = cloneMeta(t.Meta)
	return t
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
//
// Every write through here stamps meta "updated" (task 84), which is why it
// happens HERE and not in each decide: it must be derived on the attempt that
// actually lands, like everything else the retry re-derives, and it must cover
// Claim as well as Update. An aborted decide stamps nothing - nothing was
// written, so nothing was updated.
func (g *GitStore) casUpdate(id int, action string, decide func(current Task) (next Task, ok bool, err error)) (Task, error) {
	message := fmt.Sprintf("%s task %d", action, id)
	var lastErr error
	for attempt := 0; attempt < maxCASRetries; attempt++ {
		current, oid, err := g.readTaskAndOID(id)
		if err != nil {
			return Task{}, err
		}
		// decide gets its own Meta map. Its implementations start with
		// `candidate := current`, which shares the map, and a mutate callback
		// calling SetPriority/SetStatus/SetTags writes straight through it -
		// so without this an ABORTED decide would hand `current` back with
		// the declined edits already in its Meta, contradicting the contract
		// above and leaving fields disagreeing with meta. Latent until this
		// backend started writing meta at all: an empty map marshals away, so
		// every task used to deserialize with a nil Meta and ensureMeta
		// allocated a fresh one on the copy.
		next, ok, err := decide(withClonedMeta(current))
		if err != nil {
			return Task{}, err
		}
		if !ok {
			return current, nil
		}
		next = withMeta(next, "updated", nowStamp())
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
		// "created" only, no "updated" - a task that has never been touched
		// since is not "updated", and the markdown formatter drops an
		// "updated" equal to "created" anyway (markdown.go). Unconditional,
		// like Store.Add: this allocates a fresh id, so any caller-supplied
		// timestamp describes some other task. ImportTask is the path that
		// preserves them.
		//
		// Which is why a supplied "updated" is DROPPED rather than left
		// alone. Overwriting only "created" would leave a brand-new task
		// claiming it was updated before it existed - the webui renders that
		// as "updated 27 years ago" (app.js taskTimestamp).
		candidate = withMeta(candidate, "created", nowStamp())
		delete(candidate.Meta, "updated") // safe: withMeta just gave us a private copy
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

// ImportTask writes t verbatim as a brand-new task ref (TaskRef(t.ID)),
// preserving t.ID exactly rather than allocating a fresh one via NextID -
// used by `md convert`'s file->git migration, where task ids must carry
// over unchanged (see cmd/md/convert.go). t.Deleted is preserved as given,
// so a soft-deleted source task lands already-deleted rather than being
// resurrected as active, matching git mode's "refs are never removed"
// model (GitStore.SoftDelete's doc comment).
//
// Fails with ErrCASConflict if a ref already exists at t.ID (create-only
// CAS against ZeroOID, exactly like Create) - a caller migrating into git
// mode is expected to check the whole namespace is empty first (see
// initCmd.runGit's identical precondition), not rely on this to skip
// collisions task by task.
//
// Unlike Create, this deliberately skips validateTaskDeps: a whole-file
// import calls ImportTask once per task, typically in ascending id order,
// while a forward reference (a lower id depending on one not yet imported)
// is entirely valid data that would spuriously fail per-call validation
// against an incomplete ref set. The caller is responsible for validating
// the batch as a whole once every task has been imported, e.g. via
// FindCycles.
func (g *GitStore) ImportTask(t Task) error {
	if t.ID <= 0 {
		return fmt.Errorf("import task: id must be positive (got %d)", t.ID)
	}
	if err := validateTitle(t.Title); err != nil {
		return err
	}
	return g.commitTaskCAS(t.ID, t, ZeroOID, fmt.Sprintf("import task %d", t.ID))
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
		tasks, oids, err := g.loadAllWithOIDs(TasksRefPrefix)
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

// HardDelete removes a task's ref outright - the whole commit chain, every
// version of it - and, in the same atomic transaction, strips the id from
// every other active task's DependsOn exactly as SoftDelete does.
//
// This destroys the one property the storage model otherwise guarantees:
// that an id, once used, is never reused. NextID is the maximum EXISTING
// task ref plus one, so hard-deleting the highest id lowers NextID and the
// next Create hands that number straight back out to a different task. Any
// reference held elsewhere - a commit message, a branch name, a link in
// another task's description, an agent's memory - then silently points at
// the wrong task rather than at a tombstone that reads back deleted. Nothing
// here detects or prevents that; the caller is expected to have said so out
// loud (cmd/md's `md del --force` prints the warning and names the id it is
// about to make reusable).
//
// It is also unrecoverable through meads. Deleting the ref makes its commits
// unreachable, so `md get`, `md list --history` and Restore all lose it
// together; only git's own reflog/fsck can find the objects, and only until
// they are gc'd. SoftDelete is what "delete" should almost always mean.
//
// The dependent cleanup is not optional here the way it might look. A ref
// that no longer exists is not merely deleted-and-skippable: validateDeps
// (tombstone.go) fails any task pointing at an id that is absent from the
// task set, so leaving the edges behind would make every dependent
// unwritable through Add/Update until someone hand-repaired it.
func (g *GitStore) HardDelete(id int) (Task, error) {
	var lastErr error
	for attempt := 0; attempt < maxCASRetries; attempt++ {
		tasks, oids, err := g.loadAllWithOIDs(TasksRefPrefix)
		if err != nil {
			return Task{}, err
		}
		target, ok := tasks[id]
		if !ok {
			return Task{}, fmt.Errorf("task %d not found", id)
		}

		// Same "clean dangling deps" pass as SoftDelete, with one difference:
		// already-deleted dependents are cleaned too. SoftDelete can skip
		// them because a tombstone's DependsOn is inert history pointing at
		// another tombstone that still exists; here the target ref is about
		// to stop existing, so a tombstone left pointing at it would hand
		// Restore a task whose dependency cannot be restored at all.
		updates := make([]RefUpdate, 0, 8)
		next := map[int]Task{}
		for depID, t := range tasks {
			if depID == id || len(t.DependsOn) == 0 {
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
		changedIDs := make([]int, 0, len(next))
		for cid := range next {
			changedIDs = append(changedIDs, cid)
		}
		sort.Ints(changedIDs) // deterministic batch order
		for _, cid := range changedIDs {
			commit, err := g.buildTaskVersion(cid, next[cid], oids[cid], fmt.Sprintf("remove dependency on permanently deleted task %d", id))
			if err != nil {
				return Task{}, err
			}
			updates = append(updates, RefUpdate{Name: g.TaskRef(cid), Next: commit, Prev: oids[cid]})
		}
		// ZeroOID as Next is AtomicUpdate's delete form, so the ref removal
		// and the dependent rewrites land as one transaction or not at all.
		updates = append(updates, RefUpdate{Name: g.TaskRef(id), Next: ZeroOID, Prev: oids[id]})

		if err := g.refs.AtomicUpdate(updates); err != nil {
			if !errors.Is(err, ErrCASConflict) {
				return Task{}, err
			}
			lastErr = err // lost the race: loop and re-read
			continue
		}
		return target, nil
	}
	return Task{}, fmt.Errorf("hard delete task %d: exhausted %d attempts: %w", id, maxCASRetries, lastErr)
}

// Restore clears a task's Deleted flag, bringing a tombstone back into the
// active set. It is the inverse of SoftDelete's FIRST half only, and
// deliberately not of its second.
//
// SoftDelete strips id from every dependent's DependsOn, and each of those
// edits lands as an ordinary commit on that dependent's own ref. Restore
// cannot undo them: it has no way to tell an edge SoftDelete removed from
// one the user dropped on purpose afterwards, and re-adding the wrong set
// would silently re-block tasks. What the tombstone does still hold is the
// restored task's OWN DependsOn, written back untouched.
//
// So a restored task can come back depending on an id that is itself still
// deleted, and readyTasks (query.go) only treats a "closed" dependency as
// satisfying - "deleted" never unblocks - leaving it permanently un-ready.
// That is why this returns the restored task rather than just an error:
// callers report those still-deleted dependencies (see cmd/md/restore.go).
// Restoring a whole tombstone set at once resolves them by construction.
//
// Unlike Create and Update this runs no validateTaskDeps. A tombstone's
// DependsOn is a historical record, not a new edit, and refusing to restore
// a task because a dependency is still deleted would make a set restorable
// only in dependency order - or, for a cycle among tombstones, not at all.
// The set-level check belongs to the caller, which can see the whole batch.
//
// Restoring a task that is not deleted is idempotent success and writes
// nothing at all, not even a no-op commit - matching SoftDelete's own
// behaviour on a repeat delete.
func (g *GitStore) Restore(id int) (Task, error) {
	return g.casUpdate(id, "restore", func(current Task) (Task, bool, error) {
		if !current.Deleted {
			return current, false, nil
		}
		next := current
		next.Deleted = false
		return next, true, nil
	})
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
