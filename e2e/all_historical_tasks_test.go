package e2e

import (
	"testing"

	"github.com/jpillora/meads/pkg/meads"
)

// Tests for Store.AllHistoricalTasks (TASKS #70): `md convert --to-git` needs
// the raw, unfiltered picture of every id that ever appeared in the tasks
// file's git history - unlike GetHistory, a historically-deleted task must
// NOT be dropped, since convert needs to know it existed at all in order to
// re-import it soft-deleted rather than silently losing its id. See
// query.go's collectFirstSeen, the walk GetHistory, recoverFromHistory, and
// AllHistoricalTasks all now share.

// Unlike GetHistory (which excludes deleted tasks), AllHistoricalTasks keeps
// everything - convert.go decides what to do with Deleted itself.
func TestAllHistoricalTasks_IncludesDeleted(t *testing.T) {
	s := newCSVStore(t)
	header := meads.InitCSV()
	commit := header +
		"1,Fix login,open,P1,bug,,,,,,,,," + "{}\n" +
		"2,Deleted one,open,,,,,,,,,,true," + "\n"

	git := &fakeGit{
		commits: map[string]string{"aaa:TASKS.csv": commit},
		log:     []string{"aaa"},
	}
	all := s.AllHistoricalTasks(git)
	if len(all) != 2 {
		t.Fatalf("AllHistoricalTasks = %v, want 2 tasks (GetHistory would drop the deleted one; this must not)", all)
	}
	if !all[2].Deleted || all[2].Title != "Deleted one" {
		t.Errorf("task 2 = %+v, want deleted \"Deleted one\"", all[2])
	}
	if all[1].Deleted {
		t.Errorf("task 1 = %+v, want not deleted", all[1])
	}
}

// The newest commit's version of an id wins over any older one, matching
// GetHistory/recoverFromHistory's shared "first seen wins" walk order.
func TestAllHistoricalTasks_NewestVersionWins(t *testing.T) {
	s := newMDStore(t)
	newer := "## 1. New title\n\n* status: closed\n"
	older := "## 1. Old title\n\n* status: open\n"
	git := &fakeGit{
		commits: map[string]string{"newer:TASKS.md": newer, "older:TASKS.md": older},
		log:     []string{"newer", "older"}, // newest first
	}
	all := s.AllHistoricalTasks(git)
	if len(all) != 1 || all[1].Title != "New title" || all[1].Status != "closed" {
		t.Fatalf("AllHistoricalTasks = %+v, want the newest version of task 1", all)
	}
}

// A git failure (e.g. not a repository) degrades to an empty map, never an
// error - AllHistoricalTasks has no error return at all, so a caller (e.g.
// `md convert --to-git`) can always call it unconditionally.
func TestAllHistoricalTasks_LogErrorDegrades(t *testing.T) {
	s := newMDStore(t)
	all := s.AllHistoricalTasks(&fakeGitError{})
	if len(all) != 0 {
		t.Fatalf("AllHistoricalTasks with a failing git = %v, want empty", all)
	}
}

// No commits ever touched the tasks file: `git log` succeeds with empty
// output, which must yield an empty map rather than a crash.
func TestAllHistoricalTasks_EmptyLog(t *testing.T) {
	s := newMDStore(t)
	all := s.AllHistoricalTasks(&fakeGit{commits: map[string]string{}, log: nil})
	if len(all) != 0 {
		t.Fatalf("AllHistoricalTasks with no history = %v, want empty", all)
	}
}

// A nil Git (e.g. an uninitialized globals in a test or a code path that
// never constructed one) must not panic.
func TestAllHistoricalTasks_NilGit(t *testing.T) {
	s := newMDStore(t)
	all := s.AllHistoricalTasks(nil)
	if len(all) != 0 {
		t.Fatalf("AllHistoricalTasks(nil) = %v, want empty, not a panic", all)
	}
}
