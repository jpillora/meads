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

// watcher watches a Tasks for changes and publishes a "file_changed" event
// to an eventBus whenever one is observed. Two implementations share this
// type: fsnotify on the underlying file in file mode (startFileWatcher), or
// periodic revision polling in git mode (startRevWatcher) - see
// startWatcher for how the choice is made.
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

// startWatcher begins watching store for changes and forwards them to bus,
// choosing the strategy from store.Backend(): revision polling in git mode,
// fsnotify in file mode - see pollInterval's doc comment for why polling is
// necessary in git mode specifically.
//
// It used to discover the two strategies by type-asserting a pair of
// optional interfaces (refSnapshotter, fileLocator), with a third "neither,
// so watch nothing" branch that was unreachable for any real store. Backend
// is on meads.Tasks itself and is total, so the choice is now a switch over
// two cases and every store is watchable.
func startWatcher(ctx context.Context, store meads.Tasks, bus *eventBus, stderr io.Writer) (*watcher, error) {
	if store.Backend() == meads.BackendGit {
		return startRevWatcher(ctx, store, bus), nil
	}
	return startFileWatcher(ctx, store, bus, stderr)
}

// --- file mode: fsnotify on the tasks file --------------------------------

// startFileWatcher watches the parent directory of store's file and
// publishes a debounced "file_changed" event whenever the target file is
// written. The file is meads.Tasks.Location(), which for a file backend is
// the absolute on-disk path.
func startFileWatcher(ctx context.Context, store meads.Tasks, bus *eventBus, stderr io.Writer) (*watcher, error) {
	path := store.Location()
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

// --- git mode: revision polling -------------------------------------------

// startRevWatcher periodically polls store.Revision (every pollInterval)
// and publishes "file_changed" whenever it differs from the previous poll -
// see pollInterval's doc comment for why polling, rather than fsnotify, is
// used here.
//
// Revision is the same one `git for-each-ref` over the task refs it used to
// compare by hand as a map of ref name -> oid, folded to a single token by
// the library, so a tick is one string comparison instead of a map walk and
// the map-equality helper is gone.
func startRevWatcher(ctx context.Context, store meads.Tasks, bus *eventBus) *watcher {
	ctx, cancel := context.WithCancel(ctx)
	w := &watcher{cancel: cancel}
	go w.runRevs(ctx, store, bus)
	return w
}

func (w *watcher) runRevs(ctx context.Context, store meads.Tasks, bus *eventBus) {
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	// Best-effort baseline: if this first read fails, prev stays "" and the
	// next successful read (necessarily different, since a real revision is
	// never empty) fires one extra event - harmless, and simpler than
	// special-casing a failed baseline read.
	prev, _ := store.Revision()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			cur, err := store.Revision()
			if err != nil {
				continue // transient git failure; try again next tick
			}
			if cur != prev {
				prev = cur
				bus.publish(event{Kind: "file_changed"})
			}
		}
	}
}
