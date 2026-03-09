package meads

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// fakeGit implements the Git interface for testing GetHistory.
type fakeGit struct {
	commits map[string]string // "hash:file" -> content
	log     []string          // ordered commit hashes (newest first)
}

func (g *fakeGit) Run(args ...string) error { return nil }

func (g *fakeGit) Output(args ...string) (string, error) {
	// Handle: git log --all --format=%H -- <file>
	if len(args) >= 4 && args[0] == "log" {
		if len(g.log) == 0 {
			return "", nil
		}
		return strings.Join(g.log, "\n"), nil
	}
	// Handle: git show <hash>:<file>
	if len(args) >= 1 && args[0] == "show" && len(args) == 2 && strings.Contains(args[1], ":") {
		key := args[1]
		content, ok := g.commits[key]
		if !ok {
			return "", fmt.Errorf("not found: %s", key)
		}
		return content, nil
	}
	return "", fmt.Errorf("fakeGit: unsupported command: %v", args)
}

// fakeGitError is a Git implementation that always returns errors.
type fakeGitError struct{}

func (g *fakeGitError) Run(args ...string) error                { return fmt.Errorf("git error") }
func (g *fakeGitError) Output(args ...string) (string, error) { return "", fmt.Errorf("git error") }

// --- Merge safety tests using real git in a temp dir ---

func gitInDir(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=test",
		"GIT_AUTHOR_EMAIL=test@test.com",
		"GIT_COMMITTER_NAME=test",
		"GIT_COMMITTER_EMAIL=test@test.com",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v failed: %s\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func TestCSV_MergeSafety(t *testing.T) {
	dir := t.TempDir()

	// Init repo with initial CSV file containing 3 tasks.
	// Having existing rows allows branches to edit *different* rows, which merges cleanly.
	gitInDir(t, dir, "init", "-b", "main")
	header := csvHeaderRow()
	task1Row := "1,Fix login,open,P1,bug,,,Session cookie expires,,,," + "{}\n"
	task2Row := "2,Add tests,open,P2,task,,,,,,," + "{}\n"
	task3Row := "3,Write docs,open,P3,task,,,,,,," + "{}\n"
	initial := header + task1Row + task2Row + task3Row
	if err := os.WriteFile(filepath.Join(dir, "TASKS.csv"), []byte(initial), 0644); err != nil {
		t.Fatal(err)
	}
	gitInDir(t, dir, "add", "TASKS.csv")
	gitInDir(t, dir, "commit", "-m", "initial")

	// branch-a: modify task 1 (close it).
	gitInDir(t, dir, "checkout", "-b", "branch-a")
	task1Closed := "1,Fix login,closed,P1,bug,,,Session cookie expires,,,," + "{}\n"
	contentA := header + task1Closed + task2Row + task3Row
	if err := os.WriteFile(filepath.Join(dir, "TASKS.csv"), []byte(contentA), 0644); err != nil {
		t.Fatal(err)
	}
	gitInDir(t, dir, "add", "TASKS.csv")
	gitInDir(t, dir, "commit", "-m", "close task 1")

	// branch-b: modify task 3 (change priority).
	gitInDir(t, dir, "checkout", "main")
	gitInDir(t, dir, "checkout", "-b", "branch-b")
	task3Modified := "3,Write docs,open,P1,task,,,,,,," + "{}\n"
	contentB := header + task1Row + task2Row + task3Modified
	if err := os.WriteFile(filepath.Join(dir, "TASKS.csv"), []byte(contentB), 0644); err != nil {
		t.Fatal(err)
	}
	gitInDir(t, dir, "add", "TASKS.csv")
	gitInDir(t, dir, "commit", "-m", "reprioritize task 3")

	// Merge branch-a into main (fast-forward).
	gitInDir(t, dir, "checkout", "main")
	gitInDir(t, dir, "merge", "branch-a")

	// Merge branch-b into main (three-way merge — edits on different rows).
	gitInDir(t, dir, "merge", "branch-b", "-m", "merge branch-b")

	// Read merged file and verify all 3 tasks present with correct states.
	merged, err := os.ReadFile(filepath.Join(dir, "TASKS.csv"))
	if err != nil {
		t.Fatal(err)
	}
	content := string(merged)

	// No conflict markers.
	if strings.Contains(content, "<<<<<<<") {
		t.Fatalf("merge produced conflict markers:\n%s", content)
	}

	f := ParseCSV(content)
	if len(f.Tasks) != 3 {
		t.Fatalf("expected 3 tasks after merge, got %d\nmerged content:\n%s", len(f.Tasks), content)
	}

	// Verify branch-a's change: task 1 is closed.
	for _, task := range f.Tasks {
		if task.ID == 1 && task.Status != "closed" {
			t.Errorf("task 1 status = %q, want %q (from branch-a)", task.Status, "closed")
		}
		// Verify branch-b's change: task 3 priority is P1.
		if task.ID == 3 && task.Priority != "P1" {
			t.Errorf("task 3 priority = %q, want %q (from branch-b)", task.Priority, "P1")
		}
	}
}

func TestCSV_SameRowConflict(t *testing.T) {
	dir := t.TempDir()

	// Init repo with task 1.
	gitInDir(t, dir, "init", "-b", "main")
	header := csvHeaderRow()
	task1Row := "1,Original title,open,P1,bug,,,desc,,,," + "{}\n"
	initial := header + task1Row
	if err := os.WriteFile(filepath.Join(dir, "TASKS.csv"), []byte(initial), 0644); err != nil {
		t.Fatal(err)
	}
	gitInDir(t, dir, "add", "TASKS.csv")
	gitInDir(t, dir, "commit", "-m", "initial")

	// branch-a: modify task 1 title.
	gitInDir(t, dir, "checkout", "-b", "branch-a")
	modA := header + "1,Title from branch A,open,P1,bug,,,desc,,,," + "{}\n"
	if err := os.WriteFile(filepath.Join(dir, "TASKS.csv"), []byte(modA), 0644); err != nil {
		t.Fatal(err)
	}
	gitInDir(t, dir, "add", "TASKS.csv")
	gitInDir(t, dir, "commit", "-m", "branch-a title change")

	// branch-b: modify task 1 title differently.
	gitInDir(t, dir, "checkout", "main")
	gitInDir(t, dir, "checkout", "-b", "branch-b")
	modB := header + "1,Title from branch B,open,P1,bug,,,desc,,,," + "{}\n"
	if err := os.WriteFile(filepath.Join(dir, "TASKS.csv"), []byte(modB), 0644); err != nil {
		t.Fatal(err)
	}
	gitInDir(t, dir, "add", "TASKS.csv")
	gitInDir(t, dir, "commit", "-m", "branch-b title change")

	// Merge branch-a into main.
	gitInDir(t, dir, "checkout", "main")
	gitInDir(t, dir, "merge", "branch-a")

	// Merge branch-b — should fail with conflict.
	cmd := exec.Command("git", "merge", "branch-b", "-m", "merge branch-b")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=test",
		"GIT_AUTHOR_EMAIL=test@test.com",
		"GIT_COMMITTER_NAME=test",
		"GIT_COMMITTER_EMAIL=test@test.com",
	)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatal("expected merge conflict but merge succeeded")
	}
	_ = out

	// Verify conflict markers are present.
	conflicted, err := os.ReadFile(filepath.Join(dir, "TASKS.csv"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(conflicted), "<<<<<<<") {
		t.Fatal("expected conflict markers in merged file")
	}
}

// --- GetHistory tests with fakeGit ---

func TestGetHistory_CSV(t *testing.T) {
	fs := newCSVTestStore(t, "").fs
	s := NewStore(fs, "TASKS.csv")

	header := csvHeaderRow()
	// Commit 1 (newest): tasks 1,2,3 — task 2 is deleted.
	commit1 := header +
		"1,Fix login,closed,P1,bug,,,,,,," + "{}\n" +
		"2,deleted,deleted,,,,,,,,," + "\n" +
		"3,Write docs,open,P2,task,,,,,,," + "{}\n"
	// Commit 2 (older): tasks 1,2 — both active.
	commit2 := header +
		"1,Fix login,open,P1,bug,,,,,,," + "{}\n" +
		"2,Add tests,open,P2,task,,,,,,," + "{}\n"

	git := &fakeGit{
		commits: map[string]string{
			"aaa:TASKS.csv": commit1,
			"bbb:TASKS.csv": commit2,
		},
		log: []string{"aaa", "bbb"},
	}

	tasks, err := s.GetHistory(git)
	if err != nil {
		t.Fatalf("GetHistory: %v", err)
	}

	// Task 2 is deleted in the most recent commit, so excluded.
	// Tasks 1 and 3 should be present.
	if len(tasks) != 2 {
		t.Fatalf("expected 2 tasks, got %d", len(tasks))
	}
	ids := map[int]bool{}
	for _, task := range tasks {
		ids[task.ID] = true
	}
	if !ids[1] || !ids[3] {
		t.Errorf("expected tasks [1,3], got %v", ids)
	}
	// Most recent version of task 1 should be "closed".
	for _, task := range tasks {
		if task.ID == 1 && task.Status != "closed" {
			t.Errorf("task 1 status = %q, want %q", task.Status, "closed")
		}
	}
}

func TestGetHistory_CrossFormat(t *testing.T) {
	fs := newCSVTestStore(t, "").fs
	s := NewStore(fs, "TASKS.csv")

	header := csvHeaderRow()
	// Most recent commit: CSV format with task 2.
	csvContent := header + "2,New task,open,P2,task,,,,,,," + "{}\n"
	// Older commit: markdown format with task 1.
	mdContent := "## 1 Old task\n\n* status: open\n* priority: P1\n"

	git := &fakeGit{
		commits: map[string]string{
			"aaa:TASKS.csv": csvContent,
			"bbb:TASKS.csv": mdContent,
		},
		log: []string{"aaa", "bbb"},
	}

	tasks, err := s.GetHistory(git)
	if err != nil {
		t.Fatalf("GetHistory: %v", err)
	}

	// Should find both tasks via format fallback.
	if len(tasks) != 2 {
		t.Fatalf("expected 2 tasks, got %d", len(tasks))
	}
	ids := map[int]bool{}
	for _, task := range tasks {
		ids[task.ID] = true
	}
	if !ids[1] || !ids[2] {
		t.Errorf("expected tasks [1,2], got %v", ids)
	}
}

func TestGetHistory_EmptyLog(t *testing.T) {
	fs := newCSVTestStore(t, "").fs
	s := NewStore(fs, "TASKS.csv")

	git := &fakeGit{
		commits: map[string]string{},
		log:     nil,
	}

	tasks, err := s.GetHistory(git)
	if err != nil {
		t.Fatalf("GetHistory: %v", err)
	}
	if tasks != nil {
		t.Fatalf("expected nil tasks, got %v", tasks)
	}
}

func TestGetHistory_ShowError(t *testing.T) {
	fs := newCSVTestStore(t, "").fs
	s := NewStore(fs, "TASKS.csv")

	// Log returns a commit, but show fails (file doesn't exist in that commit).
	git := &fakeGit{
		commits: map[string]string{},
		log:     []string{"aaa"},
	}

	tasks, err := s.GetHistory(git)
	if err != nil {
		t.Fatalf("GetHistory: %v", err)
	}
	// No tasks found since show fails.
	if len(tasks) != 0 {
		t.Fatalf("expected 0 tasks, got %d", len(tasks))
	}
}

func TestGetHistory_LogError(t *testing.T) {
	fs := newCSVTestStore(t, "").fs
	s := NewStore(fs, "TASKS.csv")

	errGit := &fakeGitError{}
	_, err := s.GetHistory(errGit)
	if err == nil {
		t.Fatal("expected error from git log failure")
	}
	if !strings.Contains(err.Error(), "git log") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestGetHistory_MarkdownStore(t *testing.T) {
	fs := newTestStore(t, "").fs
	s := NewStore(fs, "TASKS.md")

	// Most recent: markdown.
	mdContent := "## 1 Task one\n\n* status: open\n"
	// Older: CSV format (cross-format fallback for markdown store).
	header := csvHeaderRow()
	csvContent := header + "2,Task two,open,P2,task,,,,,,," + "{}\n"

	git := &fakeGit{
		commits: map[string]string{
			"aaa:TASKS.md": mdContent,
			"bbb:TASKS.md": csvContent,
		},
		log: []string{"aaa", "bbb"},
	}

	tasks, err := s.GetHistory(git)
	if err != nil {
		t.Fatalf("GetHistory: %v", err)
	}

	if len(tasks) != 2 {
		t.Fatalf("expected 2 tasks, got %d", len(tasks))
	}
}
