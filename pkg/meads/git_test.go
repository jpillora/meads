package meads

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"testing"
)

func TestSuccessfulWaitDelay(t *testing.T) {
	if !successfulWaitDelay(context.Background(), exec.ErrWaitDelay) {
		t.Fatal("a live context and ErrWaitDelay should mean the command itself succeeded")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if successfulWaitDelay(ctx, exec.ErrWaitDelay) {
		t.Fatal("ErrWaitDelay after context cancellation must remain a failure")
	}
	if successfulWaitDelay(context.Background(), errors.New("exit status 1")) {
		t.Fatal("an ordinary command failure must not be ignored")
	}
}

// Git shell aliases let the git process exit zero while a descendant keeps its
// output pipe open. RunContext has no meaningful stdout to lose and may accept
// that result, but output-bearing calls must preserve ErrWaitDelay because the
// bytes returned before os/exec closes the pipe may be truncated.
func TestExecGitWaitDelayHandling(t *testing.T) {
	g := gitRepo(t)
	args := []string{"-c", "alias.meads-wait-delay=!sleep 2 &", "meads-wait-delay"}

	if err := g.RunContext(context.Background(), args...); err != nil {
		t.Fatalf("RunContext should accept a successful command's WaitDelay: %v", err)
	}
	if _, err := g.OutputContext(context.Background(), args...); !errors.Is(err, exec.ErrWaitDelay) {
		t.Fatalf("OutputContext error = %v, want exec.ErrWaitDelay", err)
	}
	if _, err := g.CombinedOutputContext(context.Background(), args...); !errors.Is(err, exec.ErrWaitDelay) {
		t.Fatalf("CombinedOutputContext error = %v, want exec.ErrWaitDelay", err)
	}
}

// gitRepo initialises an empty repo in a temp dir and returns an ExecGit for it.
func gitRepo(t *testing.T) *ExecGit {
	t.Helper()
	dir := t.TempDir()
	cmd := exec.Command("git", "init", "-q", ".")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	return &ExecGit{Dir: dir}
}

// Run used to use cmd.Run(), which discards stderr and left callers with a bare
// "exit status 128" — the failure task 67 chased for two occurrences without
// ever seeing why git bailed.
func TestExecGitRun_SurfacesStderr(t *testing.T) {
	g := gitRepo(t)
	err := g.Run("add", "no-such-file.md")
	if err == nil {
		t.Fatal("expected error adding a nonexistent path")
	}
	if !strings.Contains(err.Error(), "no-such-file.md") {
		t.Fatalf("error should carry git's message, got: %v", err)
	}
}

func TestExecGitRun_SucceedsQuietly(t *testing.T) {
	g := gitRepo(t)
	if err := g.Run("rev-parse", "--git-dir"); err != nil {
		t.Fatalf("rev-parse: %v", err)
	}
}

// A held index.lock is the contention IsIndexLocked exists to spot, so assert
// against the real thing rather than a hand-written message.
func TestIsIndexLocked_RealContention(t *testing.T) {
	g := gitRepo(t)
	lock := g.Dir + "/.git/index.lock"
	if err := exec.Command("touch", lock).Run(); err != nil {
		t.Fatalf("creating index.lock: %v", err)
	}
	if err := exec.Command("touch", g.Dir+"/f.txt").Run(); err != nil {
		t.Fatalf("creating file: %v", err)
	}
	err := g.Run("add", "f.txt")
	if err == nil {
		t.Fatal("expected git add to fail while index.lock is held")
	}
	if !IsIndexLocked(err) {
		t.Fatalf("IsIndexLocked should recognise lock contention, got: %v", err)
	}
}

func TestIsIndexLocked(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"lock contention", errString("exit status 128: fatal: Unable to create '/r/.git/index.lock': File exists."), true},
		{"translated prose still matches on the path", errString("exit status 128: fatal: Impossible de créer '/r/.git/index.lock' : Le fichier existe."), true},
		{"unrelated failure", errString("exit status 128: fatal: pathspec 'x.md' did not match any files"), false},
		{"bare exit status", errString("exit status 128"), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsIndexLocked(tt.err); got != tt.want {
				t.Fatalf("IsIndexLocked(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

type errString string

func (e errString) Error() string { return string(e) }
