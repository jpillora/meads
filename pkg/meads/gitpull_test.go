package meads

import (
	"fmt"
	"slices"
	"testing"
)

// Tests for GitStore.Integrate and GitStore.Pull (gitpull.go, task 86):
// adopting remote-only ids, fast-forwarding unmoved ones (including
// deletions), leaving local-ahead ids alone, handing the contended
// remainder to Doctor's convergent renumbering, and the config ref's
// adopt/fast-forward. The remote-tracking namespace is seeded directly
// (seedTaskAtRef/commitTaskWithParent - see gitdoctor_test.go for why
// direct seeding suffices); Pull's fetch half gets a real two-clone test
// with a bare origin.

func TestGitStore_Integrate_AdoptsRemoteOnlyIDs(t *testing.T) {
	gs, rs, _ := newGitStoreRepo(t)
	seedTaskAtRef(t, rs, gs.TaskRef(1), Task{ID: 1, Title: "local", Status: "open"})
	remoteOID2 := seedTaskAtRef(t, rs, remoteTaskRef(2), Task{ID: 2, Title: "from origin", Status: "open"})
	remoteOID3 := seedTaskAtRef(t, rs, remoteTaskRef(3), Task{ID: 3, Title: "also from origin", Status: "open"})

	report, err := gs.Integrate()
	if err != nil {
		t.Fatalf("Integrate: %v", err)
	}
	if !slices.Equal(report.Imported, []int{2, 3}) {
		t.Errorf("Imported = %v, want [2 3]", report.Imported)
	}
	if len(report.FastForwarded) != 0 || len(report.Fixes) != 0 {
		t.Errorf("FastForwarded/Fixes = %v/%v, want none", report.FastForwarded, report.Fixes)
	}
	// The adopted refs point at the very same commits (history comes free).
	for id, want := range map[int]OID{2: remoteOID2, 3: remoteOID3} {
		if oid, err := rs.ResolveRef(gs.TaskRef(id)); err != nil || oid != want {
			t.Errorf("task %d ref = %v (err=%v), want the remote-tracking commit %s", id, oid, err, want)
		}
	}
	got, err := gs.Get(nil)
	if err != nil || len(got) != 3 {
		t.Fatalf("Get(nil) after Integrate = %v, %v; want all 3 tasks", got, err)
	}
	// Remote-tracking itself is untouched (read-only input).
	if refs, _ := rs.ListRefs(RemoteTasksRefPrefix); len(refs) != 2 {
		t.Errorf("remote-tracking refs = %v, want the original 2, untouched", refs)
	}
}

func TestGitStore_Integrate_FastForwardsUnmovedTasks(t *testing.T) {
	gs, rs, _ := newGitStoreRepo(t)
	baseOID := seedTaskAtRef(t, rs, gs.TaskRef(9), Task{ID: 9, Title: "v1", Status: "open"})
	remoteOID := commitTaskWithParent(t, rs, remoteTaskRef(9), Task{ID: 9, Title: "v2 (origin ahead)", Status: "closed"}, baseOID, ZeroOID)

	report, err := gs.Integrate()
	if err != nil {
		t.Fatalf("Integrate: %v", err)
	}
	if !slices.Equal(report.FastForwarded, []int{9}) {
		t.Errorf("FastForwarded = %v, want [9]", report.FastForwarded)
	}
	if oid, err := rs.ResolveRef(gs.TaskRef(9)); err != nil || oid != remoteOID {
		t.Errorf("task 9 ref = %v (err=%v), want fast-forwarded to %s", oid, err, remoteOID)
	}
	got, err := gs.Get([]int{9})
	if err != nil || got[0].Title != "v2 (origin ahead)" || got[0].Status != "closed" {
		t.Fatalf("Get(9) = %+v (err=%v), want the fetched version", got, err)
	}
}

// TestGitStore_Integrate_FastForwardPropagatesDeletions: a soft delete is
// just a commit like any other, so an unmoved local task whose fetched
// counterpart is a tombstone fast-forwards into deletion.
func TestGitStore_Integrate_FastForwardPropagatesDeletions(t *testing.T) {
	gs, rs, _ := newGitStoreRepo(t)
	baseOID := seedTaskAtRef(t, rs, gs.TaskRef(4), Task{ID: 4, Title: "doomed", Status: "open"})
	commitTaskWithParent(t, rs, remoteTaskRef(4), Task{ID: 4, Title: "doomed", Status: "closed", Deleted: true}, baseOID, ZeroOID)

	report, err := gs.Integrate()
	if err != nil {
		t.Fatalf("Integrate: %v", err)
	}
	if !slices.Equal(report.FastForwarded, []int{4}) {
		t.Errorf("FastForwarded = %v, want [4]", report.FastForwarded)
	}
	if _, err := gs.Get([]int{4}); err == nil {
		t.Error("Get(4) after Integrate should not find the task (the fetched tombstone won)")
	}
	gone, err := gs.GetWithHistory([]int{4})
	if err != nil || len(gone) != 1 || !gone[0].Deleted {
		t.Errorf("GetWithHistory(4) = %+v (err=%v), want the fetched tombstone", gone, err)
	}
}

func TestGitStore_Integrate_LeavesLocalAheadAlone(t *testing.T) {
	gs, rs, _ := newGitStoreRepo(t)
	baseOID := seedTaskAtRef(t, rs, remoteTaskRef(9), Task{ID: 9, Title: "v1", Status: "open"})
	// Local is a child of the remote-tracking tip: strictly ahead.
	localOID := commitTaskWithParent(t, rs, gs.TaskRef(9), Task{ID: 9, Title: "v2 (local ahead)", Status: "open"}, baseOID, ZeroOID)

	report, err := gs.Integrate()
	if err != nil {
		t.Fatalf("Integrate: %v", err)
	}
	if !report.empty() {
		t.Errorf("report = %+v, want empty (local-ahead is the push's job, not the pull's)", report)
	}
	if oid, err := rs.ResolveRef(gs.TaskRef(9)); err != nil || oid != localOID {
		t.Errorf("task 9 ref = %v (err=%v), want unchanged %s", oid, err, localOID)
	}
}

// TestGitStore_Integrate_ContentionReHomedByDoctor: the full split - an
// adopt, a fast-forward, and a create/create duplicate - lands in one
// Integrate, with the duplicate's local version re-homed and the contended
// ref reset to the fetched version.
func TestGitStore_Integrate_ContentionReHomedByDoctor(t *testing.T) {
	gs, rs, _ := newGitStoreRepo(t)
	// Local: task 1 (unmoved since base), task 2 (collides with remote's 2).
	baseOID := seedTaskAtRef(t, rs, gs.TaskRef(1), Task{ID: 1, Title: "shared", Status: "open"})
	seedTaskAtRef(t, rs, gs.TaskRef(2), Task{ID: 2, Title: "local's task", Status: "open"})
	// Remote-tracking: task 1 advanced, task 2 unrelated, task 3 new.
	commitTaskWithParent(t, rs, remoteTaskRef(1), Task{ID: 1, Title: "shared (edited on origin)", Status: "open"}, baseOID, ZeroOID)
	remoteOID2 := seedTaskAtRef(t, rs, remoteTaskRef(2), Task{ID: 2, Title: "origin's task", Status: "open"})
	seedTaskAtRef(t, rs, remoteTaskRef(3), Task{ID: 3, Title: "new on origin", Status: "open"})

	report, err := gs.Integrate()
	if err != nil {
		t.Fatalf("Integrate: %v", err)
	}
	if !slices.Equal(report.FastForwarded, []int{1}) {
		t.Errorf("FastForwarded = %v, want [1]", report.FastForwarded)
	}
	if !slices.Equal(report.Imported, []int{3}) {
		t.Errorf("Imported = %v, want [3]", report.Imported)
	}
	if len(report.Fixes) != 1 || report.Fixes[0].OldID != 2 || report.Fixes[0].NewID != 4 || report.Fixes[0].Kind != DoctorFixDuplicate {
		t.Fatalf("Fixes = %+v, want exactly one {OldID:2 NewID:4 Kind:duplicate}", report.Fixes)
	}
	// The contended ref holds origin's task; the local one moved to 4.
	if oid, err := rs.ResolveRef(gs.TaskRef(2)); err != nil || oid != remoteOID2 {
		t.Errorf("task 2 ref = %v (err=%v), want reset to %s", oid, err, remoteOID2)
	}
	got, err := gs.Get(nil)
	if err != nil || len(got) != 4 {
		t.Fatalf("Get(nil) = %v, %v; want 4 tasks", got, err)
	}
	byTitle := map[string]int{}
	for _, task := range got {
		byTitle[task.Title] = task.ID
	}
	want := map[string]int{"shared (edited on origin)": 1, "origin's task": 2, "new on origin": 3, "local's task": 4}
	for title, id := range want {
		if byTitle[title] != id {
			t.Errorf("task %q has id %d, want %d (all tasks = %v)", title, byTitle[title], id, got)
		}
	}
}

// TestGitStore_Integrate_Idempotent: running Integrate twice must be a
// no-op the second time - nothing re-adopted, nothing re-renumbered.
func TestGitStore_Integrate_Idempotent(t *testing.T) {
	gs, rs, _ := newGitStoreRepo(t)
	seedTaskAtRef(t, rs, gs.TaskRef(2), Task{ID: 2, Title: "local's task", Status: "open"})
	seedTaskAtRef(t, rs, remoteTaskRef(2), Task{ID: 2, Title: "origin's task", Status: "open"})
	seedTaskAtRef(t, rs, remoteTaskRef(3), Task{ID: 3, Title: "new on origin", Status: "open"})

	if _, err := gs.Integrate(); err != nil {
		t.Fatalf("Integrate (1st): %v", err)
	}
	second, err := gs.Integrate()
	if err != nil {
		t.Fatalf("Integrate (2nd): %v", err)
	}
	if !second.empty() {
		t.Errorf("second Integrate = %+v, want empty (idempotent)", second)
	}
}

func TestGitStore_Integrate_AdoptsConfig(t *testing.T) {
	gs, rs, dir := newGitStoreRepo(t)
	// Seed a remote-tracking config ref the way a fetch would land it: by
	// fetching from a real second repo that has one.
	other := newDetectRepo(t)
	otherGS := NewGitStore(&ExecGit{Dir: other})
	if err := otherGS.SetConfig(Config{PushInterval: "5m"}); err != nil {
		t.Fatalf("SetConfig (other): %v", err)
	}
	runGit(t, dir, "remote", "add", "origin", other)
	runGit(t, dir, "fetch", "origin", "+"+RefNamespace+"*:"+RemoteRefNamespace+"*")

	report, err := gs.Integrate()
	if err != nil {
		t.Fatalf("Integrate: %v", err)
	}
	if !report.ConfigUpdated {
		t.Error("ConfigUpdated = false, want true (config adopted)")
	}
	cfg, err := gs.Config()
	if err != nil || cfg.PushInterval != "5m" {
		t.Errorf("Config() after adopt = %+v, %v; want PushInterval 5m", cfg, err)
	}
	_ = rs
}

// TestGitStore_Pull_TwoCloneRoundTrip is the end-to-end proof of task 86's
// flow with a real fetch: clone2 creates a task while clone1 pushes its own
// first task; clone2's Pull must fetch, re-home clone2's task at a fresh
// id, and leave clone2 able to push cleanly - and clone1's later Pull
// adopts clone2's re-homed task with zero duplication.
func TestGitStore_Pull_TwoCloneRoundTrip(t *testing.T) {
	bare := t.TempDir()
	runGit(t, bare, "init", "--bare", "-b", "main")

	// clone1: init git mode, create task 1, push.
	c1 := newDetectRepo(t)
	gs1 := NewGitStore(&ExecGit{Dir: c1})
	runGit(t, c1, "remote", "add", "origin", bare)
	if _, err := EnsureFetchRefspec(&ExecGit{Dir: c1}); err != nil {
		t.Fatalf("EnsureFetchRefspec (clone1): %v", err)
	}
	if err := gs1.SetConfig(DefaultConfig()); err != nil {
		t.Fatalf("SetConfig (clone1): %v", err)
	}
	if _, err := gs1.Create(Task{Title: "clone1's task", Status: "open"}); err != nil {
		t.Fatalf("Create (clone1): %v", err)
	}
	runGit(t, c1, "push", "origin", RefNamespace+"*:"+RefNamespace+"*")

	// clone2: partitioned from clone1's push (its origin HAS clone1's refs
	// now, but clone2 was initialised before seeing them - simulate the
	// partition by configuring git mode locally without fetching).
	c2 := newDetectRepo(t)
	gs2 := NewGitStore(&ExecGit{Dir: c2})
	runGit(t, c2, "remote", "add", "origin", bare)
	if _, err := EnsureFetchRefspec(&ExecGit{Dir: c2}); err != nil {
		t.Fatalf("EnsureFetchRefspec (clone2): %v", err)
	}
	if err := gs2.SetConfig(DefaultConfig()); err != nil {
		t.Fatalf("SetConfig (clone2): %v", err)
	}
	if _, err := gs2.Create(Task{Title: "clone2's task", Status: "open"}); err != nil {
		t.Fatalf("Create (clone2): %v", err)
	}

	// clone2 pulls: fetch + integrate + re-home.
	report, err := gs2.Pull()
	if err != nil {
		t.Fatalf("Pull (clone2): %v", err)
	}
	if len(report.Fixes) != 1 || report.Fixes[0].OldID != 1 || report.Fixes[0].NewID != 2 {
		t.Fatalf("Fixes = %+v, want exactly one {OldID:1 NewID:2}", report.Fixes)
	}
	got2, err := gs2.Get(nil)
	if err != nil || len(got2) != 2 {
		t.Fatalf("Get(nil) on clone2 = %v, %v; want 2 tasks", got2, err)
	}
	byTitle2 := map[string]int{}
	for _, task := range got2 {
		byTitle2[task.Title] = task.ID
	}
	if byTitle2["clone1's task"] != 1 || byTitle2["clone2's task"] != 2 {
		t.Errorf("clone2 tasks = %v, want clone1's=1 clone2's=2", got2)
	}

	// clone2's push now converges - no non-fast-forward rejection.
	if err := (&ExecGit{Dir: c2}).Run("push", "origin", RefNamespace+"*:"+RefNamespace+"*"); err != nil {
		t.Fatalf("push (clone2) after Pull should succeed cleanly: %v", err)
	}

	// clone1 pulls: adopts clone2's re-homed task, no fixes, no duplication.
	report1, err := gs1.Pull()
	if err != nil {
		t.Fatalf("Pull (clone1): %v", err)
	}
	if !slices.Equal(report1.Imported, []int{2}) || len(report1.Fixes) != 0 {
		t.Errorf("clone1 report = %+v, want Imported [2], no fixes", report1)
	}
	got1, err := gs1.Get(nil)
	if err != nil || len(got1) != 2 {
		t.Fatalf("Get(nil) on clone1 = %v, %v; want 2 tasks", got1, err)
	}
	// And a second pull on clone2 is a no-op (fully converged).
	second, err := gs2.Pull()
	if err != nil {
		t.Fatalf("Pull (clone2, 2nd): %v", err)
	}
	if !second.empty() {
		t.Errorf("clone2's second Pull = %+v, want empty (converged)", second)
	}
}

// conflictOnceGit makes the FIRST `update-ref --stdin` (RefStore.
// AtomicUpdate) fail the way a genuinely lost CAS race does: it moves one
// of the refs the batch expected to create, then errors, so AtomicUpdate's
// conflictError check finds a mismatched prev and reports ErrCASConflict.
// Every later call passes straight through.
type conflictOnceGit struct {
	Git
	fired   bool
	refName string
	refOID  OID
}

func (g *conflictOnceGit) OutputWithInput(stdin string, args ...string) (string, error) {
	if !g.fired && len(args) >= 2 && args[0] == "update-ref" && args[1] == "--stdin" {
		g.fired = true
		if err := g.Git.Run("update-ref", g.refName, string(g.refOID)); err != nil {
			return "", err
		}
		return "", fmt.Errorf("simulated lost race")
	}
	return g.Git.OutputWithInput(stdin, args...)
}

// TestGitStore_Integrate_SucceedsAfterALostRace: losing the CAS race on an
// early attempt and winning on a later one is a SUCCESS. The retry loop
// used to break out to a shared post-loop `if lastErr != nil` check, so the
// error recorded by the lost attempt outlived the attempt that won and a
// completed integration was reported as "exhausted N attempts" - with its
// refs already moved. That silently aborts Sync before its push and
// swallows the re-homing notice the causing command must print.
func TestGitStore_Integrate_SucceedsAfterALostRace(t *testing.T) {
	gs, rs, _ := newGitStoreRepo(t)
	remoteOID2 := seedTaskAtRef(t, rs, remoteTaskRef(2), Task{ID: 2, Title: "from origin", Status: "open"})
	seedTaskAtRef(t, rs, remoteTaskRef(3), Task{ID: 3, Title: "also from origin", Status: "open"})

	gs.refs = NewRefStore(&conflictOnceGit{Git: gs.git, refName: gs.TaskRef(2), refOID: remoteOID2})

	report, err := gs.Integrate()
	if err != nil {
		t.Fatalf("Integrate after one lost race: %v (the retry won; both refs are in place)", err)
	}
	if report == nil {
		t.Fatal("Integrate returned a nil report on success")
	}
	// The winning attempt's report only - not the losing attempt's, and not
	// both concatenated (see planIntegration's fresh-report-per-attempt).
	if !slices.Equal(report.Imported, []int{3}) {
		t.Errorf("Imported = %v, want [3]: task 2 was already local by the winning attempt", report.Imported)
	}
	for _, id := range []int{2, 3} {
		if _, err := rs.ResolveRef(gs.TaskRef(id)); err != nil {
			t.Errorf("task %d ref missing after Integrate: %v", id, err)
		}
	}
	got, err := gs.Get(nil)
	if err != nil || len(got) != 2 {
		t.Fatalf("Get(nil) = %v, %v; want both integrated tasks", got, err)
	}
}
