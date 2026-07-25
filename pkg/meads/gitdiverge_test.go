package meads

import "testing"

// Tests for GitStore.Diverged (git mode phase 8, TASKS #65): detecting
// edit/edit conflicts between local task state and the last `git fetch`'s
// remote-tracking copy (RemoteTasksRefPrefix). Unlike gitdoctor_test.go,
// which seeds refs/meads-remote/* directly (Doctor only cares about current
// ref state, not how it got there), these tests exercise real two-clone/
// bare-remote setups with actual `git push`/`git fetch` - what's under test
// here (a real non-fast-forward divergence, and above all that a real fetch
// never clobbers local state) is precisely what direct ref seeding would
// rubber-stamp without exercising. Mirrors cmd/md/push_test.go's
// setupDivergedGitModeClones, but at the GitStore/RefStore level rather
// than through cmd/md's CLI commands, since Diverged and the fetch refspec
// it depends on are pkg/meads concerns.

// --- helpers ---

// cloneRepo is one git-mode clone under test: a GitStore/RefStore pair
// rooted at dir.
type cloneRepo struct {
	gs  *GitStore
	rs  *RefStore
	dir string
}

// newBareRemote creates a bare git repo under t.TempDir() to act as
// "origin" for the two-clone tests below.
func newBareRemote(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	runGit(t, dir, "init", "--bare", "-b", "main")
	return dir
}

// cloneOf clones bareDir into a fresh t.TempDir() and returns a GitStore/
// RefStore pair rooted there. It also configures the SAME kind of fetch
// refspec `md init --git` configures in production
// (cmd/md/init.go's meadsFetchRefspec) - built from this package's own
// RefNamespace/RemoteRefNamespace constants rather than a hardcoded
// literal, so a future change to either constant can't silently drift out
// of sync with what these tests exercise - so a plain `git fetch origin`
// (no explicit refspec argument, see fetchMeadsRefs) behaves exactly as it
// would for a real user.
func cloneOf(t *testing.T, bareDir string) cloneRepo {
	t.Helper()
	dir := t.TempDir()
	runGit(t, "", "clone", bareDir, dir)
	runGit(t, dir, "config", "--add", "remote.origin.fetch", "+"+RefNamespace+"*:"+RemoteRefNamespace+"*")
	git := &ExecGit{Dir: dir}
	return cloneRepo{gs: NewGitStore(git), rs: NewRefStore(git), dir: dir}
}

// pushMeadsRefs pushes c's entire refs/meads/* namespace to origin - a
// plain, non-force push, exactly like cmd/md/push.go's pushRefspec: meads
// never force-pushes, so a rejected push here means the same thing it would
// in production.
func pushMeadsRefs(t *testing.T, c cloneRepo) {
	t.Helper()
	runGit(t, c.dir, "push", "origin", RefNamespace+"*:"+RefNamespace+"*")
}

// fetchMeadsRefs runs a plain `git fetch origin` in c, relying entirely on
// the fetch refspec configured by cloneOf - exactly like a real user running
// `git fetch` with no further arguments.
func fetchMeadsRefs(t *testing.T, c cloneRepo) {
	t.Helper()
	runGit(t, c.dir, "fetch", "origin")
}

// bootstrapLocalTask copies id from the bare remote directly into c's LOCAL
// refs/meads/tasks/<id> via an explicit, non-wildcard `git fetch
// origin <ref>:<ref>`. This is safe regardless of the wildcard refspec's own
// safety properties, since the local ref does not exist yet - there is
// nothing to clobber the first time. It simulates a clone "pulling in" a
// task it doesn't have yet - real support for that (turning fetched
// remote-tracking state into local state) is future work, deliberately out
// of scope for Diverged (see its doc comment); tests use this narrow,
// always-safe form purely to construct a realistic shared starting point
// for two clones to then diverge from.
func bootstrapLocalTask(t *testing.T, c cloneRepo, id int) {
	t.Helper()
	ref := c.gs.TaskRef(id)
	runGit(t, c.dir, "fetch", "origin", ref+":"+ref)
}

// --- 1. the core scenario: two clones diverge one task; a second,
// untouched task stays a clean fast-forward and must not be reported ---

func TestGitStore_Diverged_ReportsExactlyTheDivergedTaskWithCorrectMergeBase(t *testing.T) {
	bareDir := newBareRemote(t)
	c1 := cloneOf(t, bareDir)
	c2 := cloneOf(t, bareDir)

	shared, err := c1.gs.Create(Task{Title: "shared task", Status: "open"})
	if err != nil {
		t.Fatalf("Create(shared): %v", err)
	}
	untouched, err := c1.gs.Create(Task{Title: "untouched task", Status: "open"})
	if err != nil {
		t.Fatalf("Create(untouched): %v", err)
	}
	pushMeadsRefs(t, c1)

	// clone2 bootstraps its own local copies of both tasks from what clone1
	// has pushed so far - the shared starting point both sides will diverge
	// from.
	bootstrapLocalTask(t, c2, shared.ID)
	bootstrapLocalTask(t, c2, untouched.ID)
	baseOID, err := c2.rs.ResolveRef(c2.gs.TaskRef(shared.ID))
	if err != nil {
		t.Fatalf("ResolveRef(shared) after bootstrap: %v", err)
	}

	// clone1 moves BOTH tasks on and pushes.
	if _, err := c1.gs.Update(shared.ID, func(task *Task) (bool, error) {
		task.Title = "clone1's edit"
		return true, nil
	}); err != nil {
		t.Fatalf("clone1 update(shared): %v", err)
	}
	if _, err := c1.gs.Update(untouched.ID, func(task *Task) (bool, error) {
		task.Title = "clone1 moved this one on too"
		return true, nil
	}); err != nil {
		t.Fatalf("clone1 update(untouched): %v", err)
	}
	pushMeadsRefs(t, c1)

	// clone2 independently edits ONLY the shared task - "untouched" stays
	// exactly at its bootstrapped commit, so once fetched it will be a pure
	// fast-forward, not a divergence.
	if _, err := c2.gs.Update(shared.ID, func(task *Task) (bool, error) {
		task.Title = "clone2's edit"
		return true, nil
	}); err != nil {
		t.Fatalf("clone2 update(shared): %v", err)
	}
	localOID, err := c2.rs.ResolveRef(c2.gs.TaskRef(shared.ID))
	if err != nil {
		t.Fatalf("ResolveRef(shared) after clone2 edit: %v", err)
	}

	// clone2 fetches - landing in refs/meads-remote/*, never touching
	// refs/meads/* (see TestGitStore_Diverged_FetchDoesNotClobberLocalUnpushedWork
	// below for a dedicated check of that).
	fetchMeadsRefs(t, c2)

	diverged, err := c2.gs.Diverged()
	if err != nil {
		t.Fatalf("Diverged: %v", err)
	}
	if len(diverged) != 1 {
		t.Fatalf("Diverged() = %+v, want exactly one divergence (task %d)", diverged, shared.ID)
	}
	d := diverged[0]
	if d.ID != shared.ID {
		t.Errorf("diverged task id = %d, want %d", d.ID, shared.ID)
	}
	if d.MergeBase != baseOID {
		t.Errorf("MergeBase = %s, want the shared starting commit %s", d.MergeBase, baseOID)
	}
	if d.LocalOID != localOID {
		t.Errorf("LocalOID = %s, want clone2's own edit %s", d.LocalOID, localOID)
	}
	if d.Local.Title != "clone2's edit" {
		t.Errorf("Local.Title = %q, want clone2's own edit", d.Local.Title)
	}
	if d.Remote.Title != "clone1's edit" {
		t.Errorf("Remote.Title = %q, want clone1's pushed edit", d.Remote.Title)
	}
}

// --- 2. fast-forwards, in either direction, are not divergences ---

func TestGitStore_Diverged_RemoteAheadFastForward_NotReported(t *testing.T) {
	bareDir := newBareRemote(t)
	c1 := cloneOf(t, bareDir)
	c2 := cloneOf(t, bareDir)

	task, err := c1.gs.Create(Task{Title: "v1", Status: "open"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	pushMeadsRefs(t, c1)
	bootstrapLocalTask(t, c2, task.ID)

	// Only clone1 moves the task on; clone2 never touches its local copy.
	if _, err := c1.gs.Update(task.ID, func(tk *Task) (bool, error) {
		tk.Title = "v2"
		return true, nil
	}); err != nil {
		t.Fatalf("clone1 update: %v", err)
	}
	pushMeadsRefs(t, c1)
	fetchMeadsRefs(t, c2)

	diverged, err := c2.gs.Diverged()
	if err != nil {
		t.Fatalf("Diverged: %v", err)
	}
	if len(diverged) != 0 {
		t.Fatalf("Diverged() with remote strictly ahead = %+v, want none (a clean fast-forward)", diverged)
	}
}

func TestGitStore_Diverged_LocalAheadFastForward_NotReported(t *testing.T) {
	bareDir := newBareRemote(t)
	c1 := cloneOf(t, bareDir)
	c2 := cloneOf(t, bareDir)

	task, err := c1.gs.Create(Task{Title: "v1", Status: "open"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	pushMeadsRefs(t, c1)
	bootstrapLocalTask(t, c2, task.ID)

	// clone2 edits locally but never pushes; clone1/the remote never change,
	// so remote-tracking (once fetched) is an ANCESTOR of clone2's local
	// state - clone2 is strictly ahead, not diverged.
	if _, err := c2.gs.Update(task.ID, func(tk *Task) (bool, error) {
		tk.Title = "clone2 local-only edit"
		return true, nil
	}); err != nil {
		t.Fatalf("clone2 update: %v", err)
	}
	fetchMeadsRefs(t, c2)

	diverged, err := c2.gs.Diverged()
	if err != nil {
		t.Fatalf("Diverged: %v", err)
	}
	if len(diverged) != 0 {
		t.Fatalf("Diverged() with local strictly ahead = %+v, want none (a clean fast-forward)", diverged)
	}
}

// --- 3. fetch safety: a plain `git fetch` must never clobber local
// un-pushed work (task 65 phase 8's fetch-refspec fix) ---

func TestGitStore_Diverged_FetchDoesNotClobberLocalUnpushedWork(t *testing.T) {
	bareDir := newBareRemote(t)
	c1 := cloneOf(t, bareDir)
	c2 := cloneOf(t, bareDir)

	task, err := c1.gs.Create(Task{Title: "base", Status: "open"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	pushMeadsRefs(t, c1)
	bootstrapLocalTask(t, c2, task.ID)

	// clone2 makes a LOCAL, never-pushed edit.
	if _, err := c2.gs.Update(task.ID, func(tk *Task) (bool, error) {
		tk.Title = "clone2's un-pushed local work"
		return true, nil
	}); err != nil {
		t.Fatalf("clone2 update: %v", err)
	}
	localOIDBefore, err := c2.rs.ResolveRef(c2.gs.TaskRef(task.ID))
	if err != nil {
		t.Fatalf("ResolveRef before fetch: %v", err)
	}

	// Meanwhile clone1 ALSO changes the same task differently and pushes -
	// so the OLD, unsafe refspec (+refs/meads/*:refs/meads/*, what phase 5
	// originally shipped) would force-overwrite clone2's local ref with
	// clone1's the moment clone2 next ran a plain `git fetch`.
	if _, err := c1.gs.Update(task.ID, func(tk *Task) (bool, error) {
		tk.Title = "clone1's conflicting push"
		return true, nil
	}); err != nil {
		t.Fatalf("clone1 update: %v", err)
	}
	pushMeadsRefs(t, c1)

	fetchMeadsRefs(t, c2)

	localOIDAfter, err := c2.rs.ResolveRef(c2.gs.TaskRef(task.ID))
	if err != nil {
		t.Fatalf("ResolveRef after fetch: %v", err)
	}
	if localOIDAfter != localOIDBefore {
		t.Fatalf("local ref moved from %s to %s after a plain `git fetch` - the fetch refspec clobbered un-pushed local work", localOIDBefore, localOIDAfter)
	}
	got, err := c2.gs.Get([]int{task.ID})
	if err != nil {
		t.Fatalf("Get after fetch: %v", err)
	}
	if got[0].Title != "clone2's un-pushed local work" {
		t.Fatalf("task content after fetch = %q, want clone2's own local edit, untouched", got[0].Title)
	}

	// The fetched remote state IS visible - just in the separate
	// remote-tracking namespace, not silently dropped on the floor.
	remote, _, err := c2.gs.loadAllWithOIDs(RemoteTasksRefPrefix)
	if err != nil {
		t.Fatalf("loadAllWithOIDs(remote): %v", err)
	}
	if remote[task.ID].Title != "clone1's conflicting push" {
		t.Fatalf("remote-tracking content = %+v, want clone1's pushed edit to be visible there", remote[task.ID])
	}
}

// --- 4. MVP safety: Diverged never writes anything, and local state is
// exactly what the caller left it as - no auto-merge ---

func TestGitStore_Diverged_NoAutoMerge_LocalStateUntouched(t *testing.T) {
	bareDir := newBareRemote(t)
	c1 := cloneOf(t, bareDir)
	c2 := cloneOf(t, bareDir)

	task, err := c1.gs.Create(Task{Title: "base", Status: "open"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	pushMeadsRefs(t, c1)
	bootstrapLocalTask(t, c2, task.ID)

	if _, err := c1.gs.Update(task.ID, func(tk *Task) (bool, error) {
		tk.Title = "clone1's edit"
		return true, nil
	}); err != nil {
		t.Fatalf("clone1 update: %v", err)
	}
	pushMeadsRefs(t, c1)

	if _, err := c2.gs.Update(task.ID, func(tk *Task) (bool, error) {
		tk.Title = "clone2's edit"
		return true, nil
	}); err != nil {
		t.Fatalf("clone2 update: %v", err)
	}
	fetchMeadsRefs(t, c2)

	localRefsBefore, err := c2.rs.ListRefs(RefNamespace)
	if err != nil {
		t.Fatalf("ListRefs before Diverged: %v", err)
	}

	diverged, err := c2.gs.Diverged()
	if err != nil {
		t.Fatalf("Diverged: %v", err)
	}
	if len(diverged) != 1 {
		t.Fatalf("Diverged() = %+v, want exactly one (precondition for this test)", diverged)
	}

	localRefsAfter, err := c2.rs.ListRefs(RefNamespace)
	if err != nil {
		t.Fatalf("ListRefs after Diverged: %v", err)
	}
	if len(localRefsAfter) != len(localRefsBefore) {
		t.Fatalf("ref count under %s changed from %d to %d - Diverged must never write a ref", RefNamespace, len(localRefsBefore), len(localRefsAfter))
	}
	for name, oid := range localRefsBefore {
		if localRefsAfter[name] != oid {
			t.Errorf("ref %s moved from %s to %s merely by calling Diverged - it must be a pure read", name, oid, localRefsAfter[name])
		}
	}

	// Local content is STILL exactly clone2's own edit - not silently
	// merged, not overwritten with clone1's version, not blended.
	got, err := c2.gs.Get([]int{task.ID})
	if err != nil {
		t.Fatalf("Get after Diverged: %v", err)
	}
	if got[0].Title != "clone2's edit" {
		t.Fatalf("task content after Diverged = %q, want clone2's own edit untouched (no auto-merge)", got[0].Title)
	}
}

// --- 5. an id that only exists on one side is not a divergence ---

func TestGitStore_Diverged_OneSidedID_NotReported(t *testing.T) {
	bareDir := newBareRemote(t)
	c1 := cloneOf(t, bareDir)
	c2 := cloneOf(t, bareDir)

	remoteOnly, err := c1.gs.Create(Task{Title: "remote only", Status: "open"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	pushMeadsRefs(t, c1)
	fetchMeadsRefs(t, c2) // lands in refs/meads-remote/*; clone2 never bootstraps a local copy

	localOnly, err := c2.gs.Create(Task{Title: "local only", Status: "open"})
	if err != nil {
		t.Fatalf("Create(local-only): %v", err)
	}

	diverged, err := c2.gs.Diverged()
	if err != nil {
		t.Fatalf("Diverged: %v", err)
	}
	for _, d := range diverged {
		if d.ID == remoteOnly.ID {
			t.Errorf("Diverged() reports id %d, which has no local counterpart at all", remoteOnly.ID)
		}
		if d.ID == localOnly.ID {
			t.Errorf("Diverged() reports id %d, which has no remote-tracking counterpart at all", localOnly.ID)
		}
	}
}
