package main

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/jpillora/meads/pkg/meads"
)

// verboseAction prints an action before it starts, then returns a completion
// function that prints its outcome and elapsed wall time. Printing both ends
// matters for the slow-path this flag was added to diagnose: if a remote Git
// operation stalls, the last start line says exactly what md is waiting for.
//
// Verbose output always goes to stderr (or VerboseOutput in tests/embedders),
// leaving stdout stable for JSON, Markdown, and scripts.
func (g *globals) verboseAction(action string) func(error) {
	if g == nil || !g.Verbose {
		return func(error) {}
	}
	g.verbosef("%s...\n", action)
	started := time.Now()
	return func(err error) {
		elapsed := verboseDuration(time.Since(started))
		if err != nil {
			g.verbosef("%s failed in %s: %s\n", action, elapsed, redactURLCredentials(err.Error()))
			return
		}
		g.verbosef("%s done in %s\n", action, elapsed)
	}
}

func (g *globals) verbosef(format string, args ...any) {
	if g == nil || !g.Verbose {
		return
	}
	w := g.VerboseOutput
	if w == nil {
		w = os.Stderr
	}
	fmt.Fprintf(w, "meads: verbose: "+format, args...)
}

// verboseDuration keeps fast local actions useful without turning normal
// network timings into noisy nanosecond measurements.
func verboseDuration(d time.Duration) time.Duration {
	switch {
	case d < time.Millisecond:
		return d.Round(time.Microsecond)
	case d < time.Second:
		return d.Round(time.Millisecond)
	default:
		return d.Round(10 * time.Millisecond)
	}
}

func verboseCall[T any](g *globals, action string, fn func() (T, error)) (value T, err error) {
	done := g.verboseAction(action)
	defer func() { done(err) }()
	return fn()
}

func verboseDo(g *globals, action string, fn func() error) (err error) {
	done := g.verboseAction(action)
	defer func() { done(err) }()
	return fn()
}

// verboseTasks decorates the backend-neutral Tasks seam with user-facing
// operation names. It is installed only for a verbose invocation, so the
// ordinary path has no extra calls or output.
type verboseTasks struct {
	meads.Tasks
	g *globals
}

func (t *verboseTasks) Exists() (bool, error) {
	return verboseCall(t.g, "check task store", t.Tasks.Exists)
}

func (t *verboseTasks) Revision() (string, error) {
	return verboseCall(t.g, "read task-store revision", t.Tasks.Revision)
}

func (t *verboseTasks) Get(ids []int) ([]meads.Task, error) {
	return verboseCall(t.g, readTasksAction(ids, false), func() ([]meads.Task, error) {
		return t.Tasks.Get(ids)
	})
}

func (t *verboseTasks) GetWithHistory(ids []int) ([]meads.Task, error) {
	return verboseCall(t.g, readTasksAction(ids, true), func() ([]meads.Task, error) {
		return t.Tasks.GetWithHistory(ids)
	})
}

func (t *verboseTasks) GetHistory() ([]meads.Task, error) {
	return verboseCall(t.g, "read task history", t.Tasks.GetHistory)
}

func (t *verboseTasks) Ready() ([]meads.Task, error) {
	return verboseCall(t.g, "find ready tasks", t.Tasks.Ready)
}

func (t *verboseTasks) FindCycles() ([][]int, error) {
	return verboseCall(t.g, "check dependency cycles", t.Tasks.FindCycles)
}

func (t *verboseTasks) Doctor() ([]meads.DoctorFix, error) {
	return verboseCall(t.g, "doctor task store", t.Tasks.Doctor)
}

func (t *verboseTasks) Add(task meads.Task) (int, error) {
	return verboseCall(t.g, "add task", func() (int, error) { return t.Tasks.Add(task) })
}

func (t *verboseTasks) Update(id int, fn func(*meads.Task)) error {
	return verboseDo(t.g, fmt.Sprintf("update task %d", id), func() error {
		return t.Tasks.Update(id, fn)
	})
}

func (t *verboseTasks) Delete(id int) error {
	return verboseDo(t.g, fmt.Sprintf("delete task %d", id), func() error {
		return t.Tasks.Delete(id)
	})
}

func (t *verboseTasks) Restore(id int) error {
	return verboseDo(t.g, fmt.Sprintf("restore task %d", id), func() error {
		return t.Tasks.Restore(id)
	})
}

func (t *verboseTasks) HardDelete(id int) error {
	return verboseDo(t.g, fmt.Sprintf("erase task %d", id), func() error {
		return t.Tasks.HardDelete(id)
	})
}

func (t *verboseTasks) Sync(ctx context.Context) (*meads.SyncReport, error) {
	return verboseCall(t.g, "sync task refs with origin", func() (*meads.SyncReport, error) {
		return t.Tasks.Sync(ctx)
	})
}

func readTasksAction(ids []int, history bool) string {
	suffix := ""
	if history {
		suffix = " (including deleted)"
	}
	switch len(ids) {
	case 0:
		return "read all tasks" + suffix
	case 1:
		return fmt.Sprintf("read task %d%s", ids[0], suffix)
	default:
		parts := make([]string, len(ids))
		for i, id := range ids {
			parts[i] = strconv.Itoa(id)
		}
		return "read tasks " + strings.Join(parts, ",") + suffix
	}
}

// verboseGit times the exact Git subprocesses beneath the task-store action.
// It preserves Git's optional context/combined-output interfaces so wrapping
// an ExecGit cannot accidentally remove timeout enforcement or push-rejection
// diagnostics.
type verboseGit struct {
	base meads.Git
	g    *globals
}

func (v *verboseGit) Run(args ...string) error {
	return verboseDo(v.g, gitAction(args), func() error { return v.base.Run(args...) })
}

func (v *verboseGit) RunContext(ctx context.Context, args ...string) error {
	return verboseDo(v.g, gitAction(args), func() error {
		if git, ok := v.base.(meads.ContextGit); ok {
			return git.RunContext(ctx, args...)
		}
		if ctx != nil {
			if err := ctx.Err(); err != nil {
				return err
			}
		}
		return v.base.Run(args...)
	})
}

func (v *verboseGit) Output(args ...string) (string, error) {
	return verboseCall(v.g, gitAction(args), func() (string, error) {
		return v.base.Output(args...)
	})
}

func (v *verboseGit) OutputContext(ctx context.Context, args ...string) (string, error) {
	return verboseCall(v.g, gitAction(args), func() (string, error) {
		if git, ok := v.base.(meads.ContextGit); ok {
			return git.OutputContext(ctx, args...)
		}
		if ctx != nil {
			if err := ctx.Err(); err != nil {
				return "", err
			}
		}
		return v.base.Output(args...)
	})
}

func (v *verboseGit) CombinedOutputContext(ctx context.Context, args ...string) (string, error) {
	return verboseCall(v.g, gitAction(args), func() (string, error) {
		if git, ok := v.base.(meads.CombinedOutputGit); ok {
			return git.CombinedOutputContext(ctx, args...)
		}
		if ctx != nil {
			if err := ctx.Err(); err != nil {
				return "", err
			}
		}
		return v.base.Output(args...)
	})
}

func (v *verboseGit) OutputWithInput(stdin string, args ...string) (string, error) {
	return verboseCall(v.g, gitAction(args), func() (string, error) {
		return v.base.OutputWithInput(stdin, args...)
	})
}

func (v *verboseGit) OutputRaw(args ...string) ([]byte, error) {
	return verboseCall(v.g, gitAction(args), func() ([]byte, error) {
		return v.base.OutputRaw(args...)
	})
}

func (v *verboseGit) OutputRawWithInput(stdin string, args ...string) ([]byte, error) {
	return verboseCall(v.g, gitAction(args), func() ([]byte, error) {
		return v.base.OutputRawWithInput(stdin, args...)
	})
}

var urlCredentials = regexp.MustCompile(`(?i)(https?://)[^/@\s]+@`)

func redactURLCredentials(s string) string {
	return urlCredentials.ReplaceAllString(s, `${1}[redacted]@`)
}

func gitAction(args []string) string {
	quoted := make([]string, len(args))
	for i, arg := range args {
		arg = redactURLCredentials(arg)
		if strings.ContainsAny(arg, " \t\n\"'") {
			arg = strconv.Quote(arg)
		}
		quoted[i] = arg
	}
	return "git " + strings.Join(quoted, " ")
}

var (
	_ meads.Tasks             = (*verboseTasks)(nil)
	_ meads.Git               = (*verboseGit)(nil)
	_ meads.ContextGit        = (*verboseGit)(nil)
	_ meads.CombinedOutputGit = (*verboseGit)(nil)
)
