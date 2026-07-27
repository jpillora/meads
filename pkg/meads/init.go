package meads

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// FetchRefspec is the fetch refspec git-mode initialisation adds to origin
// so a plain `git fetch`/`git clone` picks up refs/meads/* - without it
// these refs are pushed and visible via `git ls-remote` but a default clone
// never downloads them (verified against GitHub and Gitea; see task 57's
// design doc). It is purely additive (git config --add, never a plain set)
// and deliberately says nothing about pushing: see EnsureFetchRefspec.
//
// It lands in RemoteRefNamespace, NEVER directly in RefNamespace (task 65
// phase 8's fetch-safety fix). The leading "+" makes this a force-updating
// refspec, so a naive "+refs/meads/*:refs/meads/*" would forcibly overwrite
// refs/meads/tasks/<id> on every fetch - silently discarding any local
// commit on that ref the moment it hadn't been pushed yet, with no error
// and no trace beyond git's own reflog. This is exactly git's own
// convention for ordinary branches
// ("+refs/heads/*:refs/remotes/origin/*", landing in refs/remotes/origin/*
// rather than overwriting refs/heads/* directly) applied to the custom
// refs/meads/* namespace: force-update is fine for a namespace nothing
// local depends on, so fetching into a separate remote-tracking namespace
// lets local and fetched-remote state be compared (GitStore.Diverged,
// GitStore.Doctor) without ever clobbering local work. See
// RemoteRefNamespace's doc comment.
const FetchRefspec = "+" + RefNamespace + "*:" + RemoteRefNamespace + "*"

// FetchRefspecOutcome reports what EnsureFetchRefspec did, as data, so
// callers with a UI (cmd/md prints one line per case) and silent callers
// (servers) can both use it without any stdout side effects.
type FetchRefspecOutcome int

const (
	// FetchRefspecNoOrigin: no origin remote is configured; nothing was
	// done (not an error).
	FetchRefspecNoOrigin FetchRefspecOutcome = iota
	// FetchRefspecAlreadyPresent: FetchRefspec was already among origin's
	// fetch refspecs; left untouched.
	FetchRefspecAlreadyPresent
	// FetchRefspecAdded: FetchRefspec was added to remote.origin.fetch.
	FetchRefspecAdded
)

// EnsureFetchRefspec adds FetchRefspec to origin's fetch refspecs. It is
// purely additive (git config --add), never replacing origin's existing
// fetch line(s), and idempotent: it first checks the configured refspecs
// and reports FetchRefspecAlreadyPresent if FetchRefspec is already among
// them, so re-running init-like setup never adds a duplicate. No origin
// remote reports FetchRefspecNoOrigin and a nil error rather than failing.
//
// It deliberately never touches remote.origin.push. Configuring ANY push
// refspec on a remote replaces git's default matching/simple push behaviour
// and would break ordinary `git push` for the user - see cmd/md's
// TestIntegration_InitGit_DoesNotBreakNormalPush. The auto-push path passes
// an explicit refspec at push time instead.
func EnsureFetchRefspec(git Git) (FetchRefspecOutcome, error) {
	if err := git.Run("remote", "get-url", "origin"); err != nil {
		return FetchRefspecNoOrigin, nil
	}
	out, _ := git.Output("config", "--get-all", "remote.origin.fetch")
	for _, line := range strings.Split(out, "\n") {
		if strings.TrimSpace(line) == FetchRefspec {
			return FetchRefspecAlreadyPresent, nil
		}
	}
	if err := git.Run("config", "--add", "remote.origin.fetch", FetchRefspec); err != nil {
		return FetchRefspecNoOrigin, fmt.Errorf("setting fetch refspec: %w", err)
	}
	return FetchRefspecAdded, nil
}

// InitResult describes what InitTasks did, as data - the reason InitTasks
// prints nothing itself: a server caller (rais) gets the full outcome with
// no stdout side effects, while cmd/md's init command is a thin wrapper
// that prints one line per case.
type InitResult struct {
	// Tasks is the freshly initialised store, ready to use.
	Tasks Tasks
	// FetchRefspec is the fetch-refspec outcome, meaningful for BackendGit
	// only (file backends have no remote plumbing and always report
	// FetchRefspecNoOrigin).
	FetchRefspec FetchRefspecOutcome
}

// InitTasks initialises a task store in dir for backend b and returns it.
//
// Markdown/CSV get the empty-file write (TASKS.md empty, TASKS.csv its
// header row), refusing if the file already exists. BackendGit writes the
// default config ref - deliberately nothing else, in particular no
// placeholder task: "no refs/meads/tasks/* refs" already means exactly "no
// tasks", and the config ref is the one thing worth writing up front
// (without it, a freshly-initialised repo with zero tasks would be
// indistinguishable from one that was never initialised at all, so a second
// init would succeed again instead of erroring - exactly the clobber this
// function must refuse). It then ensures origin's fetch refspec, reporting
// the outcome in InitResult.
//
// BackendGit refuses, with an error, when dir is not inside a git
// repository, or when refs/meads/ already has any ref ("git mode is already
// initialized").
func InitTasks(dir string, b Backend) (InitResult, error) {
	switch b {
	case BackendMarkdown, BackendCSV:
		name := "TASKS.md"
		content := ""
		if b == BackendCSV {
			name = "TASKS.csv"
			content = InitCSV()
		}
		file := filepath.Join(dir, name)
		if _, err := os.Stat(file); err == nil {
			return InitResult{}, fmt.Errorf("%s already exists", name)
		}
		if err := os.WriteFile(file, []byte(content), 0644); err != nil {
			return InitResult{}, fmt.Errorf("creating %s: %w", name, err)
		}
		tasks, err := OpenTasksFile(file)
		if err != nil {
			return InitResult{}, err
		}
		return InitResult{Tasks: tasks}, nil
	case BackendGit:
		git := &ExecGit{Dir: dir}
		if _, err := git.Output("rev-parse", "--git-dir"); err != nil {
			return InitResult{}, fmt.Errorf("not in a git repository")
		}
		existing, err := NewRefStore(git).ListRefs(RefNamespace)
		if err != nil {
			return InitResult{}, fmt.Errorf("checking for existing git-mode refs: %w", err)
		}
		if len(existing) > 0 {
			return InitResult{}, fmt.Errorf("git mode is already initialized (%s already has refs)", RefNamespace)
		}
		gs := NewGitStore(git)
		if err := gs.SetConfig(DefaultConfig()); err != nil {
			return InitResult{}, fmt.Errorf("writing default config: %w", err)
		}
		outcome, err := EnsureFetchRefspec(git)
		if err != nil {
			return InitResult{}, err
		}
		return InitResult{Tasks: NewGitTasks(gs), FetchRefspec: outcome}, nil
	}
	return InitResult{}, fmt.Errorf("unknown backend %v", b)
}
