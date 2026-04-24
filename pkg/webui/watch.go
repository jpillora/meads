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

// watcher watches the parent directory of a tasks file and publishes a
// debounced "file_changed" event whenever the target file is written.
type watcher struct {
	fs     *fsnotify.Watcher
	cancel context.CancelFunc
}

func (w *watcher) Close() {
	if w == nil {
		return
	}
	w.cancel()
	_ = w.fs.Close()
}

// startWatcher begins watching the store's file path and forwards changes to the bus.
// Returns nil watcher if fsnotify is unavailable for this platform/path.
func startWatcher(ctx context.Context, store *meads.Store, bus *eventBus, stderr io.Writer) (*watcher, error) {
	path := filepath.Join(store.FS().Root(), store.Path())
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
	go w.run(ctx, base, bus, stderr)
	return w, nil
}

func (w *watcher) run(ctx context.Context, targetBase string, bus *eventBus, stderr io.Writer) {
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
