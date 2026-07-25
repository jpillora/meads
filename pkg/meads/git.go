package meads

import (
	"bytes"
	"fmt"
	"os/exec"
	"strings"
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

// ExecGit implements Git by shelling out to the git CLI.
type ExecGit struct {
	Dir string
}

func (g *ExecGit) Run(args ...string) error {
	cmd := exec.Command("git", args...)
	if g.Dir != "" {
		cmd.Dir = g.Dir
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
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

func (g *ExecGit) Output(args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	if g.Dir != "" {
		cmd.Dir = g.Dir
	}
	out, err := cmd.Output()
	if err != nil {
		return "", err
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
	cmd := exec.Command("git", args...)
	if g.Dir != "" {
		cmd.Dir = g.Dir
	}
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
