package meads

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/go-git/go-billy/v5/memfs"
)

// Tests for Detect and the OpenTasks entry-point family (open.go). Detection
// precedence and the never-error folding mirror cmd/md's old
// gitTaskRefsExist + bareDefaultTasksFile pair, whose behaviour these tests
// pin at the library level so `md` and library consumers (rais) can never
// disagree about which store a project uses.

// newDetectRepo creates a temporary git repository and returns its dir, for
// Detect/OpenTasks tests that need a real .git (mirrors newGitStoreRepo's
// setup in gitstore_test.go).
func newDetectRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	runGit(t, dir, "init", "-b", "main")
	runGit(t, dir, "config", "user.name", "Test")
	runGit(t, dir, "config", "user.email", "test@test.com")
	return dir
}

func TestDetect_GitMode(t *testing.T) {
	dir := newDetectRepo(t)
	// The whole refs/meads/ namespace counts: a config ref alone (a fresh
	// `init --git`, zero tasks) must already detect as git mode.
	gs := NewGitStore(&ExecGit{Dir: dir})
	if err := gs.SetConfig(DefaultConfig()); err != nil {
		t.Fatalf("SetConfig: %v", err)
	}
	b, err := Detect(dir)
	if err != nil || b != BackendGit {
		t.Errorf("Detect(repo with refs/meads/*) = %v, %v; want BackendGit, nil", b, err)
	}
}

func TestDetect_GitWinsOverCSV(t *testing.T) {
	dir := newDetectRepo(t)
	gs := NewGitStore(&ExecGit{Dir: dir})
	if err := gs.SetConfig(DefaultConfig()); err != nil {
		t.Fatalf("SetConfig: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "TASKS.csv"), []byte(InitCSV()), 0644); err != nil {
		t.Fatal(err)
	}
	b, err := Detect(dir)
	if err != nil || b != BackendGit {
		t.Errorf("Detect(git refs + TASKS.csv) = %v, %v; want BackendGit (refs win), nil", b, err)
	}
}

func TestDetect_CSVThenMarkdown(t *testing.T) {
	dir := t.TempDir() // not a git repo at all: git failure must fold, not error
	if b, err := Detect(dir); err != nil || b != BackendMarkdown {
		t.Errorf("Detect(empty dir) = %v, %v; want BackendMarkdown, nil", b, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "TASKS.csv"), []byte(InitCSV()), 0644); err != nil {
		t.Fatal(err)
	}
	if b, err := Detect(dir); err != nil || b != BackendCSV {
		t.Errorf("Detect(dir with TASKS.csv) = %v, %v; want BackendCSV, nil", b, err)
	}
}

func TestDetect_EmptyGitRepoIsMarkdown(t *testing.T) {
	// A git repo with no refs/meads/* and no TASKS.csv: markdown, so the
	// first add bootstraps a tasks file.
	dir := newDetectRepo(t)
	if b, err := Detect(dir); err != nil || b != BackendMarkdown {
		t.Errorf("Detect(bare git repo) = %v, %v; want BackendMarkdown, nil", b, err)
	}
}

func TestOpenTasks_NothingInitialisedIsNotAnError(t *testing.T) {
	tasks, err := OpenTasks(t.TempDir())
	if err != nil {
		t.Fatalf("OpenTasks on an empty dir: %v", err)
	}
	if tasks.Backend() != BackendMarkdown {
		t.Errorf("Backend() = %v, want BackendMarkdown", tasks.Backend())
	}
	exists, err := tasks.Exists()
	if err != nil || exists {
		t.Errorf("Exists() = %v, %v; want false, nil (uninitialised, not an error)", exists, err)
	}
	// The store is usable: Add creates the file.
	if _, err := tasks.Add(Task{Title: "bootstrapped"}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if exists, _ := tasks.Exists(); !exists {
		t.Error("Exists() after Add = false, want true")
	}
}

func TestOpenTasks_GitMode(t *testing.T) {
	dir := newDetectRepo(t)
	gs := NewGitStore(&ExecGit{Dir: dir})
	if _, err := gs.Create(Task{Title: "git task"}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	tasks, err := OpenTasks(dir)
	if err != nil {
		t.Fatalf("OpenTasks: %v", err)
	}
	if tasks.Backend() != BackendGit {
		t.Errorf("Backend() = %v, want BackendGit", tasks.Backend())
	}
	got, err := tasks.Get(nil)
	if err != nil || len(got) != 1 || got[0].Title != "git task" {
		t.Errorf("Get(nil) = %v, %v; want the git-mode task", got, err)
	}
}

func TestOpenTasksBackend_Forced(t *testing.T) {
	dir := t.TempDir() // deliberately not a git repo: construction must not fail
	gt, err := OpenTasksBackend(dir, BackendGit)
	if err != nil {
		t.Fatalf("OpenTasksBackend(BackendGit) outside a repo: %v", err)
	}
	if gt.Backend() != BackendGit {
		t.Errorf("Backend() = %v, want BackendGit", gt.Backend())
	}
	ct, err := OpenTasksBackend(dir, BackendCSV)
	if err != nil {
		t.Fatalf("OpenTasksBackend(BackendCSV): %v", err)
	}
	if ct.Backend() != BackendCSV {
		t.Errorf("Backend() = %v, want BackendCSV", ct.Backend())
	}
	if _, err := OpenTasksBackend(dir, Backend(99)); err == nil {
		t.Error("OpenTasksBackend(unknown) should error, got nil")
	}
}

func TestOpenTasksFile_ExplicitFile(t *testing.T) {
	dir := t.TempDir()
	tasks, err := OpenTasksFile(filepath.Join(dir, "TASKS.csv"))
	if err != nil {
		t.Fatalf("OpenTasksFile: %v", err)
	}
	if tasks.Backend() != BackendCSV {
		t.Errorf("Backend() = %v, want BackendCSV", tasks.Backend())
	}
	if tasks.Location() != filepath.Join(dir, "TASKS.csv") {
		t.Errorf("Location() = %q, want the explicit path", tasks.Location())
	}
}

// TestOpenTasksFS_Memfs pins task 78's memfs guarantee: tests built on
// memfs-backed stores keep working through the OpenTasks family rather than
// constructing NewStore directly.
func TestOpenTasksFS_Memfs(t *testing.T) {
	tasks, err := OpenTasksFS(memfs.New(), "TASKS.md")
	if err != nil {
		t.Fatalf("OpenTasksFS: %v", err)
	}
	id, err := tasks.Add(Task{Title: "in memory"})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	got, err := tasks.Get([]int{id})
	if err != nil || len(got) != 1 || got[0].Title != "in memory" {
		t.Errorf("Get(%d) = %v, %v; want the added task", id, got, err)
	}
}
