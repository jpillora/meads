package main

import (
	"fmt"
	"time"

	"github.com/jpillora/meads/pkg/meads"
)

// stageBackoff is the delay before each retry after a failed `git add`, so the
// whole sequence gives up after roughly 1.2s. Overridden in tests.
var stageBackoff = []time.Duration{
	20 * time.Millisecond,
	40 * time.Millisecond,
	80 * time.Millisecond,
	160 * time.Millisecond,
	320 * time.Millisecond,
	640 * time.Millisecond,
}

// stageFile stages path into the index, retrying while another git process
// holds .git/index.lock.
//
// Both pre-commit hooks call this. A single `git add` is not reliable here:
// any concurrent index-writing git command takes the same lock, and the hooks
// run in exactly the window that provokes one. Rewriting the tasks file leaves
// its stat info stale, so the next `git status`/`git diff` — from a dirty-state
// shell prompt, an editor, or a sibling agent — must re-hash it and write the
// refreshed index back. Losing that race aborts the user's commit with a bare
// "exit status 128", which is what task 67 reported. Waiting a moment and
// retrying costs nothing and clears it, since the competing operation is short.
//
// A lock that outlives the backoff is not contention but a crashed git process
// leaving a stale .git/index.lock behind, which no amount of waiting fixes; the
// error is returned so the caller can restore state and report it.
func stageFile(git meads.Git, path string) error {
	var err error
	for attempt := 0; ; attempt++ {
		if err = git.Run("add", path); err == nil {
			return nil
		}
		if attempt >= len(stageBackoff) || !meads.IsIndexLocked(err) {
			return fmt.Errorf("staging %s: %w", path, err)
		}
		time.Sleep(stageBackoff[attempt])
	}
}
