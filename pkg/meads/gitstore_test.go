package meads

import (
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"testing"
)

// Tests for GitStore, the read path over refs/meads/tasks/* built on top of
// RefStore (git mode phase 2, TASKS #59). Like refstore_test.go, these run
// against real temporary git repositories via ExecGit rather than a fake,
// since the guarantee under test is about actual git ref/object state -
// including next-id computation from ref names alone and multi-version
// history walks spanning several commits on one ref, neither of which a
// fake could exercise meaningfully.
//
// Several tests also build the identical task set in a file-backed Store
// (see newTestStore in lock_test.go) to guard against the two backends'
// read semantics drifting apart - that parity is the whole point of
// standing up a second backend in the first place.

// --- helpers ---

// newGitStoreRepo creates a temporary git repository under a fresh
// t.TempDir() (mirrors newRefStoreRepo in refstore_test.go) and returns a
// GitStore and the RefStore it is built on, plus the repo directory.
// Nothing is committed to any branch: GitStore only ever touches
// refs/meads/tasks/*, so an unborn HEAD is a realistic starting state, not
// a special case that needs papering over.
func newGitStoreRepo(t *testing.T) (*GitStore, *RefStore, string) {
	t.Helper()
	dir := t.TempDir()
	runGit(t, dir, "init", "-b", "main")
	runGit(t, dir, "config", "user.name", "Test")
	runGit(t, dir, "config", "user.email", "test@test.com")
	git := &ExecGit{Dir: dir}
	return NewGitStore(git), NewRefStore(git), dir
}

// seedTask JSON-marshals task (through Task.MarshalJSON) and commits it as
// a brand-new task ref - i.e. the ref must not already exist. This is the
// standard way to seed a task that only needs one version; a task that
// needs several successive versions on the same ref (see the History
// tests) uses commitTaskVersion directly and threads the returned OID
// through as the next call's prev.
func seedTask(t *testing.T, rs *RefStore, gs *GitStore, task Task) {
	t.Helper()
	commitTaskVersion(t, rs, gs, task, ZeroOID)
}

// commitTaskVersion JSON-marshals task and commits it onto its task ref at
// prev (ZeroOID for a brand-new ref), returning the new commit OID so
// callers can chain further versions onto the same ref.
func commitTaskVersion(t *testing.T, rs *RefStore, gs *GitStore, task Task, prev OID) OID {
	t.Helper()
	data, err := json.Marshal(task)
	if err != nil {
		t.Fatalf("marshaling task %d: %v", task.ID, err)
	}
	oid, err := rs.CommitFile(gs.TaskRef(task.ID), TaskFileName, data, prev, "meads test: seed task")
	if err != nil {
		t.Fatalf("committing task %d (prev=%s): %v", task.ID, prev, err)
	}
	return oid
}

// taskIDs maps tasks to their ids, in order - the shape both the parity
// checks and several plain GitStore assertions compare against.
func taskIDs(tasks []Task) []int {
	ids := make([]int, len(tasks))
	for i, task := range tasks {
		ids[i] = task.ID
	}
	return ids
}

// containsID reports whether tasks contains a task with the given id.
func containsID(tasks []Task, id int) bool {
	for _, task := range tasks {
		if task.ID == id {
			return true
		}
	}
	return false
}

// readyFixture returns a task set exercising every Ready() rule the
// file-backed Store and GitStore must agree on:
//
//   - 1: open, P2, unblocked
//   - 2: open, P0, unblocked
//   - 3: open, P3, blocked by 4 (unclosed dependency)
//   - 4: inprogress (not open; also 3's blocker)
//   - 5: open, P1, depends on 6 which is closed (unblocked)
//   - 6: closed (not open; also 5's satisfied dependency)
//   - 7: draft, P0 (not open, despite the highest priority)
//   - 8: open, P0, but Deleted - must never appear
//
// The three ready tasks (1, 2, 5) are deliberately given three different
// priorities so the expected Ready() order is unambiguous. sort.Slice (used
// by both backends' Ready - see query.go) is not a stable sort, so a
// fixture relying on tie-breaking between same-priority tasks would be
// asserting an implementation accident rather than a real guarantee.
//
// Each task's Meta is populated to mirror its struct fields because
// markdown formatting (FormatTask, in markdown.go) reads status/priority/
// depends-on from Meta, not from the struct fields directly - see
// TestNewFields_RoundTrip in markdown_test.go for the established
// convention this follows. GitStore storage ignores the duplication
// (Task.MarshalJSON strips known keys back out of "meta" before storing),
// so the same fixture seeds both backends correctly.
func readyFixture() []Task {
	return []Task{
		{ID: 1, Title: "open P2 unblocked", Status: "open", Priority: "P2",
			Meta: map[string]string{"status": "open", "priority": "P2"}},
		{ID: 2, Title: "open P0 unblocked", Status: "open", Priority: "P0",
			Meta: map[string]string{"status": "open", "priority": "P0"}},
		{ID: 3, Title: "open blocked by 4", Status: "open", Priority: "P3", DependsOn: []int{4},
			Meta: map[string]string{"status": "open", "priority": "P3", "depends-on": "4"}},
		{ID: 4, Title: "inprogress blocker", Status: "inprogress", Priority: "P2",
			Meta: map[string]string{"status": "inprogress", "priority": "P2"}},
		{ID: 5, Title: "open dep closed", Status: "open", Priority: "P1", DependsOn: []int{6},
			Meta: map[string]string{"status": "open", "priority": "P1", "depends-on": "6"}},
		{ID: 6, Title: "closed dependency", Status: "closed", Priority: "P2",
			Meta: map[string]string{"status": "closed", "priority": "P2"}},
		{ID: 7, Title: "draft high priority", Status: "draft", Priority: "P0",
			Meta: map[string]string{"status": "draft", "priority": "P0"}},
		{ID: 8, Title: "deleted but open", Status: "open", Priority: "P0", Deleted: true,
			Meta: map[string]string{"status": "open", "priority": "P0"}},
	}
}

// --- 1. LoadAll: ascending by id, includes soft-deleted tasks ---

func TestGitStore_LoadAll_AscendingIncludesDeleted(t *testing.T) {
	gs, rs, _ := newGitStoreRepo(t)
	// Seed out of numeric order so a correct implementation must actually
	// sort by id rather than happening to preserve creation order.
	seedTask(t, rs, gs, Task{ID: 5, Title: "five", Status: "open"})
	seedTask(t, rs, gs, Task{ID: 2, Title: "two", Status: "open"})
	seedTask(t, rs, gs, Task{ID: 8, Title: "eight deleted", Status: "open", Deleted: true})
	seedTask(t, rs, gs, Task{ID: 1, Title: "one", Status: "open"})

	got, err := gs.LoadAll()
	if err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	want := []int{1, 2, 5, 8}
	if !slices.Equal(taskIDs(got), want) {
		t.Fatalf("LoadAll ids = %v, want %v ascending", taskIDs(got), want)
	}
	for _, task := range got {
		wantDeleted := task.ID == 8
		if task.Deleted != wantDeleted {
			t.Errorf("task %d Deleted = %v, want %v", task.ID, task.Deleted, wantDeleted)
		}
	}
}

// --- 2. LoadAll on an empty repo ---

func TestGitStore_LoadAll_EmptyRepo(t *testing.T) {
	gs, _, _ := newGitStoreRepo(t)
	got, err := gs.LoadAll()
	if err != nil {
		t.Fatalf("LoadAll on empty repo: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("LoadAll on empty repo = %v, want none", got)
	}
}

// --- 3. Get(nil): active tasks only, soft-deleted excluded ---

func TestGitStore_Get_NilExcludesDeleted(t *testing.T) {
	gs, rs, _ := newGitStoreRepo(t)
	seedTask(t, rs, gs, Task{ID: 1, Title: "one", Status: "open"})
	seedTask(t, rs, gs, Task{ID: 2, Title: "two deleted", Status: "open", Deleted: true})
	// A non-open, non-deleted task must still come back from Get(nil) -
	// only Ready() filters by status; Get(nil) only filters deletion.
	seedTask(t, rs, gs, Task{ID: 3, Title: "three closed", Status: "closed"})

	got, err := gs.Get(nil)
	if err != nil {
		t.Fatalf("Get(nil): %v", err)
	}
	gotIDs := taskIDs(got)
	slices.Sort(gotIDs)
	want := []int{1, 3}
	if !slices.Equal(gotIDs, want) {
		t.Errorf("Get(nil) ids = %v, want %v (deleted task 2 must be excluded)", gotIDs, want)
	}
}

// --- 4. Get with explicit ids: returned in the order requested ---

func TestGitStore_Get_ExplicitIDsRequestOrder(t *testing.T) {
	gs, rs, _ := newGitStoreRepo(t)
	seedTask(t, rs, gs, Task{ID: 1, Title: "one", Status: "open"})
	seedTask(t, rs, gs, Task{ID: 2, Title: "two", Status: "open"})
	seedTask(t, rs, gs, Task{ID: 3, Title: "three", Status: "open"})

	got, err := gs.Get([]int{3, 1, 2})
	if err != nil {
		t.Fatalf("Get([3,1,2]): %v", err)
	}
	want := []int{3, 1, 2}
	if !slices.Equal(taskIDs(got), want) {
		t.Errorf("Get([3,1,2]) ids = %v, want %v in the order requested", taskIDs(got), want)
	}
}

// --- 5 & 6. Get of a soft-deleted or absent id errors like the file backend ---

func TestGitStore_Get_DeletedOrAbsentIDErrors(t *testing.T) {
	gs, rs, _ := newGitStoreRepo(t)
	seedTask(t, rs, gs, Task{ID: 1, Title: "one deleted", Status: "open", Deleted: true})

	t.Run("deleted", func(t *testing.T) {
		_, err := gs.Get([]int{1})
		if err == nil {
			t.Fatal("expected an error for a soft-deleted id, got nil")
		}
		if want := "task 1 not found"; err.Error() != want {
			t.Errorf("err = %q, want %q (must match the file backend's wording)", err.Error(), want)
		}
	})

	t.Run("absent", func(t *testing.T) {
		_, err := gs.Get([]int{99})
		if err == nil {
			t.Fatal("expected an error for an absent id, got nil")
		}
		if want := "task 99 not found"; err.Error() != want {
			t.Errorf("err = %q, want %q (must match the file backend's wording)", err.Error(), want)
		}
	})
}

// --- 7. NextID: basic max+1 ---

func TestGitStore_NextID_Basic(t *testing.T) {
	gs, rs, _ := newGitStoreRepo(t)
	seedTask(t, rs, gs, Task{ID: 1, Title: "one", Status: "open"})
	seedTask(t, rs, gs, Task{ID: 2, Title: "two", Status: "open"})
	seedTask(t, rs, gs, Task{ID: 5, Title: "five", Status: "open"})

	got, err := gs.NextID()
	if err != nil {
		t.Fatalf("NextID: %v", err)
	}
	if got != 6 {
		t.Errorf("NextID = %d, want 6", got)
	}
}

// --- 8. NextID: a soft-deleted id must never be reused ---

func TestGitStore_NextID_CountsSoftDeleted(t *testing.T) {
	gs, rs, _ := newGitStoreRepo(t)
	seedTask(t, rs, gs, Task{ID: 1, Title: "one", Status: "open"})
	seedTask(t, rs, gs, Task{ID: 2, Title: "two deleted", Status: "open", Deleted: true})

	got, err := gs.NextID()
	if err != nil {
		t.Fatalf("NextID: %v", err)
	}
	if got != 3 {
		t.Errorf("NextID = %d, want 3 (task 2's ref still exists and must not be reused)", got)
	}
}

// --- 9. NextID on an empty repo ---

func TestGitStore_NextID_EmptyRepo(t *testing.T) {
	gs, _, _ := newGitStoreRepo(t)
	got, err := gs.NextID()
	if err != nil {
		t.Fatalf("NextID on empty repo: %v", err)
	}
	if got != 1 {
		t.Errorf("NextID on empty repo = %d, want 1", got)
	}
}

// --- 10. NextID ignores non-numeric and nested refs ---

func TestGitStore_NextID_IgnoresJunkRefs(t *testing.T) {
	gs, rs, _ := newGitStoreRepo(t)
	seedTask(t, rs, gs, Task{ID: 3, Title: "three", Status: "open"})
	if _, err := rs.CommitFile(TasksRefPrefix+"notanumber", TaskFileName, []byte("{}"), ZeroOID, "junk"); err != nil {
		t.Fatalf("seeding non-numeric ref: %v", err)
	}
	if _, err := rs.CommitFile(TasksRefPrefix+"7/sub", TaskFileName, []byte("{}"), ZeroOID, "junk nested"); err != nil {
		t.Fatalf("seeding nested ref: %v", err)
	}

	got, err := gs.NextID()
	if err != nil {
		t.Fatalf("NextID: %v", err)
	}
	if got != 4 {
		t.Errorf("NextID = %d, want 4 (junk refs must not be parsed as ids or crash it)", got)
	}
}

// --- 11. Ready: status/dependency filtering, priority sort, deleted excluded ---

func TestGitStore_Ready_FiltersBlockedAndSortsByPriority(t *testing.T) {
	gs, rs, _ := newGitStoreRepo(t)
	for _, task := range readyFixture() {
		seedTask(t, rs, gs, task)
	}

	got, err := gs.Ready()
	if err != nil {
		t.Fatalf("Ready: %v", err)
	}

	for _, id := range []int{3, 4, 6, 7, 8} {
		if containsID(got, id) {
			t.Errorf("Ready() unexpectedly includes task %d", id)
		}
	}
	for _, id := range []int{1, 2, 5} {
		if !containsID(got, id) {
			t.Errorf("Ready() is missing expected task %d", id)
		}
	}
	// 8 is deleted despite being open at P0, the highest priority in the
	// fixture: if deletion filtering ran after the priority sort instead of
	// before, it would incorrectly outrank everything rather than being
	// absent outright.
	want := []int{2, 5, 1}
	if !slices.Equal(taskIDs(got), want) {
		t.Errorf("Ready() ids = %v, want %v (priority order P0 < P1 < P2)", taskIDs(got), want)
	}
}

// --- 12. Ready and Get(nil) parity with the file backend ---

func TestGitStore_Ready_ParityWithFileBackend(t *testing.T) {
	fixture := readyFixture()

	gs, rs, _ := newGitStoreRepo(t)
	for _, task := range fixture {
		seedTask(t, rs, gs, task)
	}
	fileStore := newTestStore(t, FormatFile(File{Tasks: fixture}))

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

	gitAll, err := gs.Get(nil)
	if err != nil {
		t.Fatalf("GitStore.Get(nil): %v", err)
	}
	fileAll, err := fileStore.Get(nil)
	if err != nil {
		t.Fatalf("Store.Get(nil): %v", err)
	}
	if !slices.Equal(taskIDs(gitAll), taskIDs(fileAll)) {
		t.Errorf("Get(nil) id mismatch:\n  GitStore: %v\n  Store:    %v", taskIDs(gitAll), taskIDs(fileAll))
	}
}

// --- 13. History: newest first, spanning several versions of one task ---

func TestGitStore_History_NewestFirstWithMiddleVersionFields(t *testing.T) {
	gs, rs, _ := newGitStoreRepo(t)
	const id = 9

	oid1 := commitTaskVersion(t, rs, gs, Task{
		ID: id, Title: "History task", Status: "open", Priority: "P2",
	}, ZeroOID)
	oid2 := commitTaskVersion(t, rs, gs, Task{
		ID: id, Title: "History task", Status: "inprogress", Priority: "P2",
		StatusReason: "actively working",
	}, oid1)
	commitTaskVersion(t, rs, gs, Task{
		ID: id, Title: "History task", Status: "closed", Priority: "P2",
		CloseReason: "done",
	}, oid2)

	hist, err := gs.History(id)
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	if len(hist) != 3 {
		t.Fatalf("len(History) = %d, want 3 (%+v)", len(hist), hist)
	}
	if hist[0].Status != "closed" || hist[0].CloseReason != "done" {
		t.Errorf("hist[0] (newest) = %+v, want status=closed close_reason=done", hist[0])
	}
	if hist[1].Status != "inprogress" || hist[1].StatusReason != "actively working" || hist[1].Title != "History task" {
		t.Errorf("hist[1] (middle) = %+v, want status=inprogress status_reason=%q title=%q",
			hist[1], "actively working", "History task")
	}
	if hist[2].Status != "open" {
		t.Errorf("hist[2] (oldest) = %+v, want status=open", hist[2])
	}

	current, err := gs.Get([]int{id})
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !reflect.DeepEqual(current[0], hist[0]) {
		t.Errorf("Get() = %+v, want it to match History's newest entry %+v", current[0], hist[0])
	}
}

// --- 14. History of an absent task ---

func TestGitStore_History_AbsentTaskErrors(t *testing.T) {
	gs, _, _ := newGitStoreRepo(t)
	_, err := gs.History(404)
	if !errors.Is(err, ErrRefNotFound) {
		t.Errorf("History(404) err = %v, want errors.Is(err, ErrRefNotFound)", err)
	}
}

// --- 15. Task JSON round trip through the GitStore, every field populated ---

func TestGitStore_TaskJSONRoundTrip_AllFields(t *testing.T) {
	gs, rs, _ := newGitStoreRepo(t)

	original := Task{
		ID:           42,
		Title:        "Full round trip",
		Status:       "closed",
		Priority:     "P1",
		Type:         "bug",
		DependsOn:    []int{1, 2, 3},
		CloseReason:  "wontfix",
		Tags:         []string{"alpha", "beta"},
		Deleted:      true,
		StatusReason: "superseded",
		Meta: map[string]string{
			"status":     "closed",       // known key: Task.MarshalJSON strips this from "meta"
			"priority":   "P1",           // known key: stripped too
			"custom-key": "custom-value", // unknown key: preserved in "meta"
		},
		Description: "line one\nline two\n\nline four after a blank line",
	}
	seedTask(t, rs, gs, original)

	all, err := gs.LoadAll()
	if err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	var got *Task
	for i := range all {
		if all[i].ID == original.ID {
			got = &all[i]
		}
	}
	if got == nil {
		t.Fatalf("LoadAll did not return task %d: %+v", original.ID, all)
	}

	// Task.MarshalJSON intentionally strips knownMetaKeys ("status",
	// "priority", ...) from the "meta" object before storing, since they
	// are already carried by their own top-level fields - so only the
	// unknown key is expected to survive the round trip.
	want := original
	want.Meta = map[string]string{"custom-key": "custom-value"}

	if !reflect.DeepEqual(*got, want) {
		t.Errorf("round trip mismatch:\n got  %+v\n want %+v", *got, want)
	}
}

// The CLI's `md get` uses GetWithHistory so a deleted task still resolves.
// In git mode the ref persists, so this needs no history walk.
func TestGitStore_GetWithHistory_ResolvesDeleted(t *testing.T) {
	gs, rs, _ := newGitStoreRepo(t)
	seedTask(t, rs, gs, Task{ID: 1, Title: "live", Status: "open"})
	seedTask(t, rs, gs, Task{ID: 2, Title: "gone", Status: "closed", Deleted: true})

	// plain Get still refuses a deleted id, matching the file backend
	if _, err := gs.Get([]int{2}); err == nil {
		t.Fatal("Get(2) on a deleted task = nil error, want not found")
	}
	got, err := gs.GetWithHistory([]int{2})
	if err != nil {
		t.Fatalf("GetWithHistory(2): %v", err)
	}
	if len(got) != 1 || got[0].ID != 2 || !got[0].Deleted || got[0].Title != "gone" {
		t.Fatalf("GetWithHistory(2) = %+v, want the deleted task 2", got)
	}
	// empty ids stays active-only
	all, err := gs.GetWithHistory(nil)
	if err != nil {
		t.Fatalf("GetWithHistory(nil): %v", err)
	}
	if len(all) != 1 || all[0].ID != 1 {
		t.Fatalf("GetWithHistory(nil) = %+v, want only active task 1", all)
	}
}

// FindCycles is list/ready's warnCycles helper's read path in git mode (see
// cmd/md/main.go); it must agree with the file backend's Store.FindCycles on
// what counts as a cycle, and must never trip on a soft-deleted task's
// DependsOn edges.
func TestGitStore_FindCycles(t *testing.T) {
	gs, rs, _ := newGitStoreRepo(t)
	seedTask(t, rs, gs, Task{ID: 1, Title: "one", Status: "open", DependsOn: []int{2}})
	seedTask(t, rs, gs, Task{ID: 2, Title: "two", Status: "open", DependsOn: []int{1}})
	// A cycle through a deleted task must not be reported: deleted tasks are
	// excluded from the active graph FindCycles builds, same as the file
	// backend's Store.FindCycles.
	seedTask(t, rs, gs, Task{ID: 3, Title: "three deleted", Status: "open", Deleted: true, DependsOn: []int{4}})
	seedTask(t, rs, gs, Task{ID: 4, Title: "four", Status: "open", DependsOn: []int{3}})

	cycles, err := gs.FindCycles()
	if err != nil {
		t.Fatalf("FindCycles: %v", err)
	}
	if len(cycles) != 1 {
		t.Fatalf("len(FindCycles()) = %d, want 1 (%v)", len(cycles), cycles)
	}
	if got := cycleSig(cycles[0]); got != "1,2" {
		t.Errorf("cycle = %v (sig %q), want the 1<->2 cycle (sig \"1,2\")", cycles[0], got)
	}
}

func TestGitStore_FindCycles_NoneIsNilNotError(t *testing.T) {
	gs, rs, _ := newGitStoreRepo(t)
	seedTask(t, rs, gs, Task{ID: 1, Title: "one", Status: "open", DependsOn: []int{2}})
	seedTask(t, rs, gs, Task{ID: 2, Title: "two", Status: "open"})

	cycles, err := gs.FindCycles()
	if err != nil {
		t.Fatalf("FindCycles: %v", err)
	}
	if len(cycles) != 0 {
		t.Errorf("FindCycles() = %v, want none", cycles)
	}
}

// TestGitStore_TaskRefOIDs backs pkg/webui's poll-based watcher (task 66
// phase 9): it must return exactly the task refs (never ConfigRef or
// anything else under RefNamespace), and the oid for a given ref name must
// actually change when that task is mutated, so a caller diffing successive
// snapshots reliably detects the change.
func TestGitStore_TaskRefOIDs(t *testing.T) {
	gs, rs, _ := newGitStoreRepo(t)

	oids, err := gs.TaskRefOIDs()
	if err != nil {
		t.Fatalf("TaskRefOIDs on an empty repo: %v", err)
	}
	if len(oids) != 0 {
		t.Fatalf("TaskRefOIDs on an empty repo = %v, want none", oids)
	}

	seedTask(t, rs, gs, Task{ID: 1, Title: "one", Status: "open"})
	seedTask(t, rs, gs, Task{ID: 2, Title: "two", Status: "open"})
	// A config-ref write must never appear in TaskRefOIDs' result.
	if err := gs.SetConfig(Config{RemoteLocking: true}); err != nil {
		t.Fatalf("SetConfig: %v", err)
	}

	oids, err = gs.TaskRefOIDs()
	if err != nil {
		t.Fatalf("TaskRefOIDs: %v", err)
	}
	if len(oids) != 2 {
		t.Fatalf("TaskRefOIDs() = %v, want exactly 2 task refs", oids)
	}
	before, ok := oids[gs.TaskRef(1)]
	if !ok || before == "" {
		t.Fatalf("TaskRefOIDs() missing %s: %v", gs.TaskRef(1), oids)
	}
	if _, ok := oids[ConfigRef]; ok {
		t.Errorf("TaskRefOIDs() must not include %s, got %v", ConfigRef, oids)
	}

	// Mutating task 1 must change its oid...
	if _, err := gs.Update(1, func(task *Task) (bool, error) {
		task.Title = "one, renamed"
		return true, nil
	}); err != nil {
		t.Fatalf("Update: %v", err)
	}
	oids, err = gs.TaskRefOIDs()
	if err != nil {
		t.Fatalf("TaskRefOIDs after Update: %v", err)
	}
	after, ok := oids[gs.TaskRef(1)]
	if !ok {
		t.Fatalf("TaskRefOIDs() after Update missing %s: %v", gs.TaskRef(1), oids)
	}
	if after == before {
		t.Errorf("oid for %s did not change after Update: %s", gs.TaskRef(1), after)
	}

	// ...and a third task must add a third entry, task 2's oid unaffected.
	seedTask(t, rs, gs, Task{ID: 3, Title: "three", Status: "open"})
	oids, err = gs.TaskRefOIDs()
	if err != nil {
		t.Fatalf("TaskRefOIDs after a third task: %v", err)
	}
	if len(oids) != 3 {
		t.Fatalf("TaskRefOIDs() after a third task = %v, want 3 entries", oids)
	}
}
