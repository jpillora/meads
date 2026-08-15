package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jpillora/meads/pkg/meads"
)

// Tests for `md convert` (git mode phase 9, TASKS #66): the original
// file<->file behaviour (unchanged) plus the new --to-git/--from-git
// migration directions. Both new directions must preserve ids exactly,
// including soft-deleted tasks - see convert.go's runToGit/runFromGit.

// fixtureTask builds a Task the way `md add`/`md update` would: populating
// Meta via SetStatus, not just the Status struct field - markdown
// formatting (FormatTask, pkg/meads/markdown.go) reads status/priority/
// depends-on/etc from Meta, not from the struct fields directly, so a
// fixture that skips this (as a bare struct literal would) silently loses
// those fields on the very next FormatFile call. Mirrors
// pkg/meads/gitstore_test.go's readyFixture, which documents the same
// convention.
func fixtureTask(id int, title, status string) meads.Task {
	t := meads.Task{ID: id, Title: title}
	t.SetStatus(status)
	return t
}

// --- file -> file: unchanged ---

func TestConvert_FileToFile_MdToCsv_Unchanged(t *testing.T) {
	h := newHarness(t)
	h.addTask("Task A")
	h.addTask("Task B")

	if err := (&convertCmd{globals: h.globals, File: h.globals.TasksFile}).Run(); err != nil {
		t.Fatalf("convert: %v", err)
	}

	csvPath := strings.TrimSuffix(h.globals.TasksFile, ".md") + ".csv"
	dst := meads.NewFileStore(csvPath)
	tasks, err := dst.Get(nil)
	if err != nil || len(tasks) != 2 {
		t.Fatalf("converted tasks = %v, err=%v, want 2", tasks, err)
	}
}

func TestConvert_FileToFile_CsvToMd_Unchanged(t *testing.T) {
	h := newCSVHarness(t)
	h.addTask("From CSV")

	if err := (&convertCmd{globals: h.globals, File: h.globals.TasksFile}).Run(); err != nil {
		t.Fatalf("convert: %v", err)
	}

	mdPath := strings.TrimSuffix(h.globals.TasksFile, ".csv") + ".md"
	dst := meads.NewFileStore(mdPath)
	tasks, err := dst.Get(nil)
	if err != nil || len(tasks) != 1 || tasks[0].Title != "From CSV" {
		t.Fatalf("converted tasks = %v, err=%v, want [From CSV]", tasks, err)
	}
}

// --- file -> git ---

func TestConvert_FileToGit_PreservesIDsIncludingDeleted(t *testing.T) {
	h := newHarness(t)
	gapTask := fixtureTask(5, "Active gap id", "inprogress")
	gapTask.SetPriority("P1")
	deletedTask := fixtureTask(7, "Deleted gap id", "closed")
	deletedTask.Deleted = true // Deleted is synthesized by FormatTask directly, not via a Set* method - see markdown.go
	raw := meads.FormatFile(meads.File{Tasks: []meads.Task{
		fixtureTask(1, "Active low id", "open"),
		gapTask,
		deletedTask,
	}})
	if err := os.WriteFile(h.globals.TasksFile, []byte(raw), 0644); err != nil {
		t.Fatal(err)
	}

	if err := (&convertCmd{globals: h.globals, File: h.globals.TasksFile, ToGit: true}).Run(); err != nil {
		t.Fatalf("convert --to-git: %v", err)
	}

	gs := meads.NewGitStore(h.globals.git())
	all, err := gs.LoadAll()
	if err != nil || len(all) != 3 {
		t.Fatalf("GitStore.LoadAll() = %v, err=%v, want 3 tasks", all, err)
	}
	byID := map[int]meads.Task{}
	for _, task := range all {
		byID[task.ID] = task
	}
	if byID[1].Title != "Active low id" {
		t.Errorf("task 1 = %+v", byID[1])
	}
	if byID[5].Title != "Active gap id" || byID[5].Priority != "P1" {
		t.Errorf("task 5 = %+v", byID[5])
	}
	if !byID[7].Deleted || byID[7].Title != "Deleted gap id" {
		t.Errorf("task 7 = %+v, want deleted", byID[7])
	}
	// Get() (active-only) must exclude the deleted one.
	active, err := gs.Get(nil)
	if err != nil || len(active) != 2 {
		t.Fatalf("GitStore.Get(nil) = %v, err=%v, want 2 active tasks", active, err)
	}
	// A fresh Create must not reuse id 7, the highest imported id (even
	// though it's deleted).
	created, err := gs.Create(meads.Task{Title: "next"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if created.ID != 8 {
		t.Errorf("id after migration = %d, want 8", created.ID)
	}
}

func TestConvert_FileToGit_FromCSV_PreservesIDsIncludingDeleted(t *testing.T) {
	h := newCSVHarness(t)
	id1 := h.addTask("CSV active")
	id2 := h.addTask("CSV deleted")
	h.deleteTask(id2) // id2 is the highest id, so pruneTombstones keeps a real tombstone row

	if err := (&convertCmd{globals: h.globals, File: h.globals.TasksFile, ToGit: true}).Run(); err != nil {
		t.Fatalf("convert --to-git: %v", err)
	}

	gs := meads.NewGitStore(h.globals.git())
	all, err := gs.LoadAll()
	if err != nil || len(all) != 2 {
		t.Fatalf("GitStore.LoadAll() = %v, err=%v, want 2 tasks", all, err)
	}
	byID := map[int]meads.Task{}
	for _, task := range all {
		byID[task.ID] = task
	}
	if byID[id1].Title != "CSV active" || byID[id1].Deleted {
		t.Errorf("task %d = %+v", id1, byID[id1])
	}
	if !byID[id2].Deleted {
		t.Errorf("task %d = %+v, want deleted", id2, byID[id2])
	}
}

// --- file -> git: recovering tasks pruned from the file by auto-delete (TASKS #70) ---

// TestConvert_FileToGit_RecoversTaskPrunedFromHistory is task 70's core
// regression test. `md auto-delete` prunes closed tasks out of TASKS.md
// entirely once committed (see cmd/md/auto_delete.go, pkg/meads/mutate.go's
// AutoClean) - they survive only in git history. A migration that only reads
// the working file would silently drop such a task and free its id for
// reuse. --to-git must recover it from history and import it soft-deleted:
// its ref must exist (so the id stays reserved) but it must not resurrect as
// live work.
func TestConvert_FileToGit_RecoversTaskPrunedFromHistory(t *testing.T) {
	h := newHarness(t)
	keep := h.addTask("Keep me")
	prune := h.addTask("Prune me")
	h.closeTask(prune)
	h.commit("add tasks") // both ids committed at HEAD

	// Simulate what the auto-delete pre-commit hook does on the next commit:
	// AutoClean prunes any closed task already committed at HEAD out of the
	// working file entirely.
	if _, err := h.store.AutoClean(h.globals.git()); err != nil {
		t.Fatalf("AutoClean: %v", err)
	}
	h.assertTaskNotExists(prune) // precondition: gone from the working file

	if err := (&convertCmd{globals: h.globals, File: h.globals.TasksFile, ToGit: true}).Run(); err != nil {
		t.Fatalf("convert --to-git: %v", err)
	}

	gs := meads.NewGitStore(h.globals.git())
	all, err := gs.LoadAll()
	if err != nil || len(all) != 2 {
		t.Fatalf("GitStore.LoadAll() = %v, err=%v, want 2 tasks (the pruned id must still be there)", all, err)
	}
	byID := map[int]meads.Task{}
	for _, task := range all {
		byID[task.ID] = task
	}
	if byID[keep].Deleted || byID[keep].Title != "Keep me" {
		t.Errorf("task %d = %+v, want live \"Keep me\"", keep, byID[keep])
	}
	if !byID[prune].Deleted || byID[prune].Title != "Prune me" {
		t.Errorf("task %d = %+v, want deleted \"Prune me\" (recovered from history)", prune, byID[prune])
	}

	// `md get <prune>` (GetWithHistory) must still resolve it post-migration.
	got, err := gs.GetWithHistory([]int{prune})
	if err != nil || len(got) != 1 || got[0].ID != prune {
		t.Fatalf("GetWithHistory(%d) = %+v, err=%v, want the recovered task to resolve", prune, got, err)
	}
	// Get (active-only) must still exclude it.
	active, err := gs.Get(nil)
	if err != nil || len(active) != 1 || active[0].ID != keep {
		t.Fatalf("GitStore.Get(nil) = %v, err=%v, want only the live task %d", active, err, keep)
	}

	// The id must be reserved: a fresh Create must not reuse it.
	created, err := gs.Create(meads.Task{Title: "next"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if created.ID <= prune {
		t.Errorf("Create after migration = id %d, want an id greater than the reserved/pruned id %d", created.ID, prune)
	}
}

// TestConvert_FileToGit_FileVersionWinsOverHistory locks in TASKS #70's
// merge rule: when an id is present in BOTH the working file and history
// (its committed version has since been edited, not deleted/pruned), the
// working file's version - the newer one - must win.
func TestConvert_FileToGit_FileVersionWinsOverHistory(t *testing.T) {
	h := newHarness(t)
	id := h.addTask("Old title")
	h.commit("add task") // history now has "Old title"

	if err := h.store.Update(id, func(t *meads.Task) { t.Title = "New title" }); err != nil {
		t.Fatalf("update: %v", err)
	}
	// The working file now differs from history but the change is uncommitted.

	if err := (&convertCmd{globals: h.globals, File: h.globals.TasksFile, ToGit: true}).Run(); err != nil {
		t.Fatalf("convert --to-git: %v", err)
	}

	gs := meads.NewGitStore(h.globals.git())
	got, err := gs.Get([]int{id})
	if err != nil || len(got) != 1 {
		t.Fatalf("GitStore.Get(%d) = %v, err=%v", id, got, err)
	}
	if got[0].Title != "New title" {
		t.Errorf("task %d title = %q, want %q (the working file's newer version, not history's)", id, got[0].Title, "New title")
	}
	if got[0].Deleted {
		t.Errorf("task %d = %+v, want not deleted", id, got[0])
	}
}

// TestConvert_FileToGit_TasksFileNeverCommitted_DegradesGracefully: the repo
// has commits (so --to-git's own git-repo precondition passes, and there is
// real history for the history walk to traverse), but the tasks file itself
// was never committed. `git log --all -- <file>` for an uncommitted path
// simply returns nothing (not an error, see collectFirstSeen/
// recoverFromHistory), so the walk must find zero recoveries and migration
// proceeds using only the working file, exactly as before this fix.
func TestConvert_FileToGit_TasksFileNeverCommitted_DegradesGracefully(t *testing.T) {
	h := newHarness(t) // has one commit ("initial"), but never for TASKS.md
	h.addTask("Only task")

	if err := (&convertCmd{globals: h.globals, File: h.globals.TasksFile, ToGit: true}).Run(); err != nil {
		t.Fatalf("convert --to-git with an uncommitted tasks file: %v", err)
	}

	gs := meads.NewGitStore(h.globals.git())
	all, err := gs.LoadAll()
	if err != nil || len(all) != 1 {
		t.Fatalf("GitStore.LoadAll() = %v, err=%v, want exactly the 1 working-file task, nothing recovered", all, err)
	}
}

// TestConvert_FileToGit_FreshRepoNoCommits_NoCrash: a git repository that
// exists (so it passes --to-git's own git-repo precondition) but has made
// zero commits yet - not even the harness's usual "initial" commit - must
// not crash the history walk.
func TestConvert_FileToGit_FreshRepoNoCommits_NoCrash(t *testing.T) {
	dir := t.TempDir()
	initCmd := exec.Command("git", "init", "-q", "-b", "main")
	initCmd.Dir = dir
	if out, err := initCmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	g := &globals{Git: &meads.ExecGit{Dir: dir}, Dir: dir, TasksFile: filepath.Join(dir, "TASKS.md")}
	seed := fixtureTask(1, "Only task", "open")
	raw := meads.FormatFile(meads.File{Tasks: []meads.Task{seed}})
	if err := os.WriteFile(g.TasksFile, []byte(raw), 0644); err != nil {
		t.Fatal(err)
	}

	if err := (&convertCmd{globals: g, File: g.TasksFile, ToGit: true}).Run(); err != nil {
		t.Fatalf("convert --to-git in a commit-less repo: %v", err)
	}

	gs := meads.NewGitStore(g.git())
	got, err := gs.Get([]int{1})
	if err != nil || len(got) != 1 || got[0].Title != "Only task" {
		t.Fatalf("GitStore.Get(1) = %+v, err=%v", got, err)
	}
}

func TestConvert_FileToGit_RefusesNonEmptyGitMode(t *testing.T) {
	h := newHarness(t)
	h.addTask("Task A")
	gs := meads.NewGitStore(h.globals.git())
	if _, err := gs.Create(meads.Task{Title: "already there"}); err != nil {
		t.Fatalf("seeding a git-mode task: %v", err)
	}

	err := (&convertCmd{globals: h.globals, File: h.globals.TasksFile, ToGit: true}).Run()
	if err == nil {
		t.Fatal("convert --to-git into a non-empty git mode should error, got nil")
	}
	if !strings.Contains(err.Error(), "already has tasks") {
		t.Errorf("error = %q, want it to mention the namespace already has tasks", err.Error())
	}
	// The pre-existing git-mode task must be untouched.
	tasks, err := gs.Get(nil)
	if err != nil || len(tasks) != 1 {
		t.Fatalf("git-mode tasks after a refused migration = %v, err=%v, want the original 1 untouched", tasks, err)
	}
}

func TestConvert_FileToGit_OutsideGitRepo_Errors(t *testing.T) {
	dir := t.TempDir()
	g := &globals{Git: &meads.ExecGit{Dir: dir}, Dir: dir}
	tasksFile := filepath.Join(dir, "TASKS.md")
	if err := os.WriteFile(tasksFile, []byte(meads.FormatFile(meads.File{})), 0644); err != nil {
		t.Fatal(err)
	}
	err := (&convertCmd{globals: g, File: tasksFile, ToGit: true}).Run()
	if err == nil || !strings.Contains(err.Error(), "requires a git repository") {
		t.Fatalf("convert --to-git outside a git repo = %v, want a clear error", err)
	}
}

// --- git -> file ---

func TestConvert_GitToFile_RoundTrips(t *testing.T) {
	h := newHarness(t)
	gs := meads.NewGitStore(h.globals.git())
	if _, err := gs.Create(meads.Task{Title: "Active", Status: "open", Priority: "P1"}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	toDelete, err := gs.Create(meads.Task{Title: "Will be deleted", Status: "open"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := gs.SoftDelete(toDelete.ID); err != nil {
		t.Fatalf("SoftDelete: %v", err)
	}

	dstFile := filepath.Join(h.dir, "exported.md")
	if err := (&convertCmd{globals: h.globals, File: dstFile, FromGit: true}).Run(); err != nil {
		t.Fatalf("convert --from-git: %v", err)
	}

	dst := meads.NewFileStore(dstFile)
	all, err := dst.GetAll()
	if err != nil || len(all) != 2 {
		t.Fatalf("GetAll() on exported file = %v, err=%v, want 2 tasks", all, err)
	}
	byID := map[int]meads.Task{}
	for _, task := range all {
		byID[task.ID] = task
	}
	if byID[1].Title != "Active" || byID[1].Priority != "P1" {
		t.Errorf("task 1 = %+v", byID[1])
	}
	if !byID[toDelete.ID].Deleted || byID[toDelete.ID].Title != "Will be deleted" {
		t.Errorf("task %d = %+v, want deleted", toDelete.ID, byID[toDelete.ID])
	}
	active, err := dst.Get(nil)
	if err != nil || len(active) != 1 {
		t.Fatalf("Get(nil) on exported file = %v, err=%v, want 1 active task", active, err)
	}
}

// TestConvert_GitToFile_AgentIDFilesInScope_ArePreserved documents and locks
// in this phase's chosen behaviour: AgentID/FilesInScope (set only by
// GitStore.Claim today - gitmutate.go) are wired into BOTH the markdown and
// CSV formatters as regular optional fields (see markdown.go's
// FormatTask/parseTask and csv.go's FormatCSV/ParseCSV, and their own
// round-trip tests), so a claimed task's fields survive a git->file
// conversion rather than being silently dropped.
func TestConvert_GitToFile_AgentIDFilesInScope_ArePreserved(t *testing.T) {
	h := newHarness(t)
	gs := meads.NewGitStore(h.globals.git())
	created, err := gs.Create(meads.Task{Title: "Claimable", Status: "open"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	filesInScope := []string{"pkg/meads/gitstore.go", "cmd/md/webui.go"}
	if _, err := gs.Claim(created.ID, "agent-7", filesInScope); err != nil {
		t.Fatalf("Claim: %v", err)
	}

	// Markdown destination.
	mdFile := filepath.Join(h.dir, "claimed.md")
	if err := (&convertCmd{globals: h.globals, File: mdFile, FromGit: true}).Run(); err != nil {
		t.Fatalf("convert --from-git (md): %v", err)
	}
	assertAgentFieldsPreserved(t, meads.NewFileStore(mdFile), created.ID, "agent-7", filesInScope)

	// CSV destination, from the same git-mode source.
	csvFile := filepath.Join(h.dir, "claimed.csv")
	if err := (&convertCmd{globals: h.globals, File: csvFile, FromGit: true}).Run(); err != nil {
		t.Fatalf("convert --from-git (csv): %v", err)
	}
	assertAgentFieldsPreserved(t, meads.NewFileStore(csvFile), created.ID, "agent-7", filesInScope)
}

func assertAgentFieldsPreserved(t *testing.T, dst *meads.Store, id int, wantAgent string, wantFiles []string) {
	t.Helper()
	got, err := dst.Get([]int{id})
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got[0].AgentID != wantAgent {
		t.Errorf("%s: AgentID = %q, want %q (preserved, not dropped)", dst.Path(), got[0].AgentID, wantAgent)
	}
	if len(got[0].FilesInScope) != len(wantFiles) {
		t.Fatalf("%s: FilesInScope = %v, want %v", dst.Path(), got[0].FilesInScope, wantFiles)
	}
	for i, f := range wantFiles {
		if got[0].FilesInScope[i] != f {
			t.Errorf("%s: FilesInScope = %v, want %v (preserved, not dropped)", dst.Path(), got[0].FilesInScope, wantFiles)
		}
	}
}

// TestConvert_GitToFile_ClaimStatusNotStale guards the reason FormatTask
// reads status/priority/type/... from the dedicated struct field rather than
// from Meta (taskMetaForFormat, TASKS #92): Task.MarshalJSON strips every
// known meta key out of the "meta" JSON object it writes, and there is no
// custom UnmarshalJSON to reconstruct them, so ANY task read back from git
// (GitStore.Get/LoadAll) has an empty Meta for those keys. Claim is used here
// as the concrete illustration (it also sets Status directly, bypassing
// SetStatus, unlike every CLI-reachable mutation), and its result - Status
// "inprogress" with a Meta["status"] that never even reflects "open" - is
// exactly the shape the converted file must still get right.
func TestConvert_GitToFile_ClaimStatusNotStale(t *testing.T) {
	h := newHarness(t)
	gs := meads.NewGitStore(h.globals.git())
	seed := meads.Task{Title: "Will be claimed"}
	seed.SetStatus("open")
	created, err := gs.Create(seed)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := gs.Claim(created.ID, "agent-1", nil); err != nil {
		t.Fatalf("Claim: %v", err)
	}
	// Confirm the precondition this test is about: a git-mode read never
	// carries Meta["status"] at all, so the struct field is the ONLY
	// authoritative source of truth by the time convert sees it.
	preConvert, err := gs.Get([]int{created.ID})
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if preConvert[0].Status != "inprogress" {
		t.Fatalf("precondition: claimed task Status = %q, want inprogress", preConvert[0].Status)
	}
	if preConvert[0].Meta["status"] != "" {
		t.Fatalf("precondition: expected Meta[\"status\"] empty after a git-mode read, got %q", preConvert[0].Meta["status"])
	}

	dstFile := filepath.Join(h.dir, "claimed-status.md")
	if err := (&convertCmd{globals: h.globals, File: dstFile, FromGit: true}).Run(); err != nil {
		t.Fatalf("convert --from-git: %v", err)
	}

	got, err := meads.NewFileStore(dstFile).Get([]int{created.ID})
	if err != nil {
		t.Fatalf("Get on converted file: %v", err)
	}
	if got[0].Status != "inprogress" {
		t.Errorf("converted task Status = %q, want %q (the task's real current status, not Meta's stale pre-claim value)", got[0].Status, "inprogress")
	}
}

func TestConvert_GitToFile_RefusesExistingFile(t *testing.T) {
	h := newHarness(t)
	h.addTask("Existing")
	gs := meads.NewGitStore(h.globals.git())
	if _, err := gs.Create(meads.Task{Title: "git task"}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	err := (&convertCmd{globals: h.globals, File: h.globals.TasksFile, FromGit: true}).Run()
	if err == nil {
		t.Fatal("convert --from-git into an existing file should error, got nil")
	}
	if !strings.Contains(err.Error(), "already exists") {
		t.Errorf("error = %q, want it to mention \"already exists\"", err.Error())
	}
	// The original file content must be untouched.
	tasks, _ := meads.NewFileStore(h.globals.TasksFile).Get(nil)
	if len(tasks) != 1 || tasks[0].Title != "Existing" {
		t.Errorf("existing file was modified: %v", tasks)
	}
}

func TestConvert_GitToFile_OutsideGitRepo_Errors(t *testing.T) {
	dir := t.TempDir()
	g := &globals{Git: &meads.ExecGit{Dir: dir}, Dir: dir}
	err := (&convertCmd{globals: g, File: filepath.Join(dir, "out.md"), FromGit: true}).Run()
	if err == nil || !strings.Contains(err.Error(), "requires a git repository") {
		t.Fatalf("convert --from-git outside a git repo = %v, want a clear error", err)
	}
}

// --- flag validation ---

func TestConvert_BothToGitAndFromGit_Errors(t *testing.T) {
	h := newHarness(t)
	err := (&convertCmd{globals: h.globals, File: h.globals.TasksFile, ToGit: true, FromGit: true}).Run()
	if err == nil {
		t.Fatal("convert with both --to-git and --from-git should error, got nil")
	}
	if !strings.Contains(err.Error(), "cannot use both") {
		t.Errorf("error = %q, want it to mention the conflict", err.Error())
	}
}

// --- round trip: file -> git -> file, no data lost ---

func TestConvert_RoundTrip_FileToGitToFile_NoDataLoss(t *testing.T) {
	h := newHarness(t)
	one := fixtureTask(1, "One", "open")
	one.SetPriority("P0")
	three := fixtureTask(3, "Three, blocked", "open")
	three.SetDependsOn([]int{1})
	nine := fixtureTask(9, "Nine, deleted", "closed")
	nine.Deleted = true // Deleted is synthesized by FormatTask directly, not via a Set* method - see markdown.go
	raw := meads.FormatFile(meads.File{Tasks: []meads.Task{one, three, nine}})
	if err := os.WriteFile(h.globals.TasksFile, []byte(raw), 0644); err != nil {
		t.Fatal(err)
	}

	if err := (&convertCmd{globals: h.globals, File: h.globals.TasksFile, ToGit: true}).Run(); err != nil {
		t.Fatalf("convert --to-git: %v", err)
	}

	roundTripped := filepath.Join(h.dir, "roundtrip.md")
	if err := (&convertCmd{globals: h.globals, File: roundTripped, FromGit: true}).Run(); err != nil {
		t.Fatalf("convert --from-git: %v", err)
	}

	dst := meads.NewFileStore(roundTripped)
	all, err := dst.GetAll()
	if err != nil || len(all) != 3 {
		t.Fatalf("GetAll() on round-tripped file = %v, err=%v, want 3 tasks", all, err)
	}
	byID := map[int]meads.Task{}
	for _, task := range all {
		byID[task.ID] = task
	}
	if byID[1].Title != "One" || byID[1].Priority != "P0" {
		t.Errorf("task 1 = %+v", byID[1])
	}
	if byID[3].Title != "Three, blocked" || len(byID[3].DependsOn) != 1 || byID[3].DependsOn[0] != 1 {
		t.Errorf("task 3 = %+v", byID[3])
	}
	if !byID[9].Deleted || byID[9].Title != "Nine, deleted" {
		t.Errorf("task 9 = %+v, want deleted", byID[9])
	}
}
