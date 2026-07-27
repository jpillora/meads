package meads

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

// Tests that the one-shot clone resolution can never hang a command, and
// that a half-finished adopt is never reported as file mode. Both cover
// clone.go paths that sit on the critical path of the FIRST meads command
// in every repo with an origin and no tasks file.

// blockingRemoteGit implements ContextGit and blocks forever on the named
// remote subcommand until ctx is done - a black-holed host, without needing
// one. Everything else passes through to the wrapped Git.
type blockingRemoteGit struct {
	Git
	blockOn string // git subcommand to hang on, e.g. "ls-remote"

	mu      sync.Mutex
	blocked int
}

func (b *blockingRemoteGit) hang(ctx context.Context, args []string) bool {
	if len(args) == 0 || args[0] != b.blockOn {
		return false
	}
	b.mu.Lock()
	b.blocked++
	b.mu.Unlock()
	<-ctx.Done()
	return true
}

func (b *blockingRemoteGit) OutputContext(ctx context.Context, args ...string) (string, error) {
	if b.hang(ctx, args) {
		return "", ctx.Err()
	}
	return b.Git.Output(args...)
}

func (b *blockingRemoteGit) RunContext(ctx context.Context, args ...string) error {
	if b.hang(ctx, args) {
		return ctx.Err()
	}
	return b.Git.Run(args...)
}

func (b *blockingRemoteGit) blockedCalls() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.blocked
}

// shortenRemoteProbeTimeout drops remoteProbeTimeout for one test so the
// bound can be asserted in reasonable wall-clock time.
func shortenRemoteProbeTimeout(t *testing.T, d time.Duration) {
	t.Helper()
	prev := remoteProbeTimeout
	remoteProbeTimeout = d
	t.Cleanup(func() { remoteProbeTimeout = prev })
}

// TestResolveCloneBackend_UnreachableOriginIsBounded: a repo whose origin
// never answers must not hang the command. The resolution's ls-remote is
// the first thing OpenTasks does in any repo with an origin and no tasks
// file, and a git remote command is unbounded by default - an unreachable
// host costs the OS's TCP connect timeout, tens of seconds and unbounded in
// the worst case - so without remoteProbeTimeout a plain `md list` blocks
// indefinitely, on every invocation.
func TestResolveCloneBackend_UnreachableOriginIsBounded(t *testing.T) {
	shortenRemoteProbeTimeout(t, 150*time.Millisecond)
	dir := newDetectRepo(t)
	runGit(t, dir, "remote", "add", "origin", "https://198.51.100.7/never-answers.git")
	git := &blockingRemoteGit{Git: &ExecGit{Dir: dir}, blockOn: "ls-remote"}

	done := make(chan Backend, 1)
	go func() { done <- resolveCloneBackend(git) }()
	select {
	case backend := <-done:
		if backend != BackendMarkdown {
			t.Errorf("backend = %v, want BackendMarkdown for an origin that never answered", backend)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("resolveCloneBackend never returned: the remote probe is unbounded")
	}
	if b := git.blockedCalls(); b != 1 {
		t.Errorf("blocked ls-remote calls = %d, want 1", b)
	}
	// A timed-out ask is NOT "asked, and the answer was no", so no marker:
	// the next call must retry and self-heal once origin is reachable.
	if refs, checked := probeInitState(git); checked || len(refs) > 0 {
		t.Errorf("after a timed-out ls-remote: initChecked=%v refs=%v, want neither (the ask failed)", checked, refs)
	}
}

// TestResolveCloneBackend_AdoptFailingAfterFetchStaysGit: if the fetch
// lands origin's refs but the step after it fails, the repo IS in git mode
// and must be reported as such. Reporting file mode instead would let the
// very next command write TASKS.md into a git-mode clone - and an existing
// tasks file short-circuits this resolution forever after, so that mistake
// is permanent, not transient.
func TestResolveCloneBackend_AdoptFailingAfterFetchStaysGit(t *testing.T) {
	origin := newGitModeOrigin(t, "lives in git mode")
	dir := cloneDir(t, origin)
	git := &failAfterFetchGit{Git: &ExecGit{Dir: dir}}

	if backend := resolveCloneBackend(git); backend != BackendGit {
		t.Errorf("backend = %v, want BackendGit: the fetch landed origin's refs", backend)
	}
	refs, _ := probeInitState(&ExecGit{Dir: dir})
	if len(refs) == 0 {
		t.Fatal("precondition: the fetch should have landed refs/meads/* locally")
	}
	// And the store that resolution yields really is the git one, holding
	// origin's task rather than an empty file.
	tasks, err := OpenTasksBackend(dir, BackendGit)
	if err != nil {
		t.Fatalf("OpenTasksBackend: %v", err)
	}
	got, err := tasks.Get(nil)
	if err != nil || len(got) != 1 || got[0].Title != "lives in git mode" {
		t.Fatalf("Get(nil) = %+v (err=%v), want origin's single task", got, err)
	}
}

// failAfterFetchGit lets the adopt's fetch succeed and then fails the
// config write that follows it (EnsureFetchRefspec), reproducing an adopt
// that dies with the refs already local.
type failAfterFetchGit struct {
	Git
	fetched bool
}

func (f *failAfterFetchGit) Run(args ...string) error {
	if len(args) > 0 && args[0] == "fetch" {
		f.fetched = true
		return f.Git.Run(args...)
	}
	if f.fetched && len(args) > 2 && args[0] == "config" && args[1] == "--add" {
		return errors.New("simulated: could not write remote.origin.fetch")
	}
	return f.Git.Run(args...)
}

func (f *failAfterFetchGit) RunContext(ctx context.Context, args ...string) error {
	return f.Run(args...)
}

func (f *failAfterFetchGit) OutputContext(ctx context.Context, args ...string) (string, error) {
	return f.Git.Output(args...)
}

// TestGitTasks_Sync_HonoursContext: Sync takes a context, so a library
// caller passing a deadline must actually get one. Both network halves (the
// fetch inside PullContext, and the push) are bounded; before this, ctx was
// checked once on entry and then ignored, leaving rais's every Sync call
// able to block for the OS's TCP timeout.
func TestGitTasks_Sync_HonoursContext(t *testing.T) {
	gs, _, dir := newGitStoreRepo(t)
	runGit(t, dir, "remote", "add", "origin", "https://198.51.100.7/never-answers.git")
	gs.git = &blockingRemoteGit{Git: &ExecGit{Dir: dir}, blockOn: "fetch"}
	gs.refs = NewRefStore(gs.git)

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := NewGitTasks(gs).Sync(ctx); done <- err }()
	select {
	case err := <-done:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("Sync err = %v, want a context.DeadlineExceeded", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Sync never returned: it ignores its context")
	}
}

// TestGitTasks_Sync_AlreadyCancelledDoesNothing: the cheapest case - a
// context already done on entry must not spawn any git at all.
func TestGitTasks_Sync_AlreadyCancelledDoesNothing(t *testing.T) {
	gs, _, _ := newGitStoreRepo(t)
	spy := &commandSpy{Git: gs.git}
	gs.git = spy
	gs.refs = NewRefStore(spy)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := NewGitTasks(gs).Sync(ctx); !errors.Is(err, context.Canceled) {
		t.Errorf("Sync err = %v, want context.Canceled", err)
	}
	if n := spy.calls(); n != 0 {
		t.Errorf("git invocations = %d, want 0 for an already-cancelled context", n)
	}
}

// commandSpy counts every git invocation made through it.
type commandSpy struct {
	Git
	mu sync.Mutex
	n  int
}

func (s *commandSpy) note() {
	s.mu.Lock()
	s.n++
	s.mu.Unlock()
}

func (s *commandSpy) calls() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.n
}

func (s *commandSpy) Run(args ...string) error { s.note(); return s.Git.Run(args...) }

func (s *commandSpy) Output(args ...string) (string, error) {
	s.note()
	return s.Git.Output(args...)
}

func (s *commandSpy) OutputWithInput(stdin string, args ...string) (string, error) {
	s.note()
	return s.Git.OutputWithInput(stdin, args...)
}

func (s *commandSpy) OutputRaw(args ...string) ([]byte, error) {
	s.note()
	return s.Git.OutputRaw(args...)
}

// TestExecGit_ContextKillsARealGitProcess proves ContextGit's bound is a
// real hanging `git ls-remote` being killed at the deadline - the thing the
// fakes above stand in for - and that the error names the deadline rather
// than exec's bare "signal: killed" (see withContextErr). git's ext
// transport runs an arbitrary command as the transport helper, which is the
// only way to make a genuinely blocked ls-remote locally, with no network.
func TestExecGit_ContextKillsARealGitProcess(t *testing.T) {
	dir := newDetectRepo(t)
	git := &ExecGit{Dir: dir}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err := git.OutputContext(ctx, "-c", "protocol.ext.allow=always", "ls-remote", "ext::sleep 5")
	elapsed := time.Since(start)

	if elapsed > 3*time.Second {
		t.Errorf("OutputContext took %s, want it killed at the ~200ms deadline", elapsed)
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want it to wrap context.DeadlineExceeded", err)
	}
	if !strings.Contains(err.Error(), "deadline") {
		t.Errorf("err = %q, want the deadline named, not just the signal", err)
	}
}
