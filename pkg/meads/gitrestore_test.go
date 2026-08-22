package meads

import (
	"slices"
	"testing"
)

// Restore clears the deleted flag and puts the task back where Get can see
// it, with its stored record intact rather than a stub.
func TestGitStore_Restore_ReturnsTaskToActiveSet(t *testing.T) {
	gs, _, _ := newGitStoreRepo(t)
	created, err := gs.Create(Task{Title: "doomed", Status: "open", Priority: "P1", Description: "the details"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := gs.SoftDelete(created.ID); err != nil {
		t.Fatalf("SoftDelete: %v", err)
	}

	restored, err := gs.Restore(created.ID)
	if err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if restored.Deleted {
		t.Errorf("Restore() returned Deleted = true, want false")
	}

	got, err := gs.Get([]int{created.ID})
	if err != nil {
		t.Fatalf("Get after Restore: %v, want the task to be active again", err)
	}
	if len(got) != 1 {
		t.Fatalf("Get = %+v, want one task", got)
	}
	// The whole point of a git-mode tombstone is that it keeps the full
	// record, so a restore must return the task, not an empty shell.
	if got[0].Title != "doomed" || got[0].Priority != "P1" || got[0].Description != "the details" {
		t.Errorf("restored task = %+v, want title/priority/description preserved", got[0])
	}
}

// Restoring a task that was never deleted writes nothing at all - no ref
// movement, no no-op commit - matching SoftDelete's repeat-delete behaviour.
func TestGitStore_Restore_NotDeletedIsNoOp(t *testing.T) {
	gs, rs, _ := newGitStoreRepo(t)
	created, err := gs.Create(Task{Title: "alive", Status: "open"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	before, err := rs.ResolveRef(gs.TaskRef(created.ID))
	if err != nil {
		t.Fatalf("ResolveRef: %v", err)
	}

	if _, err := gs.Restore(created.ID); err != nil {
		t.Fatalf("Restore of a live task: %v, want idempotent success", err)
	}

	after, err := gs.Restore(created.ID)
	if err != nil {
		t.Fatalf("second Restore: %v", err)
	}
	if after.Deleted {
		t.Errorf("Restore() returned Deleted = true, want false")
	}
	oid, err := rs.ResolveRef(gs.TaskRef(created.ID))
	if err != nil {
		t.Fatalf("ResolveRef after Restore: %v", err)
	}
	if oid != before {
		t.Errorf("ref moved to %s, want it unchanged at %s (a no-op restore must not commit)", oid, before)
	}
}

// Delete then restore then delete again: the flag tracks the last call, and
// the ref keeps accumulating history rather than being rewritten.
func TestGitStore_Restore_RoundTrip(t *testing.T) {
	gs, _, _ := newGitStoreRepo(t)
	created, err := gs.Create(Task{Title: "yo-yo", Status: "open"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := gs.SoftDelete(created.ID); err != nil {
		t.Fatalf("SoftDelete: %v", err)
	}
	if _, err := gs.Restore(created.ID); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if _, err := gs.SoftDelete(created.ID); err != nil {
		t.Fatalf("second SoftDelete: %v", err)
	}

	if _, err := gs.Get([]int{created.ID}); err == nil {
		t.Errorf("Get after re-delete = nil error, want not found")
	}
	got, err := gs.GetWithHistory([]int{created.ID})
	if err != nil {
		t.Fatalf("GetWithHistory: %v", err)
	}
	if len(got) != 1 || !got[0].Deleted {
		t.Fatalf("GetWithHistory = %+v, want a single Deleted task", got)
	}
}

// Restore keeps the tombstone's own DependsOn - the edges SoftDelete
// stripped from OTHER tasks stay stripped, but the restored task's own
// list comes back verbatim. This is the documented asymmetry in
// GitStore.Restore, and the reason cmd/md warns about still-deleted deps.
func TestGitStore_Restore_KeepsOwnDepsDropsDependents(t *testing.T) {
	gs, _, _ := newGitStoreRepo(t)
	dep, err := gs.Create(Task{Title: "dependency", Status: "open"})
	if err != nil {
		t.Fatalf("Create dep: %v", err)
	}
	blocked, err := gs.Create(Task{Title: "blocked", Status: "open", DependsOn: []int{dep.ID}})
	if err != nil {
		t.Fatalf("Create blocked: %v", err)
	}

	// Deleting dep strips it from blocked's DependsOn.
	if _, err := gs.SoftDelete(dep.ID); err != nil {
		t.Fatalf("SoftDelete dep: %v", err)
	}
	// Deleting blocked keeps blocked's own (now empty) list.
	if _, err := gs.SoftDelete(blocked.ID); err != nil {
		t.Fatalf("SoftDelete blocked: %v", err)
	}

	if _, err := gs.Restore(dep.ID); err != nil {
		t.Fatalf("Restore dep: %v", err)
	}
	restored, err := gs.Restore(blocked.ID)
	if err != nil {
		t.Fatalf("Restore blocked: %v", err)
	}
	if len(restored.DependsOn) != 0 {
		t.Errorf("restored.DependsOn = %v, want empty: SoftDelete stripped the edge and Restore does not re-add it", restored.DependsOn)
	}
}

// A tombstone whose own DependsOn points at a still-deleted task restores
// anyway - no validateTaskDeps gate - so a set of tombstones can be restored
// in any order.
//
// It also pins down what that dangling edge then DOES, which is the opposite
// of what SoftDelete's doc comment suggests: readyTasks skips a dep that is
// absent from the active set entirely (`exists && depStatus != "closed"`),
// and filterDeleted has already removed the deleted dependency from it. So
// the edge is IGNORED, not blocking - the restored task reads as ready even
// though the thing it says it depends on is not done. cmd/md/restore.go
// warns on exactly this case, and this test is what its wording is checked
// against.
func TestGitStore_Restore_AllowsStillDeletedDependency(t *testing.T) {
	gs, _, _ := newGitStoreRepo(t)
	dep, err := gs.Create(Task{Title: "dependency", Status: "open"})
	if err != nil {
		t.Fatalf("Create dep: %v", err)
	}
	blocked, err := gs.Create(Task{Title: "blocked", Status: "open", DependsOn: []int{dep.ID}})
	if err != nil {
		t.Fatalf("Create blocked: %v", err)
	}
	// Delete blocked FIRST, so its DependsOn is preserved on the
	// tombstone, then delete dep - which cannot strip an edge from an
	// already-deleted dependent.
	if _, err := gs.SoftDelete(blocked.ID); err != nil {
		t.Fatalf("SoftDelete blocked: %v", err)
	}
	if _, err := gs.SoftDelete(dep.ID); err != nil {
		t.Fatalf("SoftDelete dep: %v", err)
	}

	restored, err := gs.Restore(blocked.ID)
	if err != nil {
		t.Fatalf("Restore blocked with a still-deleted dependency: %v, want it to succeed so a set restores in any order", err)
	}
	if !slices.Equal(restored.DependsOn, []int{dep.ID}) {
		t.Fatalf("restored.DependsOn = %v, want %v", restored.DependsOn, []int{dep.ID})
	}

	ready, err := gs.Ready()
	if err != nil {
		t.Fatalf("Ready: %v", err)
	}
	if !containsID(ready, blocked.ID) {
		t.Errorf("Ready = %v, want it to include %d: readyTasks ignores a dependency that is absent from the active set, so a still-deleted one does not block", taskIDs(ready), blocked.ID)
	}

	// Restoring the dependency turns the ignored edge back into a real one,
	// and it is open rather than closed - so now it genuinely blocks.
	if _, err := gs.Restore(dep.ID); err != nil {
		t.Fatalf("Restore dep: %v", err)
	}
	ready, err = gs.Ready()
	if err != nil {
		t.Fatalf("Ready after restoring the dependency: %v", err)
	}
	if !containsID(ready, dep.ID) {
		t.Errorf("Ready = %v, want it to include the restored dependency %d", taskIDs(ready), dep.ID)
	}
	if containsID(ready, blocked.ID) {
		t.Errorf("Ready = %v, want %d excluded now that its open dependency %d is active again", taskIDs(ready), blocked.ID, dep.ID)
	}
}

// Restoring an id that has no ref at all is an error, not a silent success
// that would let a typo look like it worked.
func TestGitStore_Restore_UnknownID(t *testing.T) {
	gs, _, _ := newGitStoreRepo(t)
	if _, err := gs.Restore(9999); err == nil {
		t.Errorf("Restore(9999) = nil error, want not found")
	}
}
