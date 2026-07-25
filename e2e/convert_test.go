package e2e

import (
	"strings"
	"testing"

	"github.com/jpillora/meads/pkg/meads"
)

// Tests for the Store-level primitives `md convert`'s file<->git migration
// (git mode phase 9, TASKS #66) is built on: GetAll (read every task
// including tombstones, unlike Get) and ImportAll (write tasks verbatim,
// preserving ids, into a brand-new file). The CLI-level migration behaviour
// itself (id preservation across the file<->git boundary, --to-git/
// --from-git flag handling) is exercised in cmd/md/convert_test.go, which
// can drive a real git repo; these tests stay at the Store/File level.

// --- GetAll ---

func TestGetAll_FileNotExist(t *testing.T) {
	s := meads.NewStore(newCSVStore(t).FS(), "NONEXISTENT.csv")
	tasks, err := s.GetAll()
	if err != nil {
		t.Fatalf("GetAll on a missing file: %v", err)
	}
	if tasks != nil {
		t.Fatalf("GetAll on a missing file = %v, want nil", tasks)
	}
}

func TestGetAll_IncludesDeleted_CSV(t *testing.T) {
	// CSV keeps a tombstone row for the single highest deleted id, when it
	// exceeds every active id (pruneTombstones) - deleting the
	// highest-numbered task is the natural way to get a REAL soft-deleted
	// row surviving in a live CSV file via the ordinary Store API.
	s := newCSVStore(t)
	id1, err := s.Add(meads.Task{Title: "Active", Status: "open"})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	id2, err := s.Add(meads.Task{Title: "Deleted", Status: "open"})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := s.Delete(id2); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	// Get (the normal read path) must still exclude it.
	active, err := s.Get(nil)
	if err != nil || len(active) != 1 || active[0].ID != id1 {
		t.Fatalf("Get(nil) = %v, err=%v, want only task %d", active, err, id1)
	}

	all, err := s.GetAll()
	if err != nil {
		t.Fatalf("GetAll: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("GetAll() = %v, want 2 tasks (including the tombstone)", all)
	}
	byID := map[int]meads.Task{}
	for _, task := range all {
		byID[task.ID] = task
	}
	if byID[id1].Deleted {
		t.Errorf("task %d should not be deleted", id1)
	}
	if !byID[id2].Deleted {
		t.Errorf("task %d should be deleted", id2)
	}
	if byID[id2].Title != "Deleted" {
		t.Errorf("deleted task title = %q, want preserved %q", byID[id2].Title, "Deleted")
	}
}

func TestGetAll_IncludesDeleted_Markdown(t *testing.T) {
	// Markdown always prunes tombstone rows on every mutation
	// (pruneTombstones), so a genuinely soft-deleted row surviving in a live
	// TASKS.md can't be produced through the ordinary Store API - write raw
	// content directly instead (mirrors get_with_history_test.go's
	// writeWorking), the same shape `md convert --from-git` would produce
	// via ImportAll for a task that started out deleted.
	s := newMDStore(t)
	raw := meads.FormatFile(meads.File{Tasks: []meads.Task{
		{ID: 1, Title: "Active", Status: "open"},
		{ID: 2, Title: "Tombstone", Status: "closed", Deleted: true},
	}})
	writeWorking(t, s, raw)

	active, err := s.Get(nil)
	if err != nil || len(active) != 1 || active[0].ID != 1 {
		t.Fatalf("Get(nil) = %v, err=%v, want only task 1", active, err)
	}

	all, err := s.GetAll()
	if err != nil {
		t.Fatalf("GetAll: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("GetAll() = %v, want 2 tasks (including the tombstone)", all)
	}
	if !all[1].Deleted || all[1].Title != "Tombstone" {
		t.Errorf("all[1] = %+v, want a deleted task titled Tombstone", all[1])
	}
}

// --- ImportAll ---

func TestImportAll_PreservesIDsAndDeleted_CSV(t *testing.T) {
	s := newCSVStore(t)
	tasks := []meads.Task{
		{ID: 1, Title: "First", Status: "open", Priority: "P1"},
		{ID: 5, Title: "Gap-id, active", Status: "inprogress"},
		{ID: 7, Title: "Gap-id, deleted", Status: "closed", Deleted: true},
	}
	if err := s.ImportAll(tasks); err != nil {
		t.Fatalf("ImportAll: %v", err)
	}

	all, err := s.GetAll()
	if err != nil {
		t.Fatalf("GetAll after ImportAll: %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("GetAll after ImportAll = %v, want 3 tasks", all)
	}
	byID := map[int]meads.Task{}
	for _, task := range all {
		byID[task.ID] = task
	}
	for _, want := range tasks {
		got, ok := byID[want.ID]
		if !ok {
			t.Fatalf("task %d missing after ImportAll", want.ID)
		}
		if got.Title != want.Title || got.Deleted != want.Deleted {
			t.Errorf("task %d = %+v, want title=%q deleted=%v", want.ID, got, want.Title, want.Deleted)
		}
	}

	// The next allocated id must account for the gap-id deleted task (7),
	// not just the highest active one (5) - nextID scans every row
	// regardless of Deleted (see ImportAll's doc comment).
	id, err := s.Add(meads.Task{Title: "Next", Status: "open"})
	if err != nil {
		t.Fatalf("Add after ImportAll: %v", err)
	}
	if id != 8 {
		t.Errorf("id after ImportAll = %d, want 8 (past the imported tombstone at 7)", id)
	}
}

func TestImportAll_PreservesIDsAndDeleted_Markdown(t *testing.T) {
	s := newMDStore(t)
	tasks := []meads.Task{
		{ID: 2, Title: "Only active", Status: "open"},
		{ID: 9, Title: "Deleted, higher id", Status: "closed", Deleted: true},
	}
	if err := s.ImportAll(tasks); err != nil {
		t.Fatalf("ImportAll: %v", err)
	}

	all, err := s.GetAll()
	if err != nil || len(all) != 2 {
		t.Fatalf("GetAll after ImportAll = %v, err=%v, want 2 tasks", all, err)
	}

	// A fresh Add must not reuse id 9, even though 9 is deleted and the
	// import wrote no explicit max-id meta - the row is still physically
	// present until the next prune, which nextID already accounts for.
	id, err := s.Add(meads.Task{Title: "Next", Status: "open"})
	if err != nil {
		t.Fatalf("Add after ImportAll: %v", err)
	}
	if id != 10 {
		t.Errorf("id after ImportAll = %d, want 10", id)
	}

	// That same Add call also pruned the tombstone row away (markdown always
	// drops deleted rows on every mutation) - Get and GetAll now agree.
	all, err = s.GetAll()
	if err != nil {
		t.Fatalf("GetAll after the follow-up Add: %v", err)
	}
	for _, task := range all {
		if task.Deleted {
			t.Errorf("tombstone task %d should have been pruned by the next mutation, got %+v", task.ID, task)
		}
	}
}

func TestImportAll_RefusesExistingFile(t *testing.T) {
	s := newCSVStore(t)
	if _, err := s.Add(meads.Task{Title: "Already here", Status: "open"}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	err := s.ImportAll([]meads.Task{{ID: 1, Title: "Would clobber", Status: "open"}})
	if err == nil {
		t.Fatal("ImportAll into an existing file should error, got nil")
	}
	if !strings.Contains(err.Error(), "already exists") {
		t.Errorf("error = %q, want it to mention \"already exists\"", err.Error())
	}
	// The original content must be untouched.
	tasks, _ := s.Get(nil)
	if len(tasks) != 1 || tasks[0].Title != "Already here" {
		t.Errorf("existing file was modified: %v", tasks)
	}
}

func TestImportAll_Empty(t *testing.T) {
	s := newCSVStore(t)
	if err := s.ImportAll(nil); err != nil {
		t.Fatalf("ImportAll(nil): %v", err)
	}
	tasks, err := s.Get(nil)
	if err != nil || len(tasks) != 0 {
		t.Fatalf("Get(nil) after ImportAll(nil) = %v, err=%v, want none", tasks, err)
	}
}
