package meads

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
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
	if err := refs.AtomicUpdate([]RefUpdate{
		{Name: "refs/meads/config", Next: second, Prev: first},
		{Name: "refs/meads/tasks/2", Next: first, Prev: first},
	}); !errors.Is(err, ErrCASConflict) {
		t.Fatalf("stale AtomicUpdate = %v, want ErrCASConflict", err)
	}
	for name, want := range map[string]OID{
		"refs/meads/config":  first,
		"refs/meads/tasks/2": second,
	} {
		if got, err := refs.ResolveRef(name); err != nil || got != want {
			t.Errorf("ResolveRef(%s) after rejected transaction = %s, %v; want %s", name, got, err, want)
		}
	}
}

func TestWasmGitCommandCoverage(t *testing.T) {
	for _, test := range []struct {
		args []string
		want bool
	}{
		{args: []string{"hash-object", "-w", "--stdin"}, want: true},
		{args: []string{"-c", "user.name=meads", "commit-tree", "deadbeef"}, want: true},
		{args: []string{"update-ref", "--stdin"}, want: true},
		{args: []string{"fetch", "origin"}, want: false},
		{args: []string{"config", "--get", "user.name"}, want: false},
	} {
		if got := wasmGitCommand(test.args); got != test.want {
			t.Errorf("wasmGitCommand(%q) = %v, want %v", test.args, got, test.want)
		}
	}
}

func TestTigoOptionsFromEnv(t *testing.T) {
	t.Setenv("MEADS_WAZERO_CACHE", "/tmp/meads-tigo-cache")
	t.Setenv("MEADS_GIT_HTTP_USERNAME", "http-user")
	t.Setenv("MEADS_GIT_HTTP_PASSWORD", "http-password")
	t.Setenv("MEADS_GIT_HTTP_TOKEN", "http-token")
	t.Setenv("MEADS_GIT_SSH_KEY", "/tmp/id_ed25519")
	t.Setenv("MEADS_GIT_SSH_PASSPHRASE", "ssh-passphrase")
	options := tigoOptionsFromEnv()
	if options.CacheDir != "/tmp/meads-tigo-cache" ||
		options.HTTPAuth.Username != "http-user" ||
		options.HTTPAuth.Password != "http-password" ||
		options.HTTPAuth.Token != "http-token" ||
		options.SSHAuth.PrivateKeyPath != "/tmp/id_ed25519" ||
		options.SSHAuth.Passphrase != "ssh-passphrase" {
		t.Fatalf("tigoOptionsFromEnv = %#v", options)
	}
}

func TestWazeroGitLocalRemoteFallsBack(t *testing.T) {
	remoteDir := t.TempDir()
	remoteNative := &ExecGit{Dir: remoteDir}
	if err := remoteNative.Run("init", "--quiet", "--bare", "."); err != nil {
		t.Fatalf("init remote: %v", err)
	}
	wasm, refs, _ := newWazeroTestRepo(t)
	if err := wasm.native.Run("remote", "add", "origin", remoteDir); err != nil {
		t.Fatalf("add origin: %v", err)
	}
	commit, err := refs.CommitFile(
		"refs/meads/tasks/12", "task.json", []byte(`{"id":12}`), ZeroOID, "create task 12",
	)
	if err != nil {
		t.Fatalf("CommitFile: %v", err)
	}
	out, err := wasm.CombinedOutputContext(context.Background(),
		"push", "--porcelain", "origin", RefNamespace+"*:"+RefNamespace+"*")
	if err != nil {
		t.Fatalf("local fallback push: %v\n%s", err, out)
	}
	if got, err := NewRefStore(remoteNative).ResolveRef("refs/meads/tasks/12"); err != nil || got != commit {
		t.Fatalf("remote ref = %s, %v; want %s", got, err, commit)
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
