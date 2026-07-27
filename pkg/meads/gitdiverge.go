package meads

import (
	"errors"
	"sort"
)

// Divergence describes one task whose local (refs/meads/tasks/<id>) and
// last-fetched remote-tracking (refs/meads-remote/tasks/<id>) counterparts
// have diverged: both were mutated since a shared ancestor commit, and
// neither history is a fast-forward of the other. This is task 65's
// "offline divergence" problem - two clones each changed the SAME task
// while disconnected; each built a commit chain from a common parent, and
// the second one to push was rejected non-fast-forward (see cmd/md/push.go's
// divergenceMessage, which classifies that rejection). Since task 86 the
// repair is GitStore.Doctor's convergent renumbering, which the auto-pull
// path runs on every sync (see GitStore.Integrate).
//
// MergeBase is the shared ancestor commit - useful for anyone who wants to
// inspect the pre-divergence state directly, e.g. `git show
// <MergeBase>:task.json`. Local/Remote are each side's current content, so
// a caller can report exactly what the two sides say without re-reading
// anything.
type Divergence struct {
	ID        int  // the task id (both TaskRef(ID) and the remote-tracking equivalent)
	MergeBase OID  // the shared ancestor commit both sides diverged from
	LocalOID  OID  // refs/meads/tasks/ID's current commit
	RemoteOID OID  // refs/meads-remote/tasks/ID's current commit
	Local     Task // local content, as of LocalOID
	Remote    Task // fetched-remote content, as of RemoteOID
}

// Diverged reports every diverging task - see Divergence's doc comment. An
// id present on only one side, whose two sides are byte-identical, whose
// histories are unrelated (a create/create id collision), or where one side
// is a plain fast-forward of the other, is not a divergence and is omitted.
//
// Diverged is a pure READ: it resolves refs and reads blobs but writes
// nothing, and in particular it never touches refs/meads/tasks/* itself.
// The corresponding REPAIR lives in GitStore.Doctor (task 86): a diverged
// task's local version is re-homed at a fresh id and the id itself takes
// the fetched-remote version - no merge, no force-push, no data loss - so
// after a successful Doctor (or an auto-pull's Integrate) Diverged reports
// nothing. Diverged remains the read-only way to inspect a divergence
// before choosing to repair it.
func (g *GitStore) Diverged() ([]Divergence, error) {
	local, localOIDs, err := g.loadAllWithOIDs(TasksRefPrefix)
	if err != nil {
		return nil, err
	}
	remote, remoteOIDs, err := g.loadAllWithOIDs(RemoteTasksRefPrefix)
	if err != nil {
		return nil, err
	}

	var ids []int
	for id := range local {
		if _, ok := remote[id]; ok {
			ids = append(ids, id)
		}
	}
	sort.Ints(ids)

	var out []Divergence
	for _, id := range ids {
		lOID, rOID := localOIDs[id], remoteOIDs[id]
		if lOID == rOID {
			continue // identical: trivially not diverged
		}
		base, err := g.refs.MergeBase(lOID, rOID)
		if err != nil {
			if errors.Is(err, ErrUnrelatedHistories) {
				continue // a duplicate id, not a divergence - see GitStore.Doctor
			}
			return nil, err
		}
		if base == lOID || base == rOID {
			continue // one side is a fast-forward of the other: nothing to reconcile
		}
		out = append(out, Divergence{
			ID:        id,
			MergeBase: base,
			LocalOID:  lOID,
			RemoteOID: rOID,
			Local:     local[id],
			Remote:    remote[id],
		})
	}
	return out, nil
}
