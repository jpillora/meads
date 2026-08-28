package meads

import (
	"context"
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
// TestIntegration_InitGit_DoesNotBreakNormalPush. The sync path passes
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

// GitInitState reports what EnsureGitInit did, as data, so a caller with a UI
// prints it and a silent caller ignores it - the same split InitResult exists
// for.
type GitInitState struct {
	// Skipped is true if the repo is not in git mode, so nothing was done and
	// FetchRefspec is meaningless.
	Skipped bool
	// FetchRefspec is what EnsureFetchRefspec did.
	FetchRefspec FetchRefspecOutcome
	// ProtocolVersionWritten reports that the shared config was missing the
	// git-ref protocol marker and was updated (or created) with the current
	// version.
	ProtocolVersionWritten bool
}

// EnsureGitInit finishes git-mode setup for a repo that is ALREADY in git
// mode, which is where InitTasks refuses to run at all.
//
// Git mode has two ways in - `md init --git` and `md convert --to-git` - and
// only the first ensured origin's fetch refspec, so the documented migration
// left a repo unable to fetch anyone else's task refs and with no way to
// repair it: InitTasks refuses on ANY ref under RefNamespace, and convert's
// task refs are under it (task 91). Both entry points call this now, and so
// does `md doctor`, so repos an older binary already left that way can be
// fixed.
//
// It ensures both pieces of shared protocol setup: the fetch refspec and the
// git_ref_protocol_version marker in ConfigRef. Repositories converted by an
// older md can have task refs but no ConfigRef; once RefNamespace is already
// non-empty, creating the config is a repair rather than a mode change.
//
// The guard below is the safety boundary. Doctor can be forced into git mode
// in a file-mode repository, and convert can import an empty file, so neither
// caller alone proves that shared refs exist. Nothing is written until the
// namespace itself does.
func EnsureGitInit(git Git) (GitInitState, error) {
	refs, err := NewRefStore(git).ListRefs(RefNamespace)
	if err != nil {
		return GitInitState{}, fmt.Errorf("checking for %s refs: %w", RefNamespace, err)
	}
	if len(refs) == 0 {
		return GitInitState{Skipped: true}, nil
	}
	protocolWritten, err := NewGitStore(git).EnsureGitRefProtocolVersion()
	if err != nil {
		return GitInitState{}, err
	}
	outcome, err := EnsureFetchRefspec(git)
	if err != nil {
		return GitInitState{}, err
	}
	return GitInitState{FetchRefspec: outcome, ProtocolVersionWritten: protocolWritten}, nil
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
	// AdoptedTasks is the number of task refs adopted from origin, or 0 for
	// a fresh initialisation. Non-zero means the repo was a clone of an
	// already-initialised git-mode remote and no new config ref was seeded.
	AdoptedTasks int
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
		// A fresh clone of a git-mode repo has an empty LOCAL namespace but
		// an initialised origin: seeding a fresh config ref here would make
		// the next push reject non-fast-forward over origin's real one, so
		// adopt origin's refs instead (safe: the local namespace is empty,
		// so there is nothing to lose).
		ctx, cancel := context.WithTimeout(context.Background(), remoteProbeTimeout)
		defer cancel()
		if lsOut, err := originMeadsRefs(ctx, git); err == nil && strings.TrimSpace(lsOut) != "" {
			tasks, outcome, err := adoptOriginRefs(ctx, git, lsOut)
			if err != nil {
				return InitResult{}, err
			}
			return InitResult{Tasks: NewGitTasks(gs), FetchRefspec: outcome, AdoptedTasks: tasks}, nil
		}
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
