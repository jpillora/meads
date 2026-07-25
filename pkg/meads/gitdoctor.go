package meads

import (
	"errors"
	"fmt"
	"sort"
)

// Doctor detects and repairs two kinds of task-id integrity problem that can
// only arise in git mode from a network partition (task 65 phase 8; design
// of record task 57's "Duplicate IDs offline"). It is the git-mode
// counterpart of Store.Doctor (mutate.go), but "duplicate" necessarily means
// something different here: a ref name IS the id in git mode
// (TasksRefPrefix + the id, gitstore.go), so two tasks can never literally
// share one ref the way two rows in a TASKS.md/csv file can share an ID
// column - there is only ever one refs/meads/tasks/<id> locally at a time.
// Concretely, Doctor finds and fixes:
//
//  1. Content/ref-name mismatch: a task ref's stored task.json "id" field
//     disagrees with the numeric id its own ref name encodes. The ref name
//     is authoritative - Get/NextID/every lookup keys off it, never off the
//     blob - so this is repaired in place: same ref, same id, corrected
//     content. No id is allocated and no DependsOn anywhere refers to this
//     task differently before or after, so nothing else needs updating.
//
//  2. Cross-clone duplicate: an id present in BOTH refs/meads/tasks/<id>
//     (local) and refs/meads-remote/tasks/<id> (the last `git fetch`'s
//     copy - see RemoteRefNamespace's doc comment) whose two commit
//     histories share NO common ancestor (RefStore.MergeBase returns
//     ErrUnrelatedHistories). That is exactly two clones independently
//     creating different tasks that happened to compute the same NextID
//     while partitioned - gitmutate.go's Create doc comment already notes
//     create-if-absent "cannot coordinate across a partition" - each
//     building its own commit chain from scratch. The local task keeps its
//     id (it is already this clone's own live state; running doctor
//     locally must not silently renumber work the caller already has); the
//     remote-tracking one is imported as a brand-new local task at a fresh
//     id from NextID, and every DependsOn edge within THIS doctor run that
//     pointed at a moved id is rewritten to match - mirroring the file
//     backend's Store.Doctor.
//
// Doctor deliberately does NOT touch an id whose two sides share a common
// ancestor: that is either a plain fast-forward (nothing wrong to fix) or
// an edit/edit conflict on ONE task, which is GitStore.Diverged's job to
// report, never renumbered here - renumbering it would fork the task's
// identity and orphan half its edit history under a new id, which is a
// strictly worse outcome than leaving it for a human (task 65's MVP
// requirement: fail loudly rather than lose or corrupt data).
//
// The whole batch - every mismatch repair and every duplicate import -
// commits as ONE atomic RefStore.AtomicUpdate transaction, each ref
// carrying its expected previous oid: if a concurrent writer lands on any
// one of the affected refs first, NOTHING in the batch moves, never a
// partial renumbering. Bounded by maxCASRetries and re-deriving its
// decision from a fresh read on every attempt, exactly like every other
// mutating GitStore method - see casUpdate's doc comment on the retry trap
// this avoids.
//
// The returned []DoctorFix reports both kinds of fix: a real renumber has
// OldID != NewID (case 2 above), while a content/ref-name repair (case 1)
// is reported with OldID == NewID - not a renumber, but callers still need
// to know a repair happened rather than wrongly concluding nothing was
// wrong.
func (g *GitStore) Doctor() ([]DoctorFix, error) {
	var lastErr error
	for attempt := 0; attempt < maxCASRetries; attempt++ {
		local, localOIDs, err := g.loadAllWithOIDs(TasksRefPrefix)
		if err != nil {
			return nil, err
		}
		remote, remoteOIDs, err := g.loadAllWithOIDs(RemoteTasksRefPrefix)
		if err != nil {
			return nil, err
		}
		fixes, updates, err := g.planDoctorFixes(local, localOIDs, remote, remoteOIDs)
		if err != nil {
			return nil, err
		}
		if len(updates) == 0 {
			return nil, nil // nothing to fix; do not even open an AtomicUpdate transaction
		}
		if err := g.refs.AtomicUpdate(updates); err != nil {
			if !errors.Is(err, ErrCASConflict) {
				return nil, err
			}
			lastErr = err // lost the race: loop and re-read
			continue
		}
		return fixes, nil
	}
	return nil, fmt.Errorf("doctor: exhausted %d attempts: %w", maxCASRetries, lastErr)
}

// planDoctorFixes computes, from one consistent snapshot of local and
// remote-tracking task refs, every fix Doctor should apply this attempt and
// the exact batch of ref updates that would apply them - the "read, then
// decide" half of Doctor's retry cycle (see casUpdate's doc comment on why
// that split matters: every attempt must re-derive its answer from a fresh
// read, never replay a decision computed against oids that may now be
// stale). Doctor calls this once per retry attempt;
// TestGitStore_Doctor_AtomicBatchLeavesNothingPartiallyMoved calls it
// directly to obtain a realistic batch - exactly how
// TestGitStore_SoftDelete_AtomicBatchLeavesNothingPartiallyMoved (gitmutate_
// test.go) reuses buildTaskVersion - so it can submit that batch under a
// deliberately stale prev and inspect a single failed AtomicUpdate call in
// isolation, rather than the eventual retry masking it.
//
// It writes candidate blob/tree/commit objects (via buildTaskVersion) for
// every fix but moves no ref itself - exactly like buildTaskVersion's other
// caller, SoftDelete. An object written here that never ends up referenced
// (this attempt loses the AtomicUpdate race) is harmless: unreferenced
// objects are simply eligible for eventual git gc, nothing is corrupted or
// left dangling in a way that matters.
func (g *GitStore) planDoctorFixes(
	local map[int]Task, localOIDs map[int]OID,
	remote map[int]Task, remoteOIDs map[int]OID,
) ([]DoctorFix, []RefUpdate, error) {
	var fixes []DoctorFix
	var updates []RefUpdate
	remap := make(map[int]int)

	// reserved tracks every id already spoken for during this planning pass
	// - every existing local id, every id remote-tracking currently holds
	// (even one this pass never touches - see below), plus every fresh id
	// already handed out to an earlier fix in this SAME pass - so
	// sequential allocation below can never hand out one fresh id twice.
	// Mirrors the file backend's Doctor, which recomputes nextID(&f)
	// against f.Tasks AFTER each fix is appended (mutate.go), for the
	// identical reason: duplicate pairs are processed one at a time, and
	// each allocation must see the previous one.
	//
	// remote ids are reserved too, not just local ones: without this, a
	// fresh id could land on a remote-tracking id that ISN'T colliding with
	// anything local yet (so this pass correctly leaves it alone - see
	// GitStore.Diverged's doc comment on why importing it is out of
	// scope), only for the newly-imported local ref to then collide with
	// THAT id the moment it is later pulled in some future run - a
	// collision this run introduced but wouldn't itself have caught.
	reserved := make(map[int]bool, len(local)+len(remote))
	maxID := 0
	for id := range local {
		reserved[id] = true
		if id > maxID {
			maxID = id
		}
	}
	for id := range remote {
		reserved[id] = true
		if id > maxID {
			maxID = id
		}
	}
	freshID := func() int {
		maxID++
		for reserved[maxID] {
			maxID++
		}
		reserved[maxID] = true
		return maxID
	}

	// --- 1. content/ref-name mismatches: repaired in place, same id,
	// processed in id order for a deterministic, reviewable batch. Reported
	// as a DoctorFix with OldID == NewID - not a renumber, but callers (in
	// particular doctorCmd) still need to know a repair happened, e.g. to
	// avoid claiming "no issues found" when one did. ---
	var mismatched []int
	for id, task := range local {
		if task.ID != id {
			mismatched = append(mismatched, id)
		}
	}
	sort.Ints(mismatched)
	for _, id := range mismatched {
		fixes = append(fixes, DoctorFix{OldID: id, NewID: id})

		task := local[id]
		task.ID = id
		commit, err := g.buildTaskVersion(id, task, localOIDs[id], fmt.Sprintf("doctor: fix task %d id mismatch", id))
		if err != nil {
			return nil, nil, err
		}
		updates = append(updates, RefUpdate{Name: g.TaskRef(id), Next: commit, Prev: localOIDs[id]})
	}

	// --- 2. cross-namespace duplicates: an id present in both local and
	// remote-tracking, unrelated histories only (a related pair is either a
	// fast-forward or GitStore.Diverged's territory - see this function's
	// doc comment). ---
	var dupIDs []int
	for id := range remote {
		if _, ok := local[id]; ok {
			dupIDs = append(dupIDs, id)
		}
	}
	sort.Ints(dupIDs)
	for _, id := range dupIDs {
		_, err := g.refs.MergeBase(localOIDs[id], remoteOIDs[id])
		if err == nil {
			continue // related: a fast-forward or a genuine divergence, never a duplicate
		}
		if !errors.Is(err, ErrUnrelatedHistories) {
			return nil, nil, err
		}

		newID := freshID()
		fixes = append(fixes, DoctorFix{OldID: id, NewID: newID})
		remap[id] = newID

		candidate := remote[id]
		candidate.ID = newID
		if len(candidate.DependsOn) > 0 {
			// Apply the remap built so far - exactly the file backend's
			// Doctor loop (mutate.go), including the same "reflects sibling
			// mappings built so far, not a fixpoint over the whole batch"
			// limitation it already documents there.
			remapped := make([]int, len(candidate.DependsOn))
			changed := false
			for i, dep := range candidate.DependsOn {
				if newDep, ok := remap[dep]; ok {
					remapped[i] = newDep
					changed = true
				} else {
					remapped[i] = dep
				}
			}
			if changed {
				candidate.SetDependsOn(remapped)
			}
		}
		// A fresh root commit (Prev: ZeroOID), deliberately NOT a
		// continuation of the remote-tracking chain: that chain's own older
		// commits still carry the OLD id in their content, so replaying them
		// onto the new ref would immediately re-trigger fix 1 above on the
		// next doctor run. Nothing is lost - the original chain stays
		// reachable forever via refs/meads-remote/*, exactly like a
		// file-backend renumbered task's pre-rename history remains
		// reachable via git log of the whole file.
		commit, err := g.buildTaskVersion(newID, candidate, ZeroOID, fmt.Sprintf("doctor: import duplicate task %d as %d", id, newID))
		if err != nil {
			return nil, nil, err
		}
		updates = append(updates, RefUpdate{Name: g.TaskRef(newID), Next: commit, Prev: ZeroOID})
	}

	return fixes, updates, nil
}
