package meads

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

func newWazeroTestRepo(t *testing.T) (*WazeroGit, *RefStore, string) {
	t.Helper()
	dir := t.TempDir()
	native := &ExecGit{Dir: dir}
	if err := native.Run("init", "--quiet", "--bare", "."); err != nil {
		t.Fatalf("git init --bare: %v", err)
	}
	git := NewWazeroGit(dir)
	t.Cleanup(func() { _ = git.Close(context.Background()) })
	return git, NewRefStore(git), dir
}

func TestWazeroGitRefStoreParity(t *testing.T) {
	_, refs, _ := newWazeroTestRepo(t)

	firstContent := []byte("{\"version\":1}\n")
	first, err := refs.CommitFile(
		"refs/meads/tasks/1", "task.json", firstContent, ZeroOID, "create task 1",
	)
	if err != nil {
		t.Fatalf("first CommitFile: %v", err)
	}
	secondContent := []byte("{\"version\":2}\n")
	second, err := refs.CommitFile(
		"refs/meads/tasks/1", "task.json", secondContent, first, "update task 1",
	)
	if err != nil {
		t.Fatalf("second CommitFile: %v", err)
	}

	got, oid, err := refs.ReadFileAtRef("refs/meads/tasks/1", "task.json")
	if err != nil {
		t.Fatalf("ReadFileAtRef: %v", err)
	}
	if oid != second || string(got) != string(secondContent) {
		t.Fatalf("ReadFileAtRef = (%q, %s), want (%q, %s)", got, oid, secondContent, second)
	}

	history, err := refs.History("refs/meads/tasks/1")
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	if len(history) != 2 || history[0] != second || history[1] != first {
		t.Fatalf("History = %v, want [%s %s]", history, second, first)
	}
	contents, err := refs.ReadFilesAtCommits(history, "task.json")
	if err != nil {
		t.Fatalf("ReadFilesAtCommits: %v", err)
	}
	if string(contents[0]) != string(secondContent) || string(contents[1]) != string(firstContent) {
		t.Fatalf("batch contents = %q, %q", contents[0], contents[1])
	}

	listed, err := refs.ListRefs(TasksRefPrefix)
	if err != nil {
		t.Fatalf("ListRefs: %v", err)
	}
	if listed["refs/meads/tasks/1"] != second {
		t.Fatalf("ListRefs = %v", listed)
	}

	if err := refs.CompareAndSwap("refs/meads/tasks/1", first, first); !errors.Is(err, ErrCASConflict) {
		t.Fatalf("stale CompareAndSwap = %v, want ErrCASConflict", err)
	}
	start := make(chan struct{})
	results := make(chan error, 2)
	var wait sync.WaitGroup
	for range 2 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			results <- refs.CompareAndSwap("refs/meads/tasks/1", first, second)
		}()
	}
	close(start)
	wait.Wait()
	close(results)
	var succeeded, conflicted int
	for err := range results {
		switch {
		case err == nil:
			succeeded++
		case errors.Is(err, ErrCASConflict):
			conflicted++
		default:
			t.Fatalf("concurrent CompareAndSwap: %v", err)
		}
	}
	if succeeded != 1 || conflicted != 1 {
		t.Fatalf("concurrent CompareAndSwap: %d succeeded, %d conflicted", succeeded, conflicted)
	}
	if err := refs.AtomicUpdate([]RefUpdate{
		{Name: "refs/meads/config", Next: first, Prev: ZeroOID},
		{Name: "refs/meads/tasks/2", Next: second, Prev: ZeroOID},
	}); err != nil {
		t.Fatalf("AtomicUpdate: %v", err)
	}
	for name, want := range map[string]OID{
		"refs/meads/config":  first,
		"refs/meads/tasks/2": second,
	} {
		if got, err := refs.ResolveRef(name); err != nil || got != want {
			t.Errorf("ResolveRef(%s) = %s, %v; want %s", name, got, err, want)
		}
	}
}

func TestWazeroGitLinkedWorktree(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("linked-worktree mount assertion uses POSIX guest paths")
	}
	root := t.TempDir()
	mainDir := filepath.Join(root, "main")
	linkedDir := filepath.Join(root, "linked")
	if err := os.Mkdir(mainDir, 0o755); err != nil {
		t.Fatal(err)
	}
	native := &ExecGit{Dir: mainDir}
	if err := native.Run("init", "--quiet", "-b", "main", "."); err != nil {
		t.Fatalf("git init: %v", err)
	}
	if err := native.Run("-c", "user.name=meads", "-c", "user.email=meads@localhost",
		"commit", "--quiet", "--allow-empty", "-m", "initial"); err != nil {
		t.Fatalf("initial commit: %v", err)
	}
	if err := native.Run("worktree", "add", "--quiet", "-b", "wasm-test", linkedDir); err != nil {
		t.Fatalf("worktree add: %v", err)
	}

	mount, guest, err := discoverGitMount(linkedDir)
	if err != nil {
		t.Fatalf("discoverGitMount: %v", err)
	}
	wantMount := filepath.Join(mainDir, ".git")
	if mount != wantMount {
		t.Fatalf("mount = %q, want %q", mount, wantMount)
	}
	if !strings.HasPrefix(guest, "/git/worktrees/") {
		t.Fatalf("guest git dir = %q, want /git/worktrees/*", guest)
	}

	wasm := NewWazeroGit(linkedDir)
	t.Cleanup(func() { _ = wasm.Close(context.Background()) })
	refs := NewRefStore(wasm)
	commit, err := refs.CommitFile(
		"refs/meads/tasks/7", "task.json", []byte("{\"id\":7}"), ZeroOID, "create task 7",
	)
	if err != nil {
		t.Fatalf("CommitFile through linked worktree: %v", err)
	}
	got, err := NewRefStore(native).ResolveRef("refs/meads/tasks/7")
	if err != nil || got != commit {
		t.Fatalf("native ResolveRef = %s, %v; want %s", got, err, commit)
	}
}
