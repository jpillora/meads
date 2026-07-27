package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/jpillora/meads/pkg/meads"
)

// pushRefspec is the explicit refspec every meads auto-push uses to send
// refs/meads/* to origin. It is passed explicitly on every push invocation
// rather than configured as remote.origin.push, which would replace git's
// default matching/simple push behaviour for ordinary branches too - see
// meads.EnsureFetchRefspec's doc comment and
// TestIntegration_InitGit_DoesNotBreakNormalPush. Mirrors meads.FetchRefspec
// (pkg/meads/init.go), which is the same kind of remote-plumbing constant.
const pushRefspec = meads.RefNamespace + "*:" + meads.RefNamespace + "*"

// pushTimeout bounds how long autoPush will wait for `git push` before
// giving up and letting the command return anyway.
//
// autoPush pushes SYNCHRONOUSLY - JP's call: with pushInterval defaulting
// to a minute (gitconfig.go), only one command in roughly that many ever
// pays a push's cost at all, so blocking that one command is an acceptable
// trade for a much simpler implementation than an async/detached one. This
// timeout is the one thing standing between that occasional synchronous
// push and a genuinely wedged command, so it must actually bound the wait.
//
// A normal push, even over SSH, was measured at ~2.5s; an unreachable or
// black-holed remote instead hangs for the OS's TCP connect timeout, which
// is commonly tens of seconds (and unbounded in the worst case). 10s gives
// a slow-but-working network real headroom above that ~2.5s baseline while
// still capping the worst case - a wedged remote - to something a user
// waiting on a single `md update` will tolerate rather than assume md
// itself has hung.
const pushTimeout = 10 * time.Second

// gitCommonDir resolves the git COMMON directory for g - shared by every
// linked worktree of one repository, unlike --git-dir which is
// per-worktree. meads.GitStore.ShouldPush/MarkPushed require this specific
// directory (not --git-dir) so linked worktrees, which already share one
// refs/meads/* ref store (see pkg/meads/gitstore.go), also share one push
// cadence rather than each fighting to push on its own schedule.
//
// Always returns an absolute path: `git rev-parse --git-common-dir` alone
// commonly prints a path relative to the invocation's cwd (a plain ".git",
// confirmed experimentally - it is NOT auto-absolute like some other
// rev-parse forms).
func gitCommonDir(g *globals) (string, error) {
	out, err := g.git().Output("rev-parse", "--git-common-dir")
	if err != nil {
		return "", fmt.Errorf("git rev-parse --git-common-dir: %w", err)
	}
	if filepath.IsAbs(out) {
		return filepath.Clean(out), nil
	}
	base := g.Dir
	if base == "" {
		base, err = os.Getwd()
		if err != nil {
			return "", fmt.Errorf("resolving cwd for relative --git-common-dir: %w", err)
		}
	}
	return filepath.Join(base, out), nil
}

// pushFunc is the seam autoPush uses to actually run a push: given a
// context already carrying pushTimeout's deadline (see autoPush) and the
// repo's working directory, it runs the push and returns its combined
// output (used by divergenceMessage regardless of success/failure) and an
// error.
//
// Implementations MUST respect ctx - returning once it is done rather than
// actually blocking past its deadline - exactly as runPush does by handing
// ctx straight to exec.CommandContext. Tests override this var (restoring
// it via t.Cleanup) with a fake that behaves the same way (selecting on
// ctx.Done() instead of a real subprocess) to prove pushTimeout's context is
// what bounds autoPush - not a coincidence of how fast a local push happens
// to be - without needing a real hung network; see
// TestAutoPush_BoundedByTimeout in push_test.go. Production always uses
// runPush.
var pushFunc = runPush

// autoPush is called after every successful git-mode mutation
// (add/update/set-status/add-dep/rm-dep/del - the same call sites as
// postWebhook, right after it). Every exit besides the final push attempt is
// a silent, best-effort skip: a push is a nice-to-have that must never turn
// into a reason the command itself fails.
//
// It pushes synchronously and reports on THIS SAME invocation - both a
// timeout and a divergence are surfaced here, in the command that triggered
// the push, using the output that push produced, rather than deferred to a
// later command (the earlier async design's "surfaced one command late" was
// exactly the confusing UX this synchronous design avoids).
func autoPush(g *globals) {
	if g == nil || g.mode() != modeGit {
		return // git mode only - file mode has no refs/meads/* to push
	}
	if err := g.git().Run("remote", "get-url", "origin"); err != nil {
		return // no origin remote configured: skip silently
	}
	commonDir, err := gitCommonDir(g)
	if err != nil {
		return
	}
	cfg, err := g.gitStore().Config()
	if err != nil {
		return
	}
	should, err := g.gitStore().ShouldPush(commonDir, cfg.PushIntervalDuration())
	if err != nil || !should {
		return
	}
	// Mark BEFORE attempting the push, not after it succeeds: a failing or
	// timed-out remote must not be retried on every single subsequent
	// command, only once pushInterval has elapsed again - see MarkPushed's
	// doc comment. The trade-off (a genuinely failed/timed-out push also
	// waits a full interval before the next attempt) is accepted for the
	// same reason it always was: never piling up redundant pushes against a
	// remote that isn't currently working.
	if err := g.gitStore().MarkPushed(commonDir); err != nil {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), pushTimeout)
	defer cancel()
	output, _ := pushFunc(ctx, g.Dir)

	if ctx.Err() == context.DeadlineExceeded {
		fmt.Fprintf(os.Stderr, "meads: push of refs/meads/* to origin timed out after %s; local changes are safe, will retry next interval\n", pushTimeout)
		return
	}
	// Any other failure (offline, auth, no remote gone stale since the
	// check above, etc.) is also non-fatal and, unlike a timeout or a
	// divergence, not worth a dedicated message - divergenceMessage below
	// returns "" for it and autoPush simply says nothing further.
	if msg := divergenceMessage(output); msg != "" {
		fmt.Fprintln(os.Stderr, msg)
	}
}

// runPush runs `git push --porcelain origin <pushRefspec>` in dir, bounded
// by ctx (pushTimeout, applied by autoPush), and returns its combined
// output regardless of whether it succeeded - divergenceMessage inspects
// this directly.
//
// exec.CommandContext kills the process the moment ctx's deadline fires
// (confirmed experimentally: a `sleep 30` bounded by a 1s context returns
// in ~1s with no leftover process - CombinedOutput already waits on the
// killed process internally, so there is nothing extra to clean up here),
// so a black-holed remote costs at most pushTimeout, never the OS's own
// much longer TCP connect timeout. The error CombinedOutput returns on a
// timeout is an unhelpful "signal: killed", not context.DeadlineExceeded
// itself, which is why autoPush checks ctx.Err() afterward rather than the
// returned error to detect a timeout.
func runPush(ctx context.Context, dir string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", "push", "--porcelain", "origin", pushRefspec)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// divergenceMessage inspects the combined output of a `git push --porcelain
// <pushRefspec>` attempt and, if it shows a non-fast-forward style
// rejection, returns a clear, actionable explanation of what that means -
// rather than leaving git's bare "! ... [rejected] (fetch first)" to speak
// for itself. Returns "" for a clean push, a different kind of failure
// (offline, auth, no remote - none of which are diagnostic of divergence),
// or unparsable/absent input.
//
// It matches only on git's OWN client-side porcelain summary reasons -
// "non-fast-forward", "fetch first", "stale info" (all three confirmed
// experimentally against a real diverged push; git currently favours
// "fetch first" for the plain two-clones-diverged case, but the other two
// are long-standing synonyms from older/other rejection paths) - which are
// stable across git versions because --porcelain exists specifically for
// scripts to parse. It never matches on a remote host's free-text rejection
// reason, which task 57's design doc found varies by host (GitHub vs
// Gitea) and which nothing else in this codebase parses either (see
// pkg/meads/refstore.go's conflictError doc comment).
func divergenceMessage(output string) string {
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "!") {
			continue
		}
		lower := strings.ToLower(line)
		if strings.Contains(lower, "non-fast-forward") ||
			strings.Contains(lower, "fetch first") ||
			strings.Contains(lower, "stale info") {
			return "meads: push of refs/meads/* to origin was rejected: another " +
				"clone has already pushed different changes (refs/meads/* has " +
				"diverged). Your local changes are committed locally and are " +
				"safe. Automatic reconciliation is not implemented yet (meads " +
				"task 65) and will need manual attention; meads will NOT " +
				"force-push."
		}
	}
	return ""
}
