package meads

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// Git abstracts git CLI operations for testability.
type Git interface {
	// Run executes a git command and returns an error if it fails. stderr is
	// captured and folded into the returned error, so a failure is
	// diagnosable rather than a bare exit status.
	Run(args ...string) error
	// Output executes a git command and returns its stdout.
	Output(args ...string) (string, error)
	// OutputWithInput executes a git command with stdin piped from the given
	// string and returns trimmed stdout. Unlike Output, stderr is captured
	// and folded into the returned error, so plumbing failures (e.g. a
	// rejected update-ref) are diagnosable rather than a bare exit status.
	OutputWithInput(stdin string, args ...string) (string, error)
	// OutputRaw executes a git command and returns stdout exactly as
	// written, with no trimming. Use this for binary-safe reads (e.g. blob
	// contents) where trimming could silently corrupt the result.
	OutputRaw(args ...string) ([]byte, error)
}

// ContextGit is the OPTIONAL half of Git: a Git implementation that can
// bound one command's wall clock. Only the commands that talk to a remote
// need it (ls-remote, fetch, push), and only ExecGit can actually hang, so
// this is an optional interface a caller discovers with a type assert
// (runContext/outputContext below) rather than two more methods every test
// fake has to grow.
//
// It exists because a remote git command is unbounded by default: an
// unreachable or black-holed host hangs for the OS's TCP connect timeout,
// commonly tens of seconds and unbounded in the worst case. Anything on a
// command's critical path - clone resolution's ls-remote (clone.go), Sync's
// fetch and push (tasks.go) - must therefore carry a deadline, exactly as
// cmd/md's auto-push already does with its own exec.CommandContext.
type ContextGit interface {
	// RunContext is Run, bounded by ctx: the process is killed the moment
	// ctx is done.
	RunContext(ctx context.Context, args ...string) error
	// OutputContext is Output, bounded by ctx.
	OutputContext(ctx context.Context, args ...string) (string, error)
}

// runContext runs args on git, bounded by ctx when git can be bounded.
// A Git that does not implement ContextGit is a test fake backed by memory,
// which cannot block on a network, so falling back to the plain call is
// safe - and keeps ContextGit optional.
func runContext(ctx context.Context, git Git, args ...string) error {
	if cg, ok := git.(ContextGit); ok {
		return cg.RunContext(ctx, args...)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return git.Run(args...)
}

// outputContext is runContext's Output counterpart - see there.
func outputContext(ctx context.Context, git Git, args ...string) (string, error) {
	if cg, ok := git.(ContextGit); ok {
		return cg.OutputContext(ctx, args...)
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return git.Output(args...)
}

// ExecGit implements Git by shelling out to the git CLI.
type ExecGit struct {
	Dir string
}

var _ ContextGit = (*ExecGit)(nil)

// waitDelay bounds how long a ctx-bounded command waits AFTER the deadline
// killed git itself, before giving up on its output pipes and returning.
//
// It is not belt-and-braces: without it the deadline does not actually
// bound the call in the case that matters most. exec.CommandContext kills
// git at the deadline, but Output/Run then block in Wait until stdout and
// stderr are closed - and git's network transports are separate child
// processes (ssh, git-remote-https) that INHERIT those pipes. Killing git
// does not kill them, so an ssh still blocked in connect() to a black-holed
// host holds the pipe open and Wait blocks for the very TCP timeout the
// deadline exists to avoid. Confirmed with a hanging transport helper: a
// 200ms deadline returned only after the 5s helper exited, until WaitDelay
// was set. 1s is generous for a cooperative child to finish flushing and
// short enough that a wedged one is still bounded by ~timeout + 1s.
const waitDelay = time.Second

// command builds the *exec.Cmd for args, bounded by ctx when ctx is
// non-nil, so a black-holed remote costs at most the caller's timeout
// rather than the OS's own much longer TCP connect timeout.
func (g *ExecGit) command(ctx context.Context, args ...string) *exec.Cmd {
	var cmd *exec.Cmd
	if ctx != nil {
		cmd = exec.CommandContext(ctx, "git", args...)
		cmd.WaitDelay = waitDelay
	} else {
		cmd = exec.Command("git", args...)
	}
	if g.Dir != "" {
		cmd.Dir = g.Dir
	}
	return cmd
}

func (g *ExecGit) Run(args ...string) error { return g.RunContext(nil, args...) }

// RunContext is Run bounded by ctx - see ContextGit. A ctx that is already
// done short-circuits before spawning anything; a deadline that fires
// mid-command kills the process, and the error is decorated with ctx's own
// error so callers see a timeout rather than an unhelpful "signal: killed".
func (g *ExecGit) RunContext(ctx context.Context, args ...string) error {
	cmd := g.command(ctx, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		err = withContextErr(ctx, err)
		// cmd.Run() alone discards stderr, leaving callers with a bare
		// "exit status 128" — git's fatal message is the only thing that
		// says *why*, so fold it into the error (same as outputRaw).
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return fmt.Errorf("%w: %s", err, msg)
		}
		return err
	}
	return nil
}

// withContextErr replaces a killed-by-context failure with ctx's own error.
// exec.CommandContext reports a deadline as a bare "signal: killed", which
// tells a caller nothing about WHY the command died; wrapping ctx.Err()
// keeps errors.Is(err, context.DeadlineExceeded) working through every
// layer above (see clone.go's resolveCloneBackend, which must distinguish
// "asked and the answer was no" from "the ask itself failed").
func withContextErr(ctx context.Context, err error) error {
	if ctx == nil || ctx.Err() == nil {
		return err
	}
	return fmt.Errorf("%w: %v", ctx.Err(), err)
}

// IsIndexLocked reports whether err is git failing to take .git/index.lock
// because another git process holds it ("fatal: Unable to create
// '<dir>/.git/index.lock': File exists."). Any index-writing command — add,
// but also the diff/status refreshes a shell prompt or editor runs constantly —
// contends for that lock, so a lone `git add` can lose the race and exit 128
// through no fault of the caller. Callers that can afford to wait should retry.
//
// Matching is on the "index.lock" path fragment rather than the prose, which
// git translates when the environment has a message catalogue.
func IsIndexLocked(err error) bool {
	return err != nil && strings.Contains(err.Error(), "index.lock")
}

func (g *ExecGit) Output(args ...string) (string, error) { return g.OutputContext(nil, args...) }

// OutputContext is Output bounded by ctx - see ContextGit and RunContext.
func (g *ExecGit) OutputContext(ctx context.Context, args ...string) (string, error) {
	out, err := g.command(ctx, args...).Output()
	if err != nil {
		return "", withContextErr(ctx, err)
	}
	return strings.TrimSpace(string(out)), nil
}

func (g *ExecGit) OutputWithInput(stdin string, args ...string) (string, error) {
	out, err := g.outputRaw(stdin, args...)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func (g *ExecGit) OutputRaw(args ...string) ([]byte, error) {
	return g.outputRaw("", args...)
}

// outputRaw runs git with stdin piped from the given string and returns
// stdout unmodified. cmd.Output() alone discards stderr; capturing it here
// and folding it into the error keeps plumbing failures diagnosable.
func (g *ExecGit) outputRaw(stdin string, args ...string) ([]byte, error) {
	cmd := g.command(nil, args...)
	cmd.Stdin = strings.NewReader(stdin)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return nil, fmt.Errorf("%w: %s", err, msg)
		}
		return nil, err
	}
	return out, nil
}
