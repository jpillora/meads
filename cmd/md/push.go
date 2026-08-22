package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/jpillora/meads/pkg/meads"
)

const defaultBackgroundSyncTimeout = 10 * time.Second

// gitCommonDir resolves the git COMMON directory for g. Linked worktrees
// share refs/meads/*, so they must also share one queued sync request.
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
	abs, err := filepath.Abs(filepath.Join(base, out))
	if err != nil {
		return "", err
	}
	return filepath.Clean(abs), nil
}

// syncFunc is the test seam around the library's explicit sync operation.
// Library mutations never call it: foreground `md sync` and the detached CLI
// worker are the only callers.
var syncFunc = runSync

// enqueueSyncFunc is separate from the worker's syncFunc so command tests can
// disable process spawning while daemon integration tests exercise the real
// scheduler directly.
var enqueueSyncFunc = enqueueBackgroundSync

func runSync(ctx context.Context, g *globals) (*meads.SyncReport, error) {
	tasks, err := g.tasks()
	if err != nil {
		return nil, err
	}
	return tasks.Sync(ctx)
}

type syncCmd struct {
	globals *globals
}

// Run has two deliberately distinct modes behind one public command:
//
//   - `md sync` is a foreground, explicit operation. It always attempts a
//     sync now and returns failure to the caller, so scripts can rely on its
//     exit status.
//   - CLI writes execute this same command with MEADS_SYNC_DAEMON set. Those
//     internal modes own the detached debounce worker and are best-effort.
func (c *syncCmd) Run() error {
	if handled, err := syncDaemonDispatch(c.globals); handled {
		return err
	}
	return runForegroundSync(c.globals)
}

func runForegroundSync(g *globals) error {
	if g == nil {
		return errors.New("sync: missing command context")
	}
	if g.mode() != modeGit {
		return errors.New("sync is only available in git mode")
	}
	if err := g.git().Run("remote", "get-url", "origin"); err != nil {
		return errors.New("sync requires an 'origin' remote")
	}

	ctx := context.Background()
	if timeout, err := syncTimeoutFromEnv(false); err != nil {
		return err
	} else if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	report, err := syncFunc(ctx, g)
	renderSyncReport(report)
	if ctx.Err() != nil {
		return fmt.Errorf("syncing %s* with origin: %w", meads.RefNamespace, ctx.Err())
	}
	if err != nil {
		return err
	}
	fmt.Printf("synced %s* with origin\n", meads.RefNamespace)
	return nil
}

// syncTimeoutFromEnv returns no timeout for an ordinary foreground `md sync`:
// that is the guaranteed/blocking path. Detached workers pass
// MEADS_SYNC_TIMEOUT=10s unless the user supplied another value. Setting it
// to 0 disables the deadline in either mode.
func syncTimeoutFromEnv(background bool) (time.Duration, error) {
	raw := strings.TrimSpace(os.Getenv("MEADS_SYNC_TIMEOUT"))
	if raw == "" {
		if background {
			return defaultBackgroundSyncTimeout, nil
		}
		return 0, nil
	}
	d, err := time.ParseDuration(raw)
	if err != nil || d < 0 {
		return 0, fmt.Errorf("invalid MEADS_SYNC_TIMEOUT %q (expected a non-negative Go duration)", raw)
	}
	return d, nil
}

func renderSyncReport(report *meads.SyncReport) {
	if report == nil {
		return
	}
	if msg := integrateMessage(report.Integrate); msg != "" {
		fmt.Fprint(os.Stderr, msg)
	}
	if msg := divergenceMessage(report); msg != "" {
		fmt.Fprintln(os.Stderr, msg)
	}
}

// scheduleSync is called only after a successful CLI mutation. Scheduling is
// intentionally best-effort: a local task write must never fail because the
// PID directory is unwritable, a worker cannot be started, or origin is down.
// The explicit `md sync` command is the path for callers that need a result.
func scheduleSync(g *globals) {
	if syncDisabled() {
		if g != nil {
			g.verbosef("background sync disabled by MEADS_SYNC_DISABLE\n")
		}
		return
	}
	if g == nil || g.mode() != modeGit {
		if g != nil {
			g.verbosef("background sync skipped: file mode\n")
		}
		return
	}
	if err := g.git().Run("remote", "get-url", "origin"); err != nil {
		g.verbosef("background sync skipped: origin is not configured\n")
		return
	}
	commonDir, err := gitCommonDir(g)
	if err != nil {
		g.verbosef("background sync skipped: %v\n", err)
		return
	}
	delay, err := syncDelay(g)
	if err != nil {
		g.verbosef("background sync skipped: %v\n", err)
		return
	}
	timeout, err := syncTimeoutFromEnv(true)
	if err != nil {
		g.verbosef("background sync skipped: %v\n", err)
		return
	}

	done := g.verboseAction("queue background sync")
	err = enqueueSyncFunc(g, commonDir, delay, timeout)
	done(err)
	if err != nil {
		g.verbosef("background sync scheduling failed (local change is safe): %v\n", err)
	}
}

func syncDisabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("MEADS_SYNC_DISABLE"))) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

func syncDelay(g *globals) (time.Duration, error) {
	if raw := strings.TrimSpace(os.Getenv("MEADS_SYNC_DELAY")); raw != "" {
		d, err := time.ParseDuration(raw)
		if err != nil || d < 0 {
			return 0, fmt.Errorf("invalid MEADS_SYNC_DELAY %q (expected a non-negative Go duration)", raw)
		}
		return d, nil
	}
	cfg, err := g.gitStore().Config()
	if err != nil {
		return 0, fmt.Errorf("reading sync delay: %w", err)
	}
	return cfg.PushIntervalDuration(), nil
}

// integrateMessage renders an IntegrateReport as stderr lines ("" when
// nothing happened, the common case).
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

func joinInts(ids []int) string {
	parts := make([]string, len(ids))
	for i, id := range ids {
		parts[i] = strconv.Itoa(id)
	}
	return strings.Join(parts, ", ")
}

func divergenceMessage(r *meads.SyncReport) string {
	if r == nil || !r.Rejected {
		return ""
	}
	return "meads: push of refs/meads/* to origin was rejected: another " +
		"clone pushed different changes while this sync was running " +
		"(refs/meads/* has diverged). Your local changes are committed " +
		"locally and are safe. Run 'md sync' again to pull and reconcile; " +
		"meads will NOT force-push."
}
