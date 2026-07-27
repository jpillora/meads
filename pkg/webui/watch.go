package webui

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/jpillora/meads/pkg/meads"
)

// pollInterval is how often the git-mode watcher (startRefWatcher) re-lists
// task ref oids. A ref moves between a loose file under
// .git/refs/meads/tasks/** and .git/packed-refs (git pack-refs, git gc) - it
// can change with NO loose-file event at all once it has been packed, so
// fsnotify cannot be trusted for git mode the way it can for a single tasks
// file (see startFileWatcher). Polling `git for-each-ref` instead reads
// packed and loose refs uniformly, so it never misses a change regardless of
// where a ref happens to live.
//
// One second is frequent enough that a change from another agent's `md`
// invocation (or a `git fetch`) shows up in the UI promptly, while
// for-each-ref over a task set of any realistic size is sub-millisecond -
// nowhere near enough load to call this a busy-poll - and each tick already
// caps how often an SSE event can fire from this source, the same job the
// file watcher's explicit 200ms debounce does for fsnotify's burstier
// events.
const pollInterval = 1 * time.Second

// watcher watches a Store for changes and publishes a "file_changed" event
// to an eventBus whenever one is observed. Two implementations share this
// type: fsnotify on the underlying file in file mode (startFileWatcher, via
// fileLocator), or periodic ref-oid polling in git mode (startRefWatcher,
// via refSnapshotter) - see startWatcher for how the choice is made.
type watcher struct {
	cancel context.CancelFunc
	fs     *fsnotify.Watcher // nil for a ref watcher; only the file watcher has an OS handle to close
}

func (w *watcher) Close() {
	if w == nil {
		return
	}
	w.cancel()
	if w.fs != nil {
		_ = w.fs.Close()
	}
}

// refSnapshotter is implemented by git-mode stores to support
// startRefWatcher. meads.GitTasks is the implementation (cmd/md/webui.go's
// gitWatchStore embeds it): no CLI command needs TaskRefOIDs, only this
// watcher, so it is not part of the plain meads.Tasks seam.
type refSnapshotter interface {
	// TaskRefOIDs returns the current commit oid of every task ref, keyed by
	// full ref name (see GitStore.TaskRefOIDs). Called once per
	// pollInterval tick; any difference from the previous call's result (a
	// ref added, removed, or moved to a new oid) is treated as a change.
	TaskRefOIDs() (map[string]meads.OID, error)
}

// startWatcher begins watching store for changes and forwards them to bus.
// It prefers ref-oid polling (git mode, via refSnapshotter) over fsnotify
// (file mode, via fileLocator) - see pollInterval's doc comment for why
// polling is necessary in git mode specifically. In practice a given store
// implements exactly one of the two. Returns a nil watcher and nil error if
// store implements neither: nothing to watch, not a failure.
func startWatcher(ctx context.Context, store meads.Tasks, bus *eventBus, stderr io.Writer) (*watcher, error) {
	if rs, ok := store.(refSnapshotter); ok {
		return startRefWatcher(ctx, rs, bus), nil
	}
	if fl, ok := store.(fileLocator); ok {
		return startFileWatcher(ctx, fl, bus, stderr)
	}
	return nil, nil
}

// --- file mode: fsnotify on the tasks file --------------------------------

// startFileWatcher watches the parent directory of fl's file and publishes a
// debounced "file_changed" event whenever the target file is written.
func startFileWatcher(ctx context.Context, fl fileLocator, bus *eventBus, stderr io.Writer) (*watcher, error) {
	path := filepath.Join(fl.FS().Root(), fl.Path())
	dir := filepath.Dir(path)
	base := filepath.Base(path)

	fsw, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	if err := fsw.Add(dir); err != nil {
		_ = fsw.Close()
		return nil, fmt.Errorf("watch %s: %w", dir, err)
	}

	ctx, cancel := context.WithCancel(ctx)
	w := &watcher{fs: fsw, cancel: cancel}
	go w.runFile(ctx, base, bus, stderr)
	return w, nil
}

func (w *watcher) runFile(ctx context.Context, targetBase string, bus *eventBus, stderr io.Writer) {
	// Debounce: coalesce bursts into a single event every 200ms.
	const debounce = 200 * time.Millisecond
	var timer *time.Timer
	fire := func() {
		bus.publish(event{Kind: "file_changed"})
	}
	schedule := func() {
		if timer != nil {
			timer.Stop()
		}
		timer = time.AfterFunc(debounce, fire)
	}
	for {
		select {
		case <-ctx.Done():
			if timer != nil {
				timer.Stop()
			}
			return
		case e, ok := <-w.fs.Events:
			if !ok {
				return
			}
			if filepath.Base(e.Name) != targetBase {
				continue
			}
			if e.Op&(fsnotify.Write|fsnotify.Create|fsnotify.Rename|fsnotify.Remove) != 0 {
				schedule()
			}
		case err, ok := <-w.fs.Errors:
			if !ok {
				return
			}
			fmt.Fprintf(stderr, "webui: watch error: %v\n", err)
		}
	}
}

// --- git mode: ref-oid polling --------------------------------------------

// startRefWatcher periodically polls rs.TaskRefOIDs (every pollInterval) and
// publishes "file_changed" whenever the returned set of ref oids differs
// from the previous poll - see pollInterval's doc comment for why polling,
// rather than fsnotify, is used here.
func startRefWatcher(ctx context.Context, rs refSnapshotter, bus *eventBus) *watcher {
	ctx, cancel := context.WithCancel(ctx)
	w := &watcher{cancel: cancel}
	go w.runRefs(ctx, rs, bus)
	return w
}

func (w *watcher) runRefs(ctx context.Context, rs refSnapshotter, bus *eventBus) {
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	// Best-effort baseline: if this first read fails, prev stays nil and
	// the next successful read (almost certainly different from "no refs
	// known yet") fires one extra event - harmless, and simpler than
	// special-casing a failed baseline read.
	prev, _ := rs.TaskRefOIDs()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			cur, err := rs.TaskRefOIDs()
			if err != nil {
				continue // transient git failure; try again next tick
			}
			if !refOIDsEqual(prev, cur) {
				prev = cur
				bus.publish(event{Kind: "file_changed"})
			}
		}
	}
}

// refOIDsEqual reports whether a and b contain exactly the same ref name ->
// oid pairs.
func refOIDsEqual(a, b map[string]meads.OID) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}
