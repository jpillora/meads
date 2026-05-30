package e2e

import (
	"fmt"
	"strings"
	"testing"

	"github.com/go-git/go-billy/v5/util"
	"github.com/jpillora/meads/pkg/meads"
)

// writeWorking writes raw content to the store's working file.
func writeWorking(t *testing.T, s *meads.Store, content string) {
	t.Helper()
	if err := util.WriteFile(s.FS(), s.Path(), []byte(content), 0644); err != nil {
		t.Fatalf("write working file: %v", err)
	}
}

// spyGit records whether any git command was executed.
type spyGit struct{ called bool }

func (g *spyGit) Run(args ...string) error { g.called = true; return nil }
func (g *spyGit) Output(args ...string) (string, error) {
	g.called = true
	return "", fmt.Errorf("spyGit should not be called")
}

// Active tasks come straight from the working file; git must not be consulted.
func TestGetWithHistory_ActiveSkipsGit(t *testing.T) {
	s := newMDStore(t)
	writeWorking(t, s, "## 1. Active task\n\n* status: open\n* priority: P2\n")

	spy := &spyGit{}
	tasks, err := s.GetWithHistory(spy, []int{1})
	if err != nil {
		t.Fatalf("GetWithHistory: %v", err)
	}
	if len(tasks) != 1 || tasks[0].ID != 1 || tasks[0].Title != "Active task" {
		t.Fatalf("unexpected result: %+v", tasks)
	}
	if spy.called {
		t.Fatal("git was consulted for an active task")
	}
}

// A task removed from the working file (e.g. deleted + tombstone pruned) is
// recovered from its most-recent committed version.
func TestGetWithHistory_RecoversDeleted(t *testing.T) {
	s := newMDStore(t)
	writeWorking(t, s, "## 1. Survivor\n\n* status: open\n") // task 5 is gone

	newer := "## 1. Survivor\n\n* status: open\n"                       // post-delete commit
	older := "## 1. Survivor\n\n* status: open\n\n## 5. Recover me\n\n* status: open\n* priority: P1\n"

	git := &fakeGit{
		commits: map[string]string{"newer:TASKS.md": newer, "older:TASKS.md": older},
		log:     []string{"newer", "older"},
	}
	tasks, err := s.GetWithHistory(git, []int{5})
	if err != nil {
		t.Fatalf("GetWithHistory: %v", err)
	}
	if len(tasks) != 1 || tasks[0].ID != 5 || tasks[0].Title != "Recover me" {
		t.Fatalf("unexpected result: %+v", tasks)
	}
	if tasks[0].Priority != "P1" {
		t.Errorf("priority = %q, want P1", tasks[0].Priority)
	}
}

// A mixed request returns active and history-recovered tasks, in request order.
func TestGetWithHistory_MixedActiveAndHistory(t *testing.T) {
	s := newMDStore(t)
	writeWorking(t, s, "## 3. Active\n\n* status: open\n")

	git := &fakeGit{
		commits: map[string]string{"aaa:TASKS.md": "## 5. Gone\n\n* status: closed\n"},
		log:     []string{"aaa"},
	}
	tasks, err := s.GetWithHistory(git, []int{3, 5})
	if err != nil {
		t.Fatalf("GetWithHistory: %v", err)
	}
	if len(tasks) != 2 || tasks[0].ID != 3 || tasks[1].ID != 5 {
		t.Fatalf("expected [3,5] in order, got %+v", tasks)
	}
}

// An ID present in neither the working file nor history is a clean not-found.
func TestGetWithHistory_NotFoundAnywhere(t *testing.T) {
	s := newMDStore(t)
	writeWorking(t, s, "## 1. Only task\n\n* status: open\n")

	git := &fakeGit{commits: map[string]string{}, log: nil}
	_, err := s.GetWithHistory(git, []int{99})
	if err == nil || !strings.Contains(err.Error(), "task 99 not found") {
		t.Fatalf("expected 'task 99 not found', got %v", err)
	}
}

// git failures (e.g. not a git repo) degrade to a plain not-found, not a git error.
func TestGetWithHistory_GitErrorDegrades(t *testing.T) {
	s := newMDStore(t)
	writeWorking(t, s, "## 1. Only task\n\n* status: open\n")

	_, err := s.GetWithHistory(&fakeGitError{}, []int{5})
	if err == nil || !strings.Contains(err.Error(), "task 5 not found") {
		t.Fatalf("expected 'task 5 not found', got %v", err)
	}
	if strings.Contains(err.Error(), "git") {
		t.Fatalf("git error leaked to caller: %v", err)
	}
}

// A CSV store recovers a task committed in markdown form (cross-format history).
func TestGetWithHistory_CSVCrossFormat(t *testing.T) {
	s := newCSVStore(t)
	writeWorking(t, s, meads.InitCSV()) // no tasks, header only

	mdContent := "## 7. Cross format\n\n* status: open\n* priority: P3\n"
	git := &fakeGit{
		commits: map[string]string{"aaa:TASKS.csv": mdContent},
		log:     []string{"aaa"},
	}
	tasks, err := s.GetWithHistory(git, []int{7})
	if err != nil {
		t.Fatalf("GetWithHistory: %v", err)
	}
	if len(tasks) != 1 || tasks[0].ID != 7 || tasks[0].Title != "Cross format" {
		t.Fatalf("unexpected result: %+v", tasks)
	}
}
