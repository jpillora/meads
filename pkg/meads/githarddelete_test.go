package meads

import (
	"errors"
	"slices"
	"testing"
)

// HardDelete removes the ref outright: nothing - not Get, not the
// history-inclusive reads, not Restore - can reach the task afterwards.
// This is the whole difference from SoftDelete, which keeps all of them.
func TestGitStore_HardDelete_RemovesRefAndAllHistory(t *testing.T) {
	gs, rs, _ := newGitStoreRepo(t)
	created, err := gs.Create(Task{Title: "erase me", Status: "open"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	erased, err := gs.HardDelete(created.ID)
	if err != nil {
		t.Fatalf("HardDelete: %v", err)
	}
	if erased.Title != "erase me" {
		t.Errorf("HardDelete() returned %+v, want the erased task's record", erased)
	}

	if _, err := rs.ResolveRef(gs.TaskRef(created.ID)); !errors.Is(err, ErrRefNotFound) {
		t.Errorf("ResolveRef after HardDelete = %v, want ErrRefNotFound (the ref is gone, not tombstoned)", err)
	}
	if _, err := gs.Get([]int{created.ID}); err == nil {
		t.Errorf("Get after HardDelete = nil error, want not found")
	}
	if _, err := gs.GetWithHistory([]int{created.ID}); err == nil {
		t.Errorf("GetWithHistory after HardDelete = nil error, want not found: a hard delete leaves no tombstone to find")
	}
	all, err := gs.LoadAll()
	if err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	if containsID(all, created.ID) {
		t.Errorf("LoadAll ids = %v, want %d absent", taskIDs(all), created.ID)
	}
	if _, err := gs.Restore(created.ID); err == nil {
		t.Errorf("Restore after HardDelete = nil error, want not found: a hard delete is unrecoverable")
	}
}

// The documented hazard, pinned down rather than left to the doc comment:
// erasing the highest id lowers NextID, so the next Create reuses that
// number for a different task.
func TestGitStore_HardDelete_HighestIDBecomesReusable(t *testing.T) {
	gs, _, _ := newGitStoreRepo(t)
	first, err := gs.Create(Task{Title: "first", Status: "open"})
	if err != nil {
		t.Fatalf("Create first: %v", err)
	}
	last, err := gs.Create(Task{Title: "last", Status: "open"})
	if err != nil {
		t.Fatalf("Create last: %v", err)
	}
	if last.ID != first.ID+1 {
		t.Fatalf("ids = %d, %d, want consecutive", first.ID, last.ID)
	}

	if _, err := gs.HardDelete(last.ID); err != nil {
		t.Fatalf("HardDelete: %v", err)
	}
	next, err := gs.NextID()
	if err != nil {
		t.Fatalf("NextID: %v", err)
	}
	if next != last.ID {
		t.Errorf("NextID after erasing the highest id = %d, want %d reused", next, last.ID)
	}
	reused, err := gs.Create(Task{Title: "someone else entirely", Status: "open"})
	if err != nil {
		t.Fatalf("Create after HardDelete: %v", err)
	}
	if reused.ID != last.ID {
		t.Errorf("reused.ID = %d, want the erased id %d handed back out", reused.ID, last.ID)
	}
}

// SoftDelete is the contrast: it spends the id forever, so the same
// sequence with a tombstone does NOT reuse it.
func TestGitStore_SoftDelete_HighestIDStaysSpent(t *testing.T) {
	gs, _, _ := newGitStoreRepo(t)
	if _, err := gs.Create(Task{Title: "first", Status: "open"}); err != nil {
		t.Fatalf("Create first: %v", err)
	}
	last, err := gs.Create(Task{Title: "last", Status: "open"})
	if err != nil {
		t.Fatalf("Create last: %v", err)
	}
	if _, err := gs.SoftDelete(last.ID); err != nil {
		t.Fatalf("SoftDelete: %v", err)
	}
	created, err := gs.Create(Task{Title: "next one", Status: "open"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if created.ID == last.ID {
		t.Errorf("created.ID = %d, want a fresh id: a tombstone must keep %d spent", created.ID, last.ID)
	}
}

// Dependents are cleaned in the same transaction, including deleted ones -
// the difference from SoftDelete, which leaves tombstones' edges alone
// because the task they point at still exists. Leaving one here would strand
// a tombstone pointing at a ref that no longer exists.
func TestGitStore_HardDelete_CleansDepsIncludingTombstones(t *testing.T) {
	gs, _, _ := newGitStoreRepo(t)
	dep, err := gs.Create(Task{Title: "dependency", Status: "open"})
	if err != nil {
		t.Fatalf("Create dep: %v", err)
	}
	live, err := gs.Create(Task{Title: "live dependent", Status: "open", DependsOn: []int{dep.ID}})
	if err != nil {
		t.Fatalf("Create live: %v", err)
	}
	tombstoned, err := gs.Create(Task{Title: "deleted dependent", Status: "open", DependsOn: []int{dep.ID}})
	if err != nil {
		t.Fatalf("Create tombstoned: %v", err)
	}
	if _, err := gs.SoftDelete(tombstoned.ID); err != nil {
		t.Fatalf("SoftDelete tombstoned: %v", err)
	}

	if _, err := gs.HardDelete(dep.ID); err != nil {
		t.Fatalf("HardDelete: %v", err)
	}

	got, err := gs.Get([]int{live.ID})
	if err != nil {
		t.Fatalf("Get live: %v", err)
	}
	if len(got[0].DependsOn) != 0 {
		t.Errorf("live dependent DependsOn = %v, want the erased id stripped", got[0].DependsOn)
	}
	withHistory, err := gs.GetWithHistory([]int{tombstoned.ID})
	if err != nil {
		t.Fatalf("GetWithHistory tombstoned: %v", err)
	}
	if len(withHistory[0].DependsOn) != 0 {
		t.Errorf("tombstoned dependent DependsOn = %v, want the erased id stripped there too - it points at a ref that no longer exists", withHistory[0].DependsOn)
	}
}

// A hard delete of a task nothing depends on must not rewrite unrelated
// refs: the batch is the target plus only the dependents that changed.
func TestGitStore_HardDelete_LeavesUnrelatedTasksUntouched(t *testing.T) {
	gs, rs, _ := newGitStoreRepo(t)
	keep, err := gs.Create(Task{Title: "bystander", Status: "open"})
	if err != nil {
		t.Fatalf("Create keep: %v", err)
	}
	doomed, err := gs.Create(Task{Title: "doomed", Status: "open"})
	if err != nil {
		t.Fatalf("Create doomed: %v", err)
	}
	before, err := rs.ResolveRef(gs.TaskRef(keep.ID))
	if err != nil {
		t.Fatalf("ResolveRef: %v", err)
	}

	if _, err := gs.HardDelete(doomed.ID); err != nil {
		t.Fatalf("HardDelete: %v", err)
	}

	after, err := rs.ResolveRef(gs.TaskRef(keep.ID))
	if err != nil {
		t.Fatalf("ResolveRef after: %v", err)
	}
	if after != before {
		t.Errorf("bystander ref moved %s -> %s, want it untouched", before, after)
	}
}

// Hard-deleting a tombstone works too - that is how a soft delete gets
// escalated to a permanent one.
func TestGitStore_HardDelete_OfATombstone(t *testing.T) {
	gs, _, _ := newGitStoreRepo(t)
	created, err := gs.Create(Task{Title: "twice doomed", Status: "open"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := gs.SoftDelete(created.ID); err != nil {
		t.Fatalf("SoftDelete: %v", err)
	}
	erased, err := gs.HardDelete(created.ID)
	if err != nil {
		t.Fatalf("HardDelete of a tombstone: %v", err)
	}
	if !erased.Deleted {
		t.Errorf("HardDelete() returned Deleted = false, want the tombstone's own record")
	}
	if _, err := gs.GetWithHistory([]int{created.ID}); err == nil {
		t.Errorf("GetWithHistory = nil error, want not found")
	}
}

func TestGitStore_HardDelete_UnknownID(t *testing.T) {
	gs, _, _ := newGitStoreRepo(t)
	if _, err := gs.HardDelete(9999); err == nil {
		t.Errorf("HardDelete(9999) = nil error, want not found")
	}
}

// The file backend's counterpart: Delete already drops the row, so what
// HardDelete adds is releasing the "max-id" high-water mark that keeps the
// id spent.
func TestStore_HardDelete_FreesIDForReuse(t *testing.T) {
	s := newTestStore(t, "")
	first, err := s.Add(Task{Title: "first", Status: "open"})
	if err != nil {
		t.Fatalf("Add first: %v", err)
	}
	last, err := s.Add(Task{Title: "last", Status: "open"})
	if err != nil {
		t.Fatalf("Add last: %v", err)
	}

	// A soft delete keeps it spent...
	if err := s.Delete(last); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	afterSoft, err := s.Add(Task{Title: "after soft", Status: "open"})
	if err != nil {
		t.Fatalf("Add after soft delete: %v", err)
	}
	if afterSoft == last {
		t.Fatalf("Add reused id %d after a soft delete, want it kept spent", last)
	}

	// ...a hard delete of the now-highest id releases it.
	if err := s.HardDelete(afterSoft); err != nil {
		t.Fatalf("HardDelete: %v", err)
	}
	reused, err := s.Add(Task{Title: "reused", Status: "open"})
	if err != nil {
		t.Fatalf("Add after hard delete: %v", err)
	}
	if reused != afterSoft {
		t.Errorf("Add = %d, want the erased id %d handed back out", reused, afterSoft)
	}
	_ = first
}

// Erasing the top id must release ONLY that id. File mode keeps a single
// "max-id" mark and pruneTombstones discards it the moment an active task
// outgrows it, so an earlier soft-deleted id can have no surviving trace at
// all - and simply dropping the mark on a force delete would silently hand
// that earlier id out again too.
func TestStore_HardDelete_DoesNotReleaseEarlierDeletedIDs(t *testing.T) {
	s := newTestStore(t, "")
	if _, err := s.Add(Task{Title: "first", Status: "open"}); err != nil {
		t.Fatalf("Add first: %v", err)
	}
	softDeleted, err := s.Add(Task{Title: "soft deleted", Status: "open"})
	if err != nil {
		t.Fatalf("Add second: %v", err)
	}
	if err := s.Delete(softDeleted); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	// Adding past the tombstone is what makes pruneTombstones drop its
	// high-water mark, leaving softDeleted with nothing recording it.
	top, err := s.Add(Task{Title: "top", Status: "open"})
	if err != nil {
		t.Fatalf("Add top: %v", err)
	}
	if err := s.HardDelete(top); err != nil {
		t.Fatalf("HardDelete: %v", err)
	}

	next, err := s.Add(Task{Title: "next", Status: "open"})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if next == softDeleted {
		t.Errorf("Add = %d, want the erased id %d: erasing the top id must not also release the earlier soft-deleted %d", next, top, softDeleted)
	}
	if next != top {
		t.Errorf("Add = %d, want the erased id %d handed back out", next, top)
	}
}

// Force-deleting a middling id must not release some OTHER id's high-water
// mark - the reuse hazard has to stay scoped to the id actually erased.
func TestStore_HardDelete_KeepsHigherMarkIntact(t *testing.T) {
	s := newTestStore(t, "")
	low, err := s.Add(Task{Title: "low", Status: "open"})
	if err != nil {
		t.Fatalf("Add low: %v", err)
	}
	high, err := s.Add(Task{Title: "high", Status: "open"})
	if err != nil {
		t.Fatalf("Add high: %v", err)
	}
	// Soft-delete the top id so "max-id" records it, then hard-delete the
	// lower one.
	if err := s.Delete(high); err != nil {
		t.Fatalf("Delete high: %v", err)
	}
	if err := s.HardDelete(low); err != nil {
		t.Fatalf("HardDelete low: %v", err)
	}
	next, err := s.Add(Task{Title: "next", Status: "open"})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if next <= high {
		t.Errorf("Add = %d, want > %d: erasing %d must not release %d's high-water mark", next, high, low, high)
	}
	if slices.Contains([]int{low, high}, next) {
		t.Errorf("Add reused %d", next)
	}
}
