package meads

import (
	"bytes"
	"context"
	"errors"
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
	// OutputRawWithInput is OutputWithInput's binary-safe counterpart: it
	// pipes stdin AND returns stdout exactly as written. The batch plumbing
	// commands need both halves at once - `cat-file --batch` takes its object
	// list on stdin and streams length-delimited payloads back, where
	// trimming would corrupt the frames (see RefStore.ReadFilesAtCommits).
	OutputRawWithInput(stdin string, args ...string) ([]byte, error)
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
// fetch and push (tasks.go) - can therefore carry a caller-provided deadline.
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

// CombinedOutputGit is a second OPTIONAL half of Git, for the one kind of
// command whose OUTPUT is meaningful even when the command FAILS.
//
// `git push --porcelain` is the case that forces it: a rejected push exits
// non-zero, and its porcelain status lines - the only stable way to tell a
// divergence apart from an auth failure or an offline remote (see
// PushRejected) - are exactly what it printed on the way out. Run discards
// stdout entirely and Output/OutputContext discard it on a non-zero exit,
// so neither can see them. Like ContextGit this is discovered by type
// assert rather than added to Git, so test fakes do not have to grow a
// method they have no use for.
type CombinedOutputGit interface {
	// CombinedOutputContext runs args bounded by ctx and returns stdout and
	// stderr interleaved, ALWAYS - including alongside a non-nil error.
	CombinedOutputContext(ctx context.Context, args ...string) (string, error)
}

// combinedOutputContext runs args on git and returns its combined output
// even on failure, when git can do that.
//
// The fallback for a Git that is not a CombinedOutputGit is Output, which
// yields "" on failure: such a Git is a test fake backed by memory, which
// neither talks to a remote nor produces porcelain status lines, so the
// only thing lost is a classification that had nothing to classify.
func combinedOutputContext(ctx context.Context, git Git, args ...string) (string, error) {
	if cg, ok := git.(CombinedOutputGit); ok {
		return cg.CombinedOutputContext(ctx, args...)
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

var (
	_ ContextGit        = (*ExecGit)(nil)
	_ CombinedOutputGit = (*ExecGit)(nil)
)

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
		if successfulWaitDelay(ctx, err) {
			return nil
		}
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

// successfulWaitDelay identifies exec's unusual "the command succeeded, but
// one of its descendants kept an output pipe open" result for RunContext. Git
// transport helpers can outlive the git process briefly (SSH control processes
// are the common case), so Cmd.Wait returns exec.ErrWaitDelay even though git
// itself exited zero and the push completed. Treating that as a failed sync
// caused a healthy push to be reported as `exec: WaitDelay expired before I/O
// complete`.
//
// It is success only while the caller's context is still live. If the context
// expired, WaitDelay is part of the timeout path and must remain an error. It
// must not be used by output-bearing calls: ErrWaitDelay means their returned
// output may be truncated.
func successfulWaitDelay(ctx context.Context, err error) bool {
	return errors.Is(err, exec.ErrWaitDelay) && (ctx == nil || ctx.Err() == nil)
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

// CombinedOutputContext is Output bounded by ctx that keeps stderr and
// survives failure - see CombinedOutputGit. The output is returned in every
// case, including a non-zero exit and a deadline kill, because for a
// rejected push the output IS the diagnosis; the error is decorated with
// ctx's own error for the same reason RunContext does it.
func (g *ExecGit) CombinedOutputContext(ctx context.Context, args ...string) (string, error) {
	out, err := g.command(ctx, args...).CombinedOutput()
	if err != nil {
		return string(out), withContextErr(ctx, err)
	}
	return string(out), nil
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

func (g *ExecGit) OutputRawWithInput(stdin string, args ...string) ([]byte, error) {
	return g.outputRaw(stdin, args...)
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
