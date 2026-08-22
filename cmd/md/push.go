package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/jpillora/meads/pkg/meads"
)

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

// syncFunc is the seam autoPush uses to actually run the sync: given a
// context already carrying pushTimeout's deadline (see autoPush), it pulls
// and pushes and returns what happened.
//
// It replaced a pair of pushFunc/fetchFunc seams wrapping cmd/md's own
// fetch/Integrate/push, which duplicated meads.Tasks.Sync exactly (task
// 80). There is one sequence now, in the library, so the CLI and any other
// embedder cannot drift apart on it; what stays here is what genuinely
// belongs to a CLI - the cadence gate, the timeout budget, and the stderr
// rendering below, since nothing under pkg/meads prints.
//
// Implementations MUST respect ctx - returning once it is done rather than
// actually blocking past its deadline - exactly as meads.Tasks.Sync does by
// handing ctx to git. Tests override this var (restoring it via t.Cleanup)
// with a fake that behaves the same way (selecting on ctx.Done() instead of
// a real subprocess) to prove pushTimeout's context is what bounds autoPush
// - not a coincidence of how fast a local push happens to be - without
// needing a real hung network; see TestAutoPush_BoundedByTimeout in
// push_test.go. Production always uses runSync.
var syncFunc = runSync

// runSync pulls-then-pushes refs/meads/* via the library, bounded by ctx.
func runSync(ctx context.Context, g *globals) (*meads.SyncReport, error) {
	return meads.NewGitTasks(g.gitStore()).Sync(ctx)
}

// autoPush is called after every successful git-mode mutation
// (add/update/set-status/add-dep/rm-dep/del - the same call sites as
// postWebhook, right after it). Every exit besides the final sync attempt is
// a silent, best-effort skip: syncing is a nice-to-have that must never turn
// into a reason the command itself fails.
//
// Once per pushInterval it PULLS, then PUSHES (task 86): the pull fetches
// origin and integrates what arrived - adopting other clones' tasks,
// fast-forwarding unmoved ones, and re-homing contended local tasks at
// fresh ids via Doctor - so the push that follows converges instead of
// rejecting non-fast-forward. It syncs synchronously and reports on THIS
// SAME invocation - a timeout, a pull summary, and a divergence are all
// surfaced here, in the command that triggered the sync, rather than
// deferred to a later command (the earlier async design's "surfaced one
// command late" was exactly the confusing UX this synchronous design
// avoids).
func autoPush(g *globals) {
	if g == nil || g.mode() != modeGit {
		if g != nil {
			g.verbosef("remote sync skipped: file mode\n")
		}
		return // git mode only - file mode has no refs/meads/* to push
	}
	if err := g.git().Run("remote", "get-url", "origin"); err != nil {
		g.verbosef("remote sync skipped: origin is not configured\n")
		return // no origin remote configured: skip silently
	}
	commonDir, err := gitCommonDir(g)
	if err != nil {
		g.verbosef("remote sync skipped: cannot resolve git common directory: %v\n", err)
		return
	}
	cfg, err := g.gitStore().Config()
	if err != nil {
		g.verbosef("remote sync skipped: cannot read config: %v\n", err)
		return
	}
	should, err := g.gitStore().ShouldPush(commonDir, cfg.PushIntervalDuration())
	if err != nil {
		g.verbosef("remote sync skipped: cannot read cadence: %v\n", err)
		return
	}
	if !should {
		g.verbosef("remote sync skipped: %s interval has not elapsed\n", cfg.PushIntervalDuration())
		return
	}
	g.verbosef("remote sync due: %s interval elapsed\n", cfg.PushIntervalDuration())
	// Mark BEFORE attempting the sync, not after it succeeds: a failing or
	// timed-out remote must not be retried on every single subsequent
	// command, only once pushInterval has elapsed again - see MarkPushed's
	// doc comment. The trade-off (a genuinely failed/timed-out sync also
	// waits a full interval before the next attempt) is accepted for the
	// same reason it always was: never piling up redundant pushes against a
	// remote that isn't currently working.
	if err := g.gitStore().MarkPushed(commonDir); err != nil {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), pushTimeout)
	defer cancel()

	// One call does the pull (task 86) and the push, both bounded by the
	// same budget. The error is deliberately ignored: every failure here is
	// non-fatal to the mutation that triggered it, and the three outcomes
	// worth telling the user about are read off the report below instead.
	done := g.verboseAction("sync task refs with origin")
	report, syncErr := syncFunc(ctx, g)
	done(syncErr)

	// What the pull integrated - most importantly a contended task re-homed
	// at a fresh id, which renames a task the user may have just been
	// looking at and so must never be silent. Printed before the timeout
	// check because a pull that landed still happened even if the push that
	// followed then ran out of budget.
	if report != nil {
		if msg := integrateMessage(report.Integrate); msg != "" {
			fmt.Fprint(os.Stderr, msg)
		}
	}

	if ctx.Err() == context.DeadlineExceeded {
		fmt.Fprintf(os.Stderr, "meads: sync of refs/meads/* with origin timed out after %s; local changes are safe, will retry next interval\n", pushTimeout)
		return
	}
	// Any other failure (offline, auth, no remote gone stale since the
	// check above, etc.) is also non-fatal and, unlike a timeout or a
	// divergence, not worth a dedicated message - Rejected is false for it
	// and autoPush simply says nothing further.
	if msg := divergenceMessage(report); msg != "" {
		fmt.Fprintln(os.Stderr, msg)
	}
}

// integrateMessage renders an IntegrateReport as stderr lines ("" when
// nothing happened, the common case): one summary each for adopted and
// fast-forwarded tasks, and one line per Doctor repair - most importantly
// the contended-task re-homings, which rename a local task the user may
// have just been looking at and so must never be silent.
func integrateMessage(r *meads.IntegrateReport) string {
	if r == nil {
		return ""
	}
	var b strings.Builder
	if len(r.Imported) > 0 {
		fmt.Fprintf(&b, "meads: pulled %d new task(s) from origin (ids %s)\n", len(r.Imported), joinInts(r.Imported))
	}
	if len(r.FastForwarded) > 0 {
		fmt.Fprintf(&b, "meads: updated %d task(s) from origin (ids %s)\n", len(r.FastForwarded), joinInts(r.FastForwarded))
	}
	if r.ConfigUpdated {
		b.WriteString("meads: updated meads config from origin\n")
	}
	for _, fix := range r.Fixes {
		switch fix.Kind {
		case meads.DoctorFixMismatch:
			fmt.Fprintf(&b, "meads: repaired task %d id mismatch (ref vs stored content)\n", fix.OldID)
		case meads.DoctorFixDiverged:
			fmt.Fprintf(&b, "meads: task %d diverged with origin; your version moved to task %d (the id now holds origin's version)\n", fix.OldID, fix.NewID)
		default:
			fmt.Fprintf(&b, "meads: task %d collided with a different task on origin; your version moved to task %d (the id now holds origin's version)\n", fix.OldID, fix.NewID)
		}
	}
	return b.String()
}

// joinInts renders ids as a comma-separated list for integrateMessage.
func joinInts(ids []int) string {
	parts := make([]string, len(ids))
	for i, id := range ids {
		parts[i] = strconv.Itoa(id)
	}
	return strings.Join(parts, ", ")
}

// divergenceMessage renders a rejected push as a clear, actionable
// explanation of what that means - rather than leaving git's bare
// "! ... [rejected] (fetch first)" to speak for itself. Returns "" for a
// clean push, or a different kind of failure (offline, auth, no remote -
// none of which are diagnostic of divergence).
//
// The DETECTION lives in meads.PushRejected, over git's own porcelain
// reasons, so every caller of Tasks.Sync classifies a rejection
// identically; only this rendering is CLI-specific, because nothing under
// pkg/meads prints.
//
// Since task 86 a divergence is normally resolved automatically, by the
// pull half of the sync itself (fetch + Integrate, whose Doctor re-homes
// contended tasks at fresh ids). So a rejection reaching here means that
// reconciliation did not happen or did not settle it - origin moved again
// between this sync's fetch and its push, or the fetch failed and the
// integration was skipped - which is a "try again" situation, not the
// permanent manual-attention dead end the message used to describe.
func divergenceMessage(r *meads.SyncReport) string {
	if r == nil || !r.Rejected {
		return ""
	}
	return "meads: push of refs/meads/* to origin was rejected: another " +
		"clone pushed different changes while this sync was running " +
		"(refs/meads/* has diverged). Your local changes are committed " +
		"locally and are safe. The next sync pulls origin's version " +
		"first and reconciles automatically - re-homing any contended " +
		"task at a fresh id rather than losing it - or run 'md doctor' " +
		"after 'git fetch origin' to do it now. meads will NOT " +
		"force-push."
}
