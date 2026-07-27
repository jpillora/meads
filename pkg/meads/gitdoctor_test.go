package meads

import (
	"encoding/json"
	"errors"
	"slices"
	"strconv"
	"testing"
)

// Tests for GitStore.Doctor (git mode phase 8, TASKS #65): duplicate/
// mismatched task-id detection and repair over refs/meads/tasks/* (local)
// and refs/meads-remote/tasks/* (the last `git fetch`'s remote-tracking
// copy - see RemoteRefNamespace). Like gitmutate_test.go, these run against
// real temporary git repositories via ExecGit rather than a fake.
//
// Doctor's cross-namespace duplicate case doesn't need a real second clone
// or a real `git fetch` to set up - Doctor only ever cares about the
// CURRENT state of the two namespaces, never how it got there - so these
// tests seed refs/meads-remote/tasks/* directly within one repo via
// seedTaskAtRef below. gitdiverge_test.go's divergence tests, by contrast,
// use real two-clone/bare-remote setups, since what's under test there
// (a real non-fast-forward push, a real safe fetch) is precisely what
// direct seeding would rubber-stamp without exercising.

// --- helpers ---

// remoteTaskRef returns the remote-tracking ref name for id - the fetched-
// but-not-yet-integrated counterpart of gs.TaskRef(id) (RemoteTasksRefPrefix
// vs TasksRefPrefix).
func remoteTaskRef(id int) string {
	return RemoteTasksRefPrefix + strconv.Itoa(id)
}

// seedTaskAtRef JSON-marshals task and commits it as a brand-new root
// commit (prev ZeroOID) directly onto ref - unlike seedTask/commitTaskVersion
// (gitstore_test.go), which always target gs.TaskRef(task.ID), this can
// target ANY ref name, including one that deliberately disagrees with
// task.ID (a content/ref-name mismatch) or a RemoteTasksRefPrefix ref (a
// simulated fetched duplicate). Returns the new commit's oid.
func seedTaskAtRef(t *testing.T, rs *RefStore, ref string, task Task) OID {
	t.Helper()
	data, err := json.Marshal(task)
	if err != nil {
		t.Fatalf("marshaling task for ref %s: %v", ref, err)
	}
	oid, err := rs.CommitFile(ref, TaskFileName, data, ZeroOID, "meads test: seed task at ref")
	if err != nil {
		t.Fatalf("committing task at ref %s: %v", ref, err)
	}
	return oid
}

// commitTaskWithParent JSON-marshals task and writes it as a new commit
// parented on parent (a DIFFERENT ref's tip - a plain commitTaskVersion
// can't express this, since its prev argument doubles as both the new
// commit's parent AND the ref's own CAS-expected-previous-value, which are
// the same oid only when extending that same ref), then CAS-updates ref
// from refPrev to the new commit. Used to build a remote-tracking ref
// (refPrev ZeroOID: it doesn't exist yet) whose first commit nonetheless
// shares history with an existing local ref, simulating what a real `git
// fetch` produces when the remote is a fast-forward or a divergence of
// local state rather than an unrelated duplicate.
func commitTaskWithParent(t *testing.T, rs *RefStore, ref string, task Task, parent, refPrev OID) OID {
	t.Helper()
	data, err := json.Marshal(task)
	if err != nil {
		t.Fatalf("marshaling task for ref %s: %v", ref, err)
	}
	blob, err := rs.WriteBlob(data)
	if err != nil {
		t.Fatalf("WriteBlob: %v", err)
	}
	tree, err := rs.WriteTree([]TreeEntry{{Mode: "100644", Type: "blob", OID: blob, Name: TaskFileName}})
	if err != nil {
		t.Fatalf("WriteTree: %v", err)
	}
	var parents []OID
	if parent != ZeroOID {
		parents = []OID{parent}
	}
	commit, err := rs.WriteCommit(tree, parents, "meads test: seed related task")
	if err != nil {
		t.Fatalf("WriteCommit: %v", err)
	}
	if err := rs.CompareAndSwap(ref, commit, refPrev); err != nil {
		t.Fatalf("CompareAndSwap(%s): %v", ref, err)
	}
	return commit
}

// --- 1. clean repo: no fixes, no writes ---

func TestGitStore_Doctor_NoIssues_NoFixesNoWrites(t *testing.T) {
	gs, rs, _ := newGitStoreRepo(t)
	seedTask(t, rs, gs, Task{ID: 1, Title: "one", Status: "open"})
	seedTask(t, rs, gs, Task{ID: 2, Title: "two", Status: "open", DependsOn: []int{1}})

	before, err := rs.ListRefs(TasksRefPrefix)
	if err != nil {
		t.Fatalf("ListRefs before: %v", err)
	}

	fixes, err := gs.Doctor()
	if err != nil {
		t.Fatalf("Doctor: %v", err)
	}
	if len(fixes) != 0 {
		t.Fatalf("Doctor() on a clean repo = %v, want no fixes", fixes)
	}

	after, err := rs.ListRefs(TasksRefPrefix)
	if err != nil {
		t.Fatalf("ListRefs after: %v", err)
	}
	if len(after) != len(before) {
		t.Fatalf("ref count changed from %d to %d, want unchanged (Doctor must not write anything when there is nothing to fix)", len(before), len(after))
	}
	for name, oid := range before {
		if after[name] != oid {
			t.Errorf("ref %s moved from %s to %s, want unchanged", name, oid, after[name])
		}
	}
}

// --- 2. content/ref-name mismatch: repaired in place, same id ---

func TestGitStore_Doctor_ContentIDMismatch_RepairedInPlace(t *testing.T) {
	gs, rs, _ := newGitStoreRepo(t)
	// Ref name says 5; stored content wrongly says 99.
	seedTaskAtRef(t, rs, gs.TaskRef(5), Task{ID: 99, Title: "mismatched", Status: "open"})

	fixes, err := gs.Doctor()
	if err != nil {
		t.Fatalf("Doctor: %v", err)
	}
	if len(fixes) != 1 || fixes[0].OldID != 5 || fixes[0].NewID != 5 {
		t.Fatalf("Doctor() fixes = %v, want exactly one {OldID:5 NewID:5} (a repair, not a renumber)", fixes)
	}

	got, err := gs.Get([]int{5})
	if err != nil {
		t.Fatalf("Get(5) after repair: %v", err)
	}
	if len(got) != 1 || got[0].ID != 5 || got[0].Title != "mismatched" {
		t.Fatalf("Get(5) after repair = %+v, want id=5 (content corrected), title preserved", got)
	}

	// The ref name never changed - id 99 must not exist as a ref at all.
	if _, err := rs.ResolveRef(gs.TaskRef(99)); !errors.Is(err, ErrRefNotFound) {
		t.Errorf("ResolveRef(TaskRef(99)) = %v, want ErrRefNotFound (a mismatch repair must not create a new ref)", err)
	}
}

func TestGitStore_Doctor_NoMismatch_NotReported(t *testing.T) {
	gs, rs, _ := newGitStoreRepo(t)
	seedTask(t, rs, gs, Task{ID: 7, Title: "consistent", Status: "open"})

	fixes, err := gs.Doctor()
	if err != nil {
		t.Fatalf("Doctor: %v", err)
	}
	if len(fixes) != 0 {
		t.Fatalf("Doctor() on a consistent task = %v, want no fixes", fixes)
	}
}

// --- 3. cross-namespace duplicate: remote keeps the id, local re-homed at a fresh id ---

func TestGitStore_Doctor_CrossNamespaceDuplicate_LocalReHomedAtFreshID(t *testing.T) {
	gs, rs, _ := newGitStoreRepo(t)
	seedTaskAtRef(t, rs, gs.TaskRef(58), Task{ID: 58, Title: "local version", Status: "open"})
	remoteOID := seedTaskAtRef(t, rs, remoteTaskRef(58), Task{ID: 58, Title: "kept (remote)", Status: "open"})

	fixes, err := gs.Doctor()
	if err != nil {
		t.Fatalf("Doctor: %v", err)
	}
	if len(fixes) != 1 || fixes[0].OldID != 58 || fixes[0].NewID != 59 {
		t.Fatalf("Doctor() fixes = %v, want exactly one {OldID:58 NewID:59}", fixes)
	}
	if fixes[0].Kind != DoctorFixDuplicate {
		t.Errorf("fix Kind = %v, want DoctorFixDuplicate", fixes[0].Kind)
	}

	// The contended ref takes the fetched-remote version - the exact reset
	// that lets the next push converge with no force-push.
	if oid, err := rs.ResolveRef(gs.TaskRef(58)); err != nil || oid != remoteOID {
		t.Errorf("task 58 ref = %v (err=%v), want reset to the remote-tracking oid %s", oid, err, remoteOID)
	}
	kept, err := gs.Get([]int{58})
	if err != nil || kept[0].Title != "kept (remote)" {
		t.Fatalf("Get(58) = %+v (err=%v), want the remote version (the id follows origin)", kept, err)
	}

	// The local version is re-homed as a new task at 59, content preserved.
	moved, err := gs.Get([]int{59})
	if err != nil {
		t.Fatalf("Get(59) after Doctor: %v", err)
	}
	if len(moved) != 1 || moved[0].ID != 59 || moved[0].Title != "local version" {
		t.Fatalf("Get(59) = %+v, want the re-homed local version with id=59", moved)
	}

	// refs/meads-remote/* itself is read-only input, never written by Doctor.
	remoteRefs, err := rs.ListRefs(RemoteTasksRefPrefix)
	if err != nil {
		t.Fatalf("ListRefs(RemoteTasksRefPrefix): %v", err)
	}
	if len(remoteRefs) != 1 {
		t.Fatalf("remote-tracking refs = %v, want exactly the original one, untouched by Doctor", remoteRefs)
	}
}

// TestGitStore_Doctor_FreshIDAvoidsUninvolvedRemoteIDsToo guards a subtler
// bug: a fresh id must never be picked from a number remote-tracking ALSO
// happens to be using, even for a task that isn't colliding with anything
// local (and so this run correctly leaves it alone entirely - adopting it
// is Integrate's job, not a repair). If freshID only avoided LOCAL ids,
// re-homing the actual duplicate here (58) could land on id 59 - which is
// "free" locally but already means something else on remote-tracking -
// manufacturing a BRAND NEW collision this very run wouldn't itself notice
// or fix.
func TestGitStore_Doctor_FreshIDAvoidsUninvolvedRemoteIDsToo(t *testing.T) {
	gs, rs, _ := newGitStoreRepo(t)
	seedTaskAtRef(t, rs, gs.TaskRef(58), Task{ID: 58, Title: "local version", Status: "open"})
	seedTaskAtRef(t, rs, remoteTaskRef(58), Task{ID: 58, Title: "kept (remote)", Status: "open"})
	// Present only on remote-tracking, not colliding with anything local -
	// correctly out of scope for this run, but its NUMBER must still not be
	// handed out as a "fresh" id.
	seedTaskAtRef(t, rs, remoteTaskRef(59), Task{ID: 59, Title: "remote-only, uninvolved", Status: "open"})

	fixes, err := gs.Doctor()
	if err != nil {
		t.Fatalf("Doctor: %v", err)
	}
	if len(fixes) != 1 || fixes[0].OldID != 58 {
		t.Fatalf("Doctor() fixes = %v, want exactly one duplicate fix for id 58", fixes)
	}
	if fixes[0].NewID == 59 {
		t.Fatalf("Doctor() assigned fresh id 59, which remote-tracking is ALREADY using for an unrelated task - want it skipped")
	}

	moved, err := gs.Get([]int{fixes[0].NewID})
	if err != nil || moved[0].Title != "local version" {
		t.Fatalf("Get(%d) = %+v (err=%v), want the re-homed local version", fixes[0].NewID, moved, err)
	}
	// The uninvolved remote-only task must still be left alone: no local
	// ref exists for it at all (Doctor never adopts), and its
	// remote-tracking ref is untouched.
	if _, err := gs.Get([]int{59}); err == nil {
		t.Fatalf("Get(59) after Doctor unexpectedly succeeded; task 59 should not exist locally")
	}
	remote59, _, err := gs.loadAllWithOIDs(RemoteTasksRefPrefix)
	if err != nil {
		t.Fatalf("loadAllWithOIDs(remote): %v", err)
	}
	if remote59[59].Title != "remote-only, uninvolved" {
		t.Fatalf("remote-tracking task 59 = %+v, want it untouched", remote59[59])
	}
}

// --- 4. DependsOn rewritten for sibling local tasks re-homed in the same run ---

func TestGitStore_Doctor_DependsOnRewrittenForSiblingDuplicates(t *testing.T) {
	gs, rs, _ := newGitStoreRepo(t)
	// Local created 58 and a 60 that depends on it; remote-tracking
	// independently created its own 58 and 60. Both local tasks are
	// re-homed; processed in ascending id order, so by the time 60's move is
	// planned, the remap from 58's move is already known (see
	// planDoctorFixes's doc comment on why this ordering is what makes the
	// remap visible, mirroring the file backend's identical ordering
	// assumption in mutate.go).
	seedTaskAtRef(t, rs, gs.TaskRef(58), Task{ID: 58, Title: "local A", Status: "open"})
	seedTaskAtRef(t, rs, gs.TaskRef(60), Task{ID: 60, Title: "local C", Status: "open", DependsOn: []int{58}})
	seedTaskAtRef(t, rs, remoteTaskRef(58), Task{ID: 58, Title: "remote B", Status: "open"})
	seedTaskAtRef(t, rs, remoteTaskRef(60), Task{ID: 60, Title: "remote D", Status: "open", DependsOn: []int{58}})

	fixes, err := gs.Doctor()
	if err != nil {
		t.Fatalf("Doctor: %v", err)
	}
	if len(fixes) != 2 {
		t.Fatalf("Doctor() fixes = %v, want exactly 2", fixes)
	}
	remap := make(map[int]int, len(fixes))
	for _, f := range fixes {
		if f.OldID != 58 && f.OldID != 60 {
			t.Fatalf("unexpected fix %+v", f)
		}
		remap[f.OldID] = f.NewID
	}

	all, err := gs.Get(nil)
	if err != nil {
		t.Fatalf("Get(nil): %v", err)
	}
	byTitle := make(map[string]Task, len(all))
	for _, task := range all {
		byTitle[task.Title] = task
	}

	// The remote versions keep the ids.
	remoteB, remoteD := byTitle["remote B"], byTitle["remote D"]
	if remoteB.ID != 58 || remoteD.ID != 60 {
		t.Fatalf("remote tasks renumbered (B=%d D=%d), want unchanged (58, 60)", remoteB.ID, remoteD.ID)
	}
	// Remote D's dependency was always on the remote's own 58, which never
	// moved - untouched.
	if want := []int{58}; !slices.Equal(remoteD.DependsOn, want) {
		t.Errorf("remote D DependsOn = %v, want %v (its own sibling never moved)", remoteD.DependsOn, want)
	}

	// The local versions move to fresh ids...
	localA, localC := byTitle["local A"], byTitle["local C"]
	if localA.ID == 58 || localA.ID == 60 {
		t.Fatalf("local A kept a contended id %d, want a fresh one", localA.ID)
	}
	if localC.ID == 58 || localC.ID == 60 {
		t.Fatalf("local C kept a contended id %d, want a fresh one", localC.ID)
	}
	// ...and the crux: local C's DependsOn must follow local A to its NEW
	// id, not stay pointing at 58 (which now unambiguously means remote B).
	if want := []int{localA.ID}; !slices.Equal(localC.DependsOn, want) {
		t.Errorf("local C DependsOn = %v, want %v (remapped to local A's new id, not remote B's 58)", localC.DependsOn, want)
	}
}

// --- 5. soft-deleted ids: never reused as a fresh id, never clobbered ---

func TestGitStore_Doctor_SoftDeletedIDs_NeverReusedNeverClobbered(t *testing.T) {
	gs, rs, _ := newGitStoreRepo(t)
	deletedOID := seedTaskAtRef(t, rs, gs.TaskRef(3), Task{ID: 3, Title: "long gone", Status: "closed", Deleted: true})
	seedTaskAtRef(t, rs, gs.TaskRef(5), Task{ID: 5, Title: "local version", Status: "open"})
	seedTaskAtRef(t, rs, remoteTaskRef(5), Task{ID: 5, Title: "duplicate", Status: "open"})

	fixes, err := gs.Doctor()
	if err != nil {
		t.Fatalf("Doctor: %v", err)
	}
	if len(fixes) != 1 || fixes[0].NewID != 6 {
		t.Fatalf("Doctor() fixes = %v, want the local version re-homed to 6 (3 is deleted but still allocated, so max is 5)", fixes)
	}

	// The deleted task's ref must be completely untouched.
	if oid, err := rs.ResolveRef(gs.TaskRef(3)); err != nil || oid != deletedOID {
		t.Errorf("deleted task 3 ref = %v (err=%v), want unchanged %s", oid, err, deletedOID)
	}
	gone, err := gs.GetWithHistory([]int{3})
	if err != nil || len(gone) != 1 || !gone[0].Deleted || gone[0].Title != "long gone" {
		t.Fatalf("GetWithHistory(3) = %+v (err=%v), want the original deleted task, untouched", gone, err)
	}
}

func TestGitStore_Doctor_ReHomedDuplicate_PreservesDeletedFlag(t *testing.T) {
	gs, rs, _ := newGitStoreRepo(t)
	// The LOCAL side of the collision is the soft-deleted one: re-homing it
	// must carry the tombstone over, not resurrect it.
	seedTaskAtRef(t, rs, gs.TaskRef(5), Task{ID: 5, Title: "local but deleted", Status: "closed", Deleted: true})
	seedTaskAtRef(t, rs, remoteTaskRef(5), Task{ID: 5, Title: "kept active (remote)", Status: "open"})

	fixes, err := gs.Doctor()
	if err != nil {
		t.Fatalf("Doctor: %v", err)
	}
	if len(fixes) != 1 {
		t.Fatalf("Doctor() fixes = %v, want exactly 1", fixes)
	}
	newID := fixes[0].NewID

	moved, err := gs.GetWithHistory([]int{newID})
	if err != nil {
		t.Fatalf("GetWithHistory(%d): %v", newID, err)
	}
	if len(moved) != 1 || !moved[0].Deleted || moved[0].Title != "local but deleted" {
		t.Fatalf("re-homed duplicate = %+v, want Deleted=true preserved", moved)
	}
	// And it must never resurface via Get (deleted tasks are excluded there).
	if _, err := gs.Get([]int{newID}); err == nil {
		t.Errorf("Get(%d) on the re-homed-but-deleted duplicate = nil error, want not found", newID)
	}
}

// --- 6. related histories: fast-forwards are Integrate's job (no fix);
// genuine divergences are re-homed like duplicates, with a distinct Kind ---

func TestGitStore_Doctor_FastForwardRelated_NotADuplicate(t *testing.T) {
	gs, rs, _ := newGitStoreRepo(t)
	localOID := seedTaskAtRef(t, rs, gs.TaskRef(9), Task{ID: 9, Title: "v1", Status: "open"})
	// Remote-tracking is a further version of the SAME task (parented on
	// localOID), not an independently created one - a plain fast-forward,
	// which Integrate (not Doctor) applies, even though both exist under
	// the same id.
	commitTaskWithParent(t, rs, remoteTaskRef(9), Task{ID: 9, Title: "v2 (remote ahead)", Status: "open"}, localOID, ZeroOID)

	fixes, err := gs.Doctor()
	if err != nil {
		t.Fatalf("Doctor: %v", err)
	}
	if len(fixes) != 0 {
		t.Fatalf("Doctor() on a fast-forward-related pair = %v, want no fixes (Integrate's job, not a repair)", fixes)
	}
	if oid, err := rs.ResolveRef(gs.TaskRef(9)); err != nil || oid != localOID {
		t.Errorf("local task 9 ref = %v (err=%v), want unchanged %s", oid, err, localOID)
	}
}

// TestGitStore_Doctor_GenuineDivergence_ReHomed pins task 86's convergent
// divergence policy: both sides edited the SAME task since a shared base,
// so the remote version keeps the id and the local edit is re-homed at a
// fresh id - reported with Kind DoctorFixDiverged, distinct from a
// create/create duplicate.
func TestGitStore_Doctor_GenuineDivergence_ReHomed(t *testing.T) {
	gs, rs, _ := newGitStoreRepo(t)
	baseOID := seedTaskAtRef(t, rs, gs.TaskRef(9), Task{ID: 9, Title: "base", Status: "open"})

	// Local moves on from the shared base...
	localData, err := json.Marshal(Task{ID: 9, Title: "local edit", Status: "open"})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if _, err := rs.CommitFile(gs.TaskRef(9), TaskFileName, localData, baseOID, "local: edit"); err != nil {
		t.Fatalf("CommitFile local edit: %v", err)
	}

	// ...and so does the fetched remote-tracking copy, independently.
	remoteOID := commitTaskWithParent(t, rs, remoteTaskRef(9), Task{ID: 9, Title: "remote edit", Status: "open"}, baseOID, ZeroOID)

	fixes, err := gs.Doctor()
	if err != nil {
		t.Fatalf("Doctor: %v", err)
	}
	if len(fixes) != 1 || fixes[0].OldID != 9 || fixes[0].NewID != 10 {
		t.Fatalf("Doctor() fixes = %v, want exactly one {OldID:9 NewID:10}", fixes)
	}
	if fixes[0].Kind != DoctorFixDiverged {
		t.Errorf("fix Kind = %v, want DoctorFixDiverged", fixes[0].Kind)
	}

	// The id takes the remote version; the local edit is preserved at 10.
	if oid, err := rs.ResolveRef(gs.TaskRef(9)); err != nil || oid != remoteOID {
		t.Errorf("task 9 ref = %v (err=%v), want reset to the remote-tracking oid %s", oid, err, remoteOID)
	}
	kept, err := gs.Get([]int{9})
	if err != nil || kept[0].Title != "remote edit" {
		t.Fatalf("Get(9) = %+v (err=%v), want the remote version", kept, err)
	}
	moved, err := gs.Get([]int{10})
	if err != nil || len(moved) != 1 || moved[0].Title != "local edit" {
		t.Fatalf("Get(10) = %+v (err=%v), want the re-homed local edit", moved, err)
	}
	// And the divergence is fully resolved: Diverged must find nothing.
	if dv, err := gs.Diverged(); err != nil || len(dv) != 0 {
		t.Errorf("Diverged() after Doctor = %v, %v; want none (the re-homing resolved it)", dv, err)
	}
}

// --- 7. atomicity: a batch with one stale ref moves NOTHING ---

func TestGitStore_Doctor_AtomicBatchLeavesNothingPartiallyMoved(t *testing.T) {
	gs, rs, _ := newGitStoreRepo(t)
	// Fix A: a content/ref-name mismatch on an EXISTING ref (prev = its real oid).
	mismatchOID := seedTaskAtRef(t, rs, gs.TaskRef(1), Task{ID: 99, Title: "mismatched", Status: "open"})
	// Fix B: a cross-namespace duplicate, re-homed onto a brand-new ref
	// (prev = ZeroOID) plus a reset of the contended ref itself (prev = the
	// local tip): two updates for one fix.
	local58OID := seedTaskAtRef(t, rs, gs.TaskRef(58), Task{ID: 58, Title: "local version", Status: "open"})
	seedTaskAtRef(t, rs, remoteTaskRef(58), Task{ID: 58, Title: "duplicate", Status: "open"})

	local, localOIDs, err := gs.loadAllWithOIDs(TasksRefPrefix)
	if err != nil {
		t.Fatalf("loadAllWithOIDs(local): %v", err)
	}
	remote, remoteOIDs, err := gs.loadAllWithOIDs(RemoteTasksRefPrefix)
	if err != nil {
		t.Fatalf("loadAllWithOIDs(remote): %v", err)
	}
	fixes, updates, err := gs.planDoctorFixes(local, localOIDs, remote, remoteOIDs)
	if err != nil {
		t.Fatalf("planDoctorFixes: %v", err)
	}
	if len(fixes) != 2 || len(updates) != 3 {
		t.Fatalf("planDoctorFixes fixes=%v updates=%v, want 2 fixes and 3 updates (mismatch + re-home + reset)", fixes, updates)
	}

	// Simulate a concurrent writer landing on the MISMATCH fix's target ref
	// between the read above and the batch write below - exactly the race
	// Doctor's own retry loop must survive (planDoctorFixes is the "read,
	// then decide" half; submitting the resulting batch directly here, the
	// way TestGitStore_SoftDelete_AtomicBatchLeavesNothingPartiallyMoved
	// reuses buildTaskVersion, lets a single failed AtomicUpdate call be
	// inspected in isolation rather than masked by Doctor's retry).
	racingOID := commitTaskVersion(t, rs, gs, Task{ID: 1, Title: "raced in", Status: "open"}, mismatchOID)

	err = rs.AtomicUpdate(updates)
	if err == nil {
		t.Fatal("AtomicUpdate with one stale prev = nil error, want ErrCASConflict")
	}
	if !errors.Is(err, ErrCASConflict) {
		t.Errorf("err = %v, want errors.Is(err, ErrCASConflict)", err)
	}

	// The mismatch-fix ref moved only because of the race, never because of
	// Doctor's own (failed) attempt.
	if got, rerr := rs.ResolveRef(gs.TaskRef(1)); rerr != nil || got != racingOID {
		t.Errorf("task 1 ref = %v (err=%v), want the racing writer's commit %s", got, rerr, racingOID)
	}

	// The crux of atomicity: fix B's new ref (59) had a perfectly valid prev
	// (ZeroOID: it genuinely didn't exist yet) in isolation, but it shares a
	// batch with fix A, which failed - it must not have been created at all.
	var newID int
	for _, f := range fixes {
		if f.OldID == 58 {
			newID = f.NewID
		}
	}
	if newID == 0 {
		t.Fatalf("planDoctorFixes did not include the id-58 duplicate: %v", fixes)
	}
	if _, rerr := rs.ResolveRef(gs.TaskRef(newID)); !errors.Is(rerr, ErrRefNotFound) {
		t.Errorf("ResolveRef(TaskRef(%d)) = %v, want ErrRefNotFound (a valid-in-isolation new ref must not be created when its batch-mate fails)", newID, rerr)
	}
	// And the contended ref itself must NOT have been reset either: it
	// still holds the local version's commit, not the remote's.
	if got, rerr := rs.ResolveRef(gs.TaskRef(58)); rerr != nil || got != local58OID {
		t.Errorf("task 58 ref = %v (err=%v), want the original local commit %s (no partial reset)", got, rerr, local58OID)
	}
}
