package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jpillora/meads/pkg/meads"
)

func gitInDir(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@test.com",
		"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@test.com",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v failed: %s\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func TestCSV_MergeSafety(t *testing.T) {
	dir := t.TempDir()
	header := meads.InitCSV()

	gitInDir(t, dir, "init", "-b", "main")
	task1Row := "1,Fix login,open,P1,bug,,,Session cookie expires,,,," + "{}\n"
	task2Row := "2,Add tests,open,P2,task,,,,,,," + "{}\n"
	task3Row := "3,Write docs,open,P3,task,,,,,,," + "{}\n"
	initial := header + task1Row + task2Row + task3Row
	os.WriteFile(filepath.Join(dir, "TASKS.csv"), []byte(initial), 0644)
	gitInDir(t, dir, "add", "TASKS.csv")
	gitInDir(t, dir, "commit", "-m", "initial")

	// branch-a: close task 1.
	gitInDir(t, dir, "checkout", "-b", "branch-a")
	task1Closed := "1,Fix login,closed,P1,bug,,,Session cookie expires,,,," + "{}\n"
	os.WriteFile(filepath.Join(dir, "TASKS.csv"), []byte(header+task1Closed+task2Row+task3Row), 0644)
	gitInDir(t, dir, "add", "TASKS.csv")
	gitInDir(t, dir, "commit", "-m", "close task 1")

	// branch-b: reprioritize task 3.
	gitInDir(t, dir, "checkout", "main")
	gitInDir(t, dir, "checkout", "-b", "branch-b")
	task3Modified := "3,Write docs,open,P1,task,,,,,,," + "{}\n"
	os.WriteFile(filepath.Join(dir, "TASKS.csv"), []byte(header+task1Row+task2Row+task3Modified), 0644)
	gitInDir(t, dir, "add", "TASKS.csv")
	gitInDir(t, dir, "commit", "-m", "reprioritize task 3")

	// Merge both into main.
	gitInDir(t, dir, "checkout", "main")
	gitInDir(t, dir, "merge", "branch-a")
	gitInDir(t, dir, "merge", "branch-b", "-m", "merge branch-b")

	merged, _ := os.ReadFile(filepath.Join(dir, "TASKS.csv"))
	content := string(merged)
	if strings.Contains(content, "<<<<<<<") {
		t.Fatalf("merge produced conflict markers:\n%s", content)
	}
	f := meads.ParseCSV(content)
	if len(f.Tasks) != 3 {
		t.Fatalf("expected 3 tasks, got %d", len(f.Tasks))
	}
	for _, task := range f.Tasks {
		if task.ID == 1 && task.Status != "closed" {
			t.Errorf("task 1 status = %q, want closed", task.Status)
		}
		if task.ID == 3 && task.Priority != "P1" {
			t.Errorf("task 3 priority = %q, want P1", task.Priority)
		}
	}
}

func TestCSV_SameRowConflict(t *testing.T) {
	dir := t.TempDir()
	header := meads.InitCSV()
	task1Row := "1,Original title,open,P1,bug,,,desc,,,," + "{}\n"

	gitInDir(t, dir, "init", "-b", "main")
	os.WriteFile(filepath.Join(dir, "TASKS.csv"), []byte(header+task1Row), 0644)
	gitInDir(t, dir, "add", "TASKS.csv")
	gitInDir(t, dir, "commit", "-m", "initial")

	gitInDir(t, dir, "checkout", "-b", "branch-a")
	os.WriteFile(filepath.Join(dir, "TASKS.csv"), []byte(header+"1,Title from branch A,open,P1,bug,,,desc,,,,{}\n"), 0644)
	gitInDir(t, dir, "add", "TASKS.csv")
	gitInDir(t, dir, "commit", "-m", "branch-a title change")

	gitInDir(t, dir, "checkout", "main")
	gitInDir(t, dir, "checkout", "-b", "branch-b")
	os.WriteFile(filepath.Join(dir, "TASKS.csv"), []byte(header+"1,Title from branch B,open,P1,bug,,,desc,,,,{}\n"), 0644)
	gitInDir(t, dir, "add", "TASKS.csv")
	gitInDir(t, dir, "commit", "-m", "branch-b title change")

	gitInDir(t, dir, "checkout", "main")
	gitInDir(t, dir, "merge", "branch-a")

	cmd := exec.Command("git", "merge", "branch-b", "-m", "merge branch-b")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@test.com",
		"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@test.com",
	)
	if err := cmd.Run(); err == nil {
		t.Fatal("expected merge conflict but merge succeeded")
	}

	conflicted, _ := os.ReadFile(filepath.Join(dir, "TASKS.csv"))
	if !strings.Contains(string(conflicted), "<<<<<<<") {
		t.Fatal("expected conflict markers in merged file")
	}
}

// --- GetHistory tests ---

func TestGetHistory_CSV(t *testing.T) {
	s := newCSVStore(t)
	header := meads.InitCSV()
	commit1 := header +
		"1,Fix login,closed,P1,bug,,,,,,," + "{}\n" +
		"2,deleted,deleted,,,,,,,,," + "\n" +
		"3,Write docs,open,P2,task,,,,,,," + "{}\n"
	commit2 := header +
		"1,Fix login,open,P1,bug,,,,,,," + "{}\n" +
		"2,Add tests,open,P2,task,,,,,,," + "{}\n"

	git := &fakeGit{
		commits: map[string]string{"aaa:TASKS.csv": commit1, "bbb:TASKS.csv": commit2},
		log:     []string{"aaa", "bbb"},
	}
	tasks, err := s.GetHistory(git)
	if err != nil {
		t.Fatalf("GetHistory: %v", err)
	}
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
}

func TestGetHistory_CrossFormat(t *testing.T) {
	s := newCSVStore(t)
	header := meads.InitCSV()
	csvContent := header + "2,New task,open,P2,task,,,,,,," + "{}\n"
	mdContent := "## 1 Old task\n\n* status: open\n* priority: P1\n"

	git := &fakeGit{
		commits: map[string]string{"aaa:TASKS.csv": csvContent, "bbb:TASKS.csv": mdContent},
		log:     []string{"aaa", "bbb"},
	}
	tasks, _ := s.GetHistory(git)
	if len(tasks) != 2 {
		t.Fatalf("expected 2 tasks, got %d", len(tasks))
	}
}

func TestGetHistory_EmptyLog(t *testing.T) {
	s := newCSVStore(t)
	git := &fakeGit{commits: map[string]string{}, log: nil}
	tasks, err := s.GetHistory(git)
	if err != nil || tasks != nil {
		t.Fatalf("expected nil/nil, got %v/%v", tasks, err)
	}
}

func TestGetHistory_ShowError(t *testing.T) {
	s := newCSVStore(t)
	git := &fakeGit{commits: map[string]string{}, log: []string{"aaa"}}
	tasks, _ := s.GetHistory(git)
	if len(tasks) != 0 {
		t.Fatalf("expected 0 tasks, got %d", len(tasks))
	}
}

func TestGetHistory_LogError(t *testing.T) {
	s := newCSVStore(t)
	_, err := s.GetHistory(&fakeGitError{})
	if err == nil || !strings.Contains(err.Error(), "git log") {
		t.Fatalf("expected git log error, got %v", err)
	}
}

func TestGetHistory_MarkdownStore(t *testing.T) {
	s := newMDStore(t)
	header := meads.InitCSV()
	mdContent := "## 1 Task one\n\n* status: open\n"
	csvContent := header + "2,Task two,open,P2,task,,,,,,," + "{}\n"

	git := &fakeGit{
		commits: map[string]string{"aaa:TASKS.md": mdContent, "bbb:TASKS.md": csvContent},
		log:     []string{"aaa", "bbb"},
	}
	tasks, _ := s.GetHistory(git)
	if len(tasks) != 2 {
		t.Fatalf("expected 2 tasks, got %d", len(tasks))
	}
}
