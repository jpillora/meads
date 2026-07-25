package meads

import (
	"errors"
	"slices"
	"sync"
	"testing"
)

// Tests for GitStore's write path (Create, Update, SoftDelete, Claim) over
// refs/meads/tasks/* (git mode phase 3, TASKS #60). Like gitstore_test.go,
// these run against real temporary git repositories via ExecGit rather than
// a fake, since the guarantee under test - single-ref compare-and-swap
// actually being atomic under real concurrent goroutines hammering real git
// plumbing - is precisely the kind of thing a fake would rubber-stamp
// without ever exercising.
//
// The most important tests here (the *Concurrent* ones) exist to catch the
// "retry trap" documented on task 60: a CAS retry loop that re-issues the
// same stale write instead of re-reading and re-deciding. That bug is
// invisible on a single run - it only shows up when two callers actually
// overlap - so those tests use a close(start) barrier to force genuine
// concurrency and repeat many times so a bug that only manifests
// occasionally can't pass by luck.

// --- helpers ---

// claimAttempt captures one goroutine's outcome from a concurrent Claim
// call, keyed by which agent made it.
type claimAttempt struct {
	agentID string
	task    Task
	err     error
}

// runConcurrentClaims starts two goroutines racing Claim on the same task id
// behind a close(start) barrier, so both calls genuinely overlap rather than
// serializing through goroutine-scheduling luck. Each goroutine writes only
// to its own result value, so this is race-detector clean despite having no
// lock around the results.
func runConcurrentClaims(gs *GitStore, id int, agentA, agentB string) (a, b claimAttempt) {
	start := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(2)
	run := func(agentID string, out *claimAttempt) {
		defer wg.Done()
		<-start
		task, err := gs.Claim(id, agentID, nil)
		*out = claimAttempt{agentID: agentID, task: task, err: err}
	}
	go run(agentA, &a)
	go run(agentB, &b)
	close(start)
	wg.Wait()
	return a, b
}

// --- 1. Create assigns sequential ids ---

func TestGitStore_Create_AssignsSequentialIDs(t *testing.T) {
	gs, _, _ := newGitStoreRepo(t)

	var ids []int
	for i := 0; i < 3; i++ {
		created, err := gs.Create(Task{Title: "created task", Status: "open"})
		if err != nil {
			t.Fatalf("Create #%d: %v", i, err)
		}
		ids = append(ids, created.ID)
	}
	want := []int{1, 2, 3}
	if !slices.Equal(ids, want) {
		t.Fatalf("created ids = %v, want %v", ids, want)
	}
	for _, id := range ids {
		got, err := gs.Get([]int{id})
		if err != nil {
			t.Fatalf("Get(%d): %v", id, err)
		}
		if len(got) != 1 || got[0].ID != id {
			t.Fatalf("Get(%d) = %+v, want a single task with that id", id, got)
		}
	}
}

// --- 2. Create after a soft delete does not reuse the id ---

func TestGitStore_Create_AfterSoftDeleteDoesNotReuseID(t *testing.T) {
	gs, _, _ := newGitStoreRepo(t)

	first, err := gs.Create(Task{Title: "one", Status: "open"})
	if err != nil {
		t.Fatalf("Create 1: %v", err)
	}
	second, err := gs.Create(Task{Title: "two", Status: "open"})
	if err != nil {
		t.Fatalf("Create 2: %v", err)
	}
	if first.ID != 1 || second.ID != 2 {
		t.Fatalf("ids = %d, %d, want 1, 2", first.ID, second.ID)
	}

	if _, err := gs.SoftDelete(second.ID); err != nil {
		t.Fatalf("SoftDelete(%d): %v", second.ID, err)
	}

	third, err := gs.Create(Task{Title: "three", Status: "open"})
	if err != nil {
		t.Fatalf("Create 3: %v", err)
	}
	// This is the central invariant: task 2's ref still exists (soft delete
	// never removes it), so a second task 2 would make history ambiguous -
	// two different task identities sharing one ref/commit chain.
	if third.ID != 3 {
		t.Fatalf("id after soft-deleting %d = %d, want 3 (a second task 2 would make history ambiguous)", second.ID, third.ID)
	}
}

// --- 3. Update mutates and commits ---

func TestGitStore_Update_MutatesAndCommits(t *testing.T) {
	gs, _, _ := newGitStoreRepo(t)
	created, err := gs.Create(Task{Title: "before", Status: "open"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	updated, err := gs.Update(created.ID, func(task *Task) (bool, error) {
		task.Title = "after"
		return true, nil
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if updated.Title != "after" {
		t.Errorf("Update() returned Title = %q, want %q", updated.Title, "after")
	}

	got, err := gs.Get([]int{created.ID})
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if len(got) != 1 || got[0].Title != "after" {
		t.Fatalf("Get after Update = %+v, want a single task titled %q", got, "after")
	}

	hist, err := gs.History(created.ID)
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	if len(hist) != 2 {
		t.Fatalf("len(History) = %d, want 2 (create + update)", len(hist))
	}
}

// --- 4. Update returning false aborts with no write ---

func TestGitStore_Update_ReturnFalseAbortsWithNoWrite(t *testing.T) {
	gs, rs, _ := newGitStoreRepo(t)
	created, err := gs.Create(Task{Title: "before", Status: "open"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	ref := gs.TaskRef(created.ID)

	before, err := rs.ResolveRef(ref)
	if err != nil {
		t.Fatalf("ResolveRef before Update: %v", err)
	}

	// mutate reporting "no change" is not a failure - it must not write, but
	// it also must not error just because nothing changed.
	if _, err := gs.Update(created.ID, func(task *Task) (bool, error) {
		task.Title = "should not stick"
		return false, nil
	}); err != nil {
		t.Fatalf("Update with mutate returning false: %v, want nil (a no-op is not an error)", err)
	}

	after, err := rs.ResolveRef(ref)
	if err != nil {
		t.Fatalf("ResolveRef after Update: %v", err)
	}
	if before != after {
		t.Errorf("ref oid changed from %s to %s, want unchanged when mutate returns false", before, after)
	}

	hist, err := gs.History(created.ID)
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	if len(hist) != 1 {
		t.Errorf("len(History) = %d, want 1 (no new version written)", len(hist))
	}
	if hist[0].Title != "before" {
		t.Errorf("hist[0].Title = %q, want %q (unmutated)", hist[0].Title, "before")
	}
}

// --- 5. SoftDelete sets Deleted, keeps the ref ---

func TestGitStore_SoftDelete_SetsDeletedKeepsRef(t *testing.T) {
	gs, rs, _ := newGitStoreRepo(t)
	created, err := gs.Create(Task{Title: "doomed", Status: "open"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	deleted, err := gs.SoftDelete(created.ID)
	if err != nil {
		t.Fatalf("SoftDelete: %v", err)
	}
	if !deleted.Deleted {
		t.Errorf("SoftDelete() returned Deleted = false, want true")
	}

	if _, err := rs.ResolveRef(gs.TaskRef(created.ID)); err != nil {
		t.Errorf("ResolveRef(%s) after SoftDelete: %v, want the ref to still resolve (refs are never removed)", gs.TaskRef(created.ID), err)
	}

	if _, err := gs.Get([]int{created.ID}); err == nil {
		t.Errorf("Get(%d) after SoftDelete = nil error, want not found (matches file-backend semantics)", created.ID)
	}

	got, err := gs.GetWithHistory([]int{created.ID})
	if err != nil {
		t.Fatalf("GetWithHistory: %v", err)
	}
	if len(got) != 1 || !got[0].Deleted {
		t.Fatalf("GetWithHistory(%d) = %+v, want a single Deleted task", created.ID, got)
	}

	all, err := gs.LoadAll()
	if err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	if !containsID(all, created.ID) {
		t.Errorf("LoadAll ids = %v, want it to include soft-deleted task %d", taskIDs(all), created.ID)
	}
}

// --- 6. SoftDelete is idempotent ---

func TestGitStore_SoftDelete_Idempotent(t *testing.T) {
	gs, _, _ := newGitStoreRepo(t)
	created, err := gs.Create(Task{Title: "doomed", Status: "open"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if _, err := gs.SoftDelete(created.ID); err != nil {
		t.Fatalf("first SoftDelete: %v", err)
	}
	if _, err := gs.SoftDelete(created.ID); err != nil {
		t.Fatalf("second SoftDelete: %v, want idempotent success", err)
	}
}

// --- 7. Claim happy path ---

func TestGitStore_Claim_HappyPath(t *testing.T) {
	gs, _, _ := newGitStoreRepo(t)
	created, err := gs.Create(Task{Title: "claim me", Status: "open"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	files := []string{"a.go", "b.go"}
	claimed, err := gs.Claim(created.ID, "agent-1", files)
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if claimed.Status != "inprogress" {
		t.Errorf("claimed.Status = %q, want %q", claimed.Status, "inprogress")
	}
	if claimed.AgentID != "agent-1" {
		t.Errorf("claimed.AgentID = %q, want %q", claimed.AgentID, "agent-1")
	}
	if !slices.Equal(claimed.FilesInScope, files) {
		t.Errorf("claimed.FilesInScope = %v, want %v", claimed.FilesInScope, files)
	}

	got, err := gs.Get([]int{created.ID})
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("Get = %+v, want one task", got)
	}
	if got[0].Status != "inprogress" || got[0].AgentID != "agent-1" || !slices.Equal(got[0].FilesInScope, files) {
		t.Errorf("Get after Claim = %+v, want status=inprogress agent=agent-1 files=%v", got[0], files)
	}
}

// --- 8. Claim of an already-claimed task fails, winner's claim intact ---

func TestGitStore_Claim_AlreadyClaimedPreservesWinnersAgentID(t *testing.T) {
	gs, _, _ := newGitStoreRepo(t)
	created, err := gs.Create(Task{Title: "claim me", Status: "open"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if _, err := gs.Claim(created.ID, "winner", nil); err != nil {
		t.Fatalf("first Claim: %v", err)
	}

	if _, err := gs.Claim(created.ID, "loser", nil); !errors.Is(err, ErrAlreadyClaimed) {
		t.Fatalf("second Claim err = %v, want errors.Is(err, ErrAlreadyClaimed)", err)
	}

	// The regression this guards: a naive retry loop that only checks the
	// precondition once could still let "loser" clobber the stored task even
	// though Claim reported an error back to the caller. Assert the winner's
	// AgentID is what actually persisted, not just that an error came back.
	got, err := gs.Get([]int{created.ID})
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if len(got) != 1 || got[0].AgentID != "winner" {
		t.Fatalf("stored task after failed second Claim = %+v, want AgentID=%q intact", got, "winner")
	}
}

// --- 9. THE RACE TEST: concurrent Claim, exactly one winner ---

func TestGitStore_Claim_ConcurrentRaceExactlyOneWinner(t *testing.T) {
	const iterations = 20
	for iter := 0; iter < iterations; iter++ {
		gs, _, _ := newGitStoreRepo(t)
		created, err := gs.Create(Task{Title: "race me", Status: "open"})
		if err != nil {
			t.Fatalf("iter %d: Create: %v", iter, err)
		}

		a, b := runConcurrentClaims(gs, created.ID, "agent-a", "agent-b")

		var winner, loser claimAttempt
		switch {
		case a.err == nil && b.err != nil:
			winner, loser = a, b
		case b.err == nil && a.err != nil:
			winner, loser = b, a
		default:
			// Either both succeeded (the double-claim bug: two "winners") or
			// both failed (contention that should have resolved between just
			// two contenders) - neither is a well-defined outcome.
			t.Fatalf("iter %d: want exactly one winner and one loser, got a=%+v b=%+v", iter, a, b)
		}

		if !errors.Is(loser.err, ErrAlreadyClaimed) && !errors.Is(loser.err, ErrCASConflict) {
			t.Fatalf("iter %d: loser err = %v, want errors.Is(_, ErrAlreadyClaimed) or errors.Is(_, ErrCASConflict)", iter, loser.err)
		}
		if winner.task.AgentID != winner.agentID {
			t.Fatalf("iter %d: Claim() return value AgentID = %q, want %q (the caller that actually won)", iter, winner.task.AgentID, winner.agentID)
		}

		// The critical check: re-read from git rather than trusting either
		// goroutine's return value, since the retry trap is precisely a bug
		// where the *stored* state ends up stomped even though the right
		// caller got the right return value in memory.
		got, err := gs.Get([]int{created.ID})
		if err != nil {
			t.Fatalf("iter %d: Get: %v", iter, err)
		}
		if len(got) != 1 || got[0].AgentID != winner.agentID {
			t.Fatalf("iter %d: stored task = %+v, want AgentID=%q (a naive retry must not stomp the winner)", iter, got, winner.agentID)
		}
	}
}

// --- 10. Concurrent Create race: distinct ids ---

func TestGitStore_Create_ConcurrentRaceDistinctIDs(t *testing.T) {
	const iterations = 10
	for iter := 0; iter < iterations; iter++ {
		gs, _, _ := newGitStoreRepo(t)

		start := make(chan struct{})
		var wg sync.WaitGroup
		wg.Add(2)
		var a, b Task
		var errA, errB error
		go func() {
			defer wg.Done()
			<-start
			a, errA = gs.Create(Task{Title: "created by A", Status: "open"})
		}()
		go func() {
			defer wg.Done()
			<-start
			b, errB = gs.Create(Task{Title: "created by B", Status: "open"})
		}()
		close(start)
		wg.Wait()

		if errA != nil {
			t.Fatalf("iter %d: Create A: %v", iter, errA)
		}
		if errB != nil {
			t.Fatalf("iter %d: Create B: %v", iter, errB)
		}
		if a.ID == b.ID {
			t.Fatalf("iter %d: both concurrent creates got id %d, want distinct ids", iter, a.ID)
		}

		got, err := gs.Get([]int{a.ID, b.ID})
		if err != nil {
			t.Fatalf("iter %d: Get([%d,%d]): %v", iter, a.ID, b.ID, err)
		}
		if len(got) != 2 {
			t.Fatalf("iter %d: Get([%d,%d]) = %+v, want both tasks present", iter, a.ID, b.ID, got)
		}
	}
}

// --- 11. Concurrent Update race: no lost update ---

func TestGitStore_Update_ConcurrentRaceNoLostUpdate(t *testing.T) {
	gs, _, _ := newGitStoreRepo(t)
	created, err := gs.Create(Task{Title: "shared", Status: "open"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	start := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(2)
	var errA, errB error
	go func() {
		defer wg.Done()
		<-start
		_, errA = gs.Update(created.ID, func(task *Task) (bool, error) {
			task.Tags = append(append([]string{}, task.Tags...), "tag-a")
			return true, nil
		})
	}()
	go func() {
		defer wg.Done()
		<-start
		_, errB = gs.Update(created.ID, func(task *Task) (bool, error) {
			task.Tags = append(append([]string{}, task.Tags...), "tag-b")
			return true, nil
		})
	}()
	close(start)
	wg.Wait()

	got, err := gs.Get([]int{created.ID})
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	final := got[0].Tags
	hasA := slices.Contains(final, "tag-a")
	hasB := slices.Contains(final, "tag-b")

	hist, err := gs.History(created.ID)
	if err != nil {
		t.Fatalf("History: %v", err)
	}

	// Whichever outcome the implementation guarantees, the final state must
	// be one of exactly two well-defined shapes - never a state with both
	// errors nil but a tag missing (a lost update), and never extra/garbled
	// entries (a merged/corrupted write).
	switch {
	case errA == nil && errB == nil:
		if !hasA || !hasB || len(final) != 2 {
			t.Fatalf("both Updates returned nil but final Tags = %v, want exactly [tag-a tag-b] in some order (lost update)", final)
		}
		if len(hist) != 3 {
			t.Errorf("len(History) = %d, want 3 (create + two independently-committed updates)", len(hist))
		}
	case errA == nil && errB != nil:
		if !hasA || hasB || len(final) != 1 {
			t.Fatalf("A won (errB=%v) but final Tags = %v, want exactly [tag-a]", errB, final)
		}
		if len(hist) != 2 {
			t.Errorf("len(History) = %d, want 2 (create + A's update)", len(hist))
		}
	case errB == nil && errA != nil:
		if !hasB || hasA || len(final) != 1 {
			t.Fatalf("B won (errA=%v) but final Tags = %v, want exactly [tag-b]", errA, final)
		}
		if len(hist) != 2 {
			t.Errorf("len(History) = %d, want 2 (create + B's update)", len(hist))
		}
	default:
		t.Fatalf("both concurrent Updates errored: errA=%v errB=%v, want at least one to succeed", errA, errB)
	}
}

// --- 12. FilesInScope is advisory, not a lock ---

func TestGitStore_Claim_FilesInScopeIsAdvisoryNotALock(t *testing.T) {
	gs, _, _ := newGitStoreRepo(t)
	task1, err := gs.Create(Task{Title: "one", Status: "open"})
	if err != nil {
		t.Fatalf("Create 1: %v", err)
	}
	task2, err := gs.Create(Task{Title: "two", Status: "open"})
	if err != nil {
		t.Fatalf("Create 2: %v", err)
	}

	shared := []string{"pkg/meads/shared.go"}
	claimed1, err := gs.Claim(task1.ID, "agent-1", shared)
	if err != nil {
		t.Fatalf("Claim task %d listing %v: %v", task1.ID, shared, err)
	}
	claimed2, err := gs.Claim(task2.ID, "agent-2", shared)
	if err != nil {
		t.Fatalf("Claim task %d listing the same file %v: %v, want success (FilesInScope is advisory, not arbitrated as a lock)", task2.ID, shared, err)
	}

	if !slices.Equal(claimed1.FilesInScope, shared) {
		t.Errorf("task %d FilesInScope = %v, want %v", task1.ID, claimed1.FilesInScope, shared)
	}
	if !slices.Equal(claimed2.FilesInScope, shared) {
		t.Errorf("task %d FilesInScope = %v, want %v", task2.ID, claimed2.FilesInScope, shared)
	}
}

// --- 13. Update of an absent task errors, creates no ref ---

func TestGitStore_Update_AbsentTaskErrors(t *testing.T) {
	gs, rs, _ := newGitStoreRepo(t)
	const id = 404

	_, err := gs.Update(id, func(task *Task) (bool, error) {
		task.Title = "should never be written"
		return true, nil
	})
	if err == nil {
		t.Fatal("Update of an absent task = nil error, want an error")
	}

	if _, err := rs.ResolveRef(gs.TaskRef(id)); !errors.Is(err, ErrRefNotFound) {
		t.Errorf("ResolveRef(%s) after failed Update = %v, want ErrRefNotFound (Update must not create a ref for an absent task)", gs.TaskRef(id), err)
	}
}

// --- 14. Dependency cleanup: unblocks a dependent task ---

func TestGitStore_SoftDelete_CleansDependentsAndUnblocks(t *testing.T) {
	gs, rs, _ := newGitStoreRepo(t)
	seedTask(t, rs, gs, Task{ID: 1, Title: "root", Status: "open"})
	seedTask(t, rs, gs, Task{ID: 2, Title: "dependent", Status: "open", DependsOn: []int{1}})

	if _, err := gs.SoftDelete(1); err != nil {
		t.Fatalf("SoftDelete(1): %v", err)
	}

	got, err := gs.Get([]int{2})
	if err != nil {
		t.Fatalf("Get(2): %v", err)
	}
	if len(got[0].DependsOn) != 0 {
		t.Errorf("task 2 DependsOn = %v after deleting 1, want 1 removed", got[0].DependsOn)
	}

	// The user-visible symptom: task 2 was blocked by 1, and must now be
	// unblocked rather than referencing a ref that will forever read back
	// Deleted.
	ready, err := gs.Ready()
	if err != nil {
		t.Fatalf("Ready after SoftDelete: %v", err)
	}
	if !containsID(ready, 2) {
		t.Errorf("Ready() after SoftDelete(1) = %v, want it to include unblocked task 2", taskIDs(ready))
	}

	// The sharper failure mode an uncleaned DependsOn causes: validateDeps
	// (tombstone.go) builds its active-id set from non-deleted tasks only
	// (depGraph, cycles.go), so a stale reference to a deleted id reads as
	// "depends on non-existent task" and permanently rejects every future
	// mutation of task 2, not just its Ready() placement - confirm task 2 is
	// still ordinarily editable.
	if _, err := gs.Update(2, func(task *Task) (bool, error) {
		task.Title = "renamed"
		return true, nil
	}); err != nil {
		t.Errorf("Update(2) after SoftDelete(1): %v, want success (a stale dep on a deleted task must not permanently block edits to the dependent)", err)
	}
}

// --- 15. Multiple dependents are all cleaned in one delete ---

func TestGitStore_SoftDelete_CleansMultipleDependents(t *testing.T) {
	gs, rs, _ := newGitStoreRepo(t)
	seedTask(t, rs, gs, Task{ID: 1, Title: "root", Status: "open"})
	seedTask(t, rs, gs, Task{ID: 2, Title: "dependent A", Status: "open", DependsOn: []int{1}})
	seedTask(t, rs, gs, Task{ID: 3, Title: "dependent B", Status: "open", DependsOn: []int{1, 2}}) // also depends on 2: must keep that one
	seedTask(t, rs, gs, Task{ID: 4, Title: "unrelated", Status: "open", DependsOn: []int{2}})      // never depended on 1: must be untouched

	ref4 := gs.TaskRef(4)
	oid4Before, err := rs.ResolveRef(ref4)
	if err != nil {
		t.Fatalf("ResolveRef(4) before: %v", err)
	}

	if _, err := gs.SoftDelete(1); err != nil {
		t.Fatalf("SoftDelete(1): %v", err)
	}

	got, err := gs.Get([]int{2, 3, 4})
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	byID := make(map[int]Task, len(got))
	for _, task := range got {
		byID[task.ID] = task
	}
	if len(byID[2].DependsOn) != 0 {
		t.Errorf("task 2 DependsOn = %v, want 1 removed (empty)", byID[2].DependsOn)
	}
	if want := []int{2}; !slices.Equal(byID[3].DependsOn, want) {
		t.Errorf("task 3 DependsOn = %v, want %v (1 removed, 2 kept)", byID[3].DependsOn, want)
	}
	if want := []int{2}; !slices.Equal(byID[4].DependsOn, want) {
		t.Errorf("task 4 DependsOn = %v, want %v (never depended on 1, must be unaffected)", byID[4].DependsOn, want)
	}
	if oid4After, err := rs.ResolveRef(ref4); err != nil || oid4After != oid4Before {
		t.Errorf("task 4's ref = %v (err=%v), want unchanged %s (it never depended on 1, so it must not even get a new commit)", oid4After, err, oid4Before)
	}
}

// --- 16. Deleted tasks are not modified ---

func TestGitStore_SoftDelete_SkipsAlreadyDeletedDependents(t *testing.T) {
	gs, rs, _ := newGitStoreRepo(t)
	seedTask(t, rs, gs, Task{ID: 1, Title: "root", Status: "open"})
	seedTask(t, rs, gs, Task{ID: 2, Title: "already gone", Status: "open", DependsOn: []int{1}, Deleted: true})

	ref2 := gs.TaskRef(2)
	oid2Before, err := rs.ResolveRef(ref2)
	if err != nil {
		t.Fatalf("ResolveRef(2) before: %v", err)
	}

	if _, err := gs.SoftDelete(1); err != nil {
		t.Fatalf("SoftDelete(1): %v", err)
	}

	// A deleted dependent must not even get a new commit - matches the file
	// backend, which skips f.Tasks[i].Deleted entirely in its cleanup loop
	// (mutate.go).
	oid2After, err := rs.ResolveRef(ref2)
	if err != nil {
		t.Fatalf("ResolveRef(2) after: %v", err)
	}
	if oid2After != oid2Before {
		t.Errorf("deleted task 2's ref moved from %s to %s, want untouched", oid2Before, oid2After)
	}

	got, err := gs.GetWithHistory([]int{2})
	if err != nil {
		t.Fatalf("GetWithHistory(2): %v", err)
	}
	if len(got) != 1 || !slices.Equal(got[0].DependsOn, []int{1}) {
		t.Fatalf("deleted task 2 DependsOn = %v, want unchanged [1]", got[0].DependsOn)
	}
}

// --- 17. Parity with the file backend ---

func TestGitStore_SoftDelete_ParityWithFileBackend(t *testing.T) {
	fixture := []Task{
		{ID: 1, Title: "root", Status: "open", Priority: "P2",
			Meta: map[string]string{"status": "open", "priority": "P2"}},
		{ID: 2, Title: "dependent A", Status: "open", Priority: "P1", DependsOn: []int{1},
			Meta: map[string]string{"status": "open", "priority": "P1", "depends-on": "1"}},
		{ID: 3, Title: "dependent B", Status: "open", Priority: "P0", DependsOn: []int{1, 2},
			Meta: map[string]string{"status": "open", "priority": "P0", "depends-on": "1,2"}},
		{ID: 4, Title: "already-deleted dependent", Status: "open", DependsOn: []int{1}, Deleted: true,
			Meta: map[string]string{"status": "open", "depends-on": "1"}},
		{ID: 5, Title: "unrelated closed", Status: "closed",
			Meta: map[string]string{"status": "closed"}},
	}

	gs, rs, _ := newGitStoreRepo(t)
	for _, task := range fixture {
		seedTask(t, rs, gs, task)
	}
	fileStore := newTestStore(t, FormatFile(File{Tasks: fixture}))

	if _, err := gs.SoftDelete(1); err != nil {
		t.Fatalf("GitStore.SoftDelete(1): %v", err)
	}
	if err := fileStore.Delete(1); err != nil {
		t.Fatalf("Store.Delete(1): %v", err)
	}

	gitReady, err := gs.Ready()
	if err != nil {
		t.Fatalf("GitStore.Ready: %v", err)
	}
	fileReady, err := fileStore.Ready()
	if err != nil {
		t.Fatalf("Store.Ready: %v", err)
	}
	if !slices.Equal(taskIDs(gitReady), taskIDs(fileReady)) {
		t.Errorf("Ready() id mismatch:\n  GitStore: %v\n  Store:    %v", taskIDs(gitReady), taskIDs(fileReady))
	}

	// Every surviving (non-deleted) task's DependsOn must agree between the
	// two backends - the regression this whole fix is about.
	for _, id := range []int{2, 3} {
		gitTask, err := gs.Get([]int{id})
		if err != nil {
			t.Fatalf("GitStore.Get(%d): %v", id, err)
		}
		fileTask, err := fileStore.Get([]int{id})
		if err != nil {
			t.Fatalf("Store.Get(%d): %v", id, err)
		}
		if !slices.Equal(gitTask[0].DependsOn, fileTask[0].DependsOn) {
			t.Errorf("task %d DependsOn mismatch:\n  GitStore: %v\n  Store:    %v", id, gitTask[0].DependsOn, fileTask[0].DependsOn)
		}
	}
}

// --- 18. Atomicity: a batch with one stale ref moves NOTHING, not even a
// ref whose own prev was still perfectly valid ---

func TestGitStore_SoftDelete_AtomicBatchLeavesNothingPartiallyMoved(t *testing.T) {
	gs, rs, _ := newGitStoreRepo(t)
	seedTask(t, rs, gs, Task{ID: 1, Title: "root", Status: "open"})
	seedTask(t, rs, gs, Task{ID: 2, Title: "dependent", Status: "open", DependsOn: []int{1}})

	ref1, ref2 := gs.TaskRef(1), gs.TaskRef(2)
	oid1, err := rs.ResolveRef(ref1)
	if err != nil {
		t.Fatalf("ResolveRef(1): %v", err)
	}
	oid2, err := rs.ResolveRef(ref2)
	if err != nil {
		t.Fatalf("ResolveRef(2): %v", err)
	}

	// Build the exact new commits a real SoftDelete(1) attempt would build -
	// the "read, then decide" half of the cycle - parented on the oids just
	// read, via the production helper (buildTaskVersion).
	newCommit1, err := gs.buildTaskVersion(1, Task{ID: 1, Title: "root", Status: "open", Deleted: true}, oid1, "soft delete task 1")
	if err != nil {
		t.Fatalf("buildTaskVersion(1): %v", err)
	}
	newCommit2, err := gs.buildTaskVersion(2, Task{ID: 2, Title: "dependent", Status: "open"}, oid2, "remove dependency on deleted task 1")
	if err != nil {
		t.Fatalf("buildTaskVersion(2): %v", err)
	}

	// Simulate a concurrent writer landing on task 2's ref between the read
	// above and the batch write below - exactly the race SoftDelete's own
	// retry loop must survive. Submitting the batch directly here (rather
	// than through SoftDelete, which would simply retry and succeed) lets a
	// single failed AtomicUpdate call be inspected in isolation, proving the
	// transaction semantics the retry loop depends on.
	racingCommit := commitTaskVersion(t, rs, gs, Task{ID: 2, Title: "dependent", Status: "open", DependsOn: []int{1}, StatusReason: "raced in"}, oid2)

	err = rs.AtomicUpdate([]RefUpdate{
		{Name: ref1, Next: newCommit1, Prev: oid1}, // still a valid prev in isolation
		{Name: ref2, Next: newCommit2, Prev: oid2}, // now stale: oid2 moved to racingCommit
	})
	if err == nil {
		t.Fatal("AtomicUpdate with one stale prev = nil error, want ErrCASConflict")
	}
	if !errors.Is(err, ErrCASConflict) {
		t.Errorf("err = %v, want errors.Is(err, ErrCASConflict)", err)
	}

	// The crux of atomicity: ref1's prev was perfectly valid on its own, but
	// it must not have moved, because it shared a batch with ref2, which failed.
	if got, rerr := rs.ResolveRef(ref1); rerr != nil || got != oid1 {
		t.Errorf("ref1 = %v (err=%v), want unchanged %s (a valid-in-isolation ref must not move when its batch-mate fails)", got, rerr, oid1)
	}
	if got, rerr := rs.ResolveRef(ref2); rerr != nil || got != racingCommit {
		t.Errorf("ref2 = %v (err=%v), want the racing writer's commit %s, not our attempted write %s", got, rerr, racingCommit, newCommit2)
	}
}

// --- 19. Idempotent: a repeat delete does not re-clean or re-touch dependents ---

func TestGitStore_SoftDelete_IdempotentDoesNotReTouchDependents(t *testing.T) {
	gs, rs, _ := newGitStoreRepo(t)
	seedTask(t, rs, gs, Task{ID: 1, Title: "root", Status: "open"})
	seedTask(t, rs, gs, Task{ID: 2, Title: "dependent", Status: "open", DependsOn: []int{1}})

	if _, err := gs.SoftDelete(1); err != nil {
		t.Fatalf("first SoftDelete(1): %v", err)
	}
	ref1, ref2 := gs.TaskRef(1), gs.TaskRef(2)
	oid1After, err := rs.ResolveRef(ref1)
	if err != nil {
		t.Fatalf("ResolveRef(1) after first delete: %v", err)
	}
	oid2After, err := rs.ResolveRef(ref2)
	if err != nil {
		t.Fatalf("ResolveRef(2) after first delete: %v", err)
	}

	if _, err := gs.SoftDelete(1); err != nil {
		t.Fatalf("second SoftDelete(1): %v, want idempotent success", err)
	}

	oid1Repeat, err := rs.ResolveRef(ref1)
	if err != nil {
		t.Fatalf("ResolveRef(1) after second delete: %v", err)
	}
	oid2Repeat, err := rs.ResolveRef(ref2)
	if err != nil {
		t.Fatalf("ResolveRef(2) after second delete: %v", err)
	}
	if oid1Repeat != oid1After {
		t.Errorf("task 1 ref moved on a repeat delete: %s -> %s, want unchanged", oid1After, oid1Repeat)
	}
	if oid2Repeat != oid2After {
		t.Errorf("task 2 ref moved on a repeat delete: %s -> %s, want untouched (an already-cleaned dependent must not be re-written)", oid2After, oid2Repeat)
	}

	got, err := gs.Get([]int{2})
	if err != nil {
		t.Fatalf("Get(2): %v", err)
	}
	if len(got[0].DependsOn) != 0 {
		t.Errorf("task 2 DependsOn = %v after repeat delete, want still empty (not corrupted)", got[0].DependsOn)
	}
}

// --- 20. SetDependsOn keeps Meta in sync: git mode's cleaned DependsOn and
// the file backend's persisted Meta["depends-on"] string agree ---
//
// The two backends store the cleaned list differently: SetDependsOn
// (task.go) writes Meta["depends-on"] = formatIntSlice(ids), and the file
// backend's markdown format persists that string directly (FormatTask has no
// dedicated depends-on field to fall back on). Git mode's Task.MarshalJSON
// strips known meta keys - including "depends-on" - before storing, since
// the JSON blob already has its own top-level "depends_on" field and keeping
// both would be a second source of truth. So after a round trip, git mode's
// Task.Meta never carries "depends-on" at all - that is not corruption, it's
// the intended storage split. What must agree is the *value*: running git
// mode's cleaned DependsOn through the same formatIntSlice SetDependsOn uses
// must produce exactly the string the file backend persisted.
func TestGitStore_SoftDelete_DependsOnMatchesFileBackendSetDependsOnForm(t *testing.T) {
	fixture := []Task{
		{ID: 1, Title: "root", Status: "open", Meta: map[string]string{"status": "open"}},
		{ID: 2, Title: "dependent", Status: "open", DependsOn: []int{1},
			Meta: map[string]string{"status": "open", "depends-on": "1"}},
	}

	gs, rs, _ := newGitStoreRepo(t)
	for _, task := range fixture {
		seedTask(t, rs, gs, task)
	}
	fileStore := newTestStore(t, FormatFile(File{Tasks: fixture}))

	if _, err := gs.SoftDelete(1); err != nil {
		t.Fatalf("GitStore.SoftDelete(1): %v", err)
	}
	if err := fileStore.Delete(1); err != nil {
		t.Fatalf("Store.Delete(1): %v", err)
	}

	gitDep, err := gs.Get([]int{2})
	if err != nil {
		t.Fatalf("GitStore.Get(2): %v", err)
	}
	fileDep, err := fileStore.Get([]int{2})
	if err != nil {
		t.Fatalf("Store.Get(2): %v", err)
	}

	wantMeta := formatIntSlice(gitDep[0].DependsOn) // what SetDependsOn would write
	if fileDep[0].Meta["depends-on"] != wantMeta {
		t.Errorf("file backend Meta[depends-on] = %q, want %q (git mode's cleaned DependsOn run through the same formatIntSlice SetDependsOn uses)",
			fileDep[0].Meta["depends-on"], wantMeta)
	}
	if wantMeta != "" {
		t.Errorf("cleaned DependsOn serialized form = %q, want empty after removing task 2's only dependency", wantMeta)
	}
}
