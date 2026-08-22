package main

import (
	"fmt"
	"os"
	"sort"
	"strconv"

	"github.com/jpillora/meads/pkg/meads"
)

// restoreCmd is `md del`'s inverse: it clears a tombstone's deleted flag so
// the task rejoins `md list`/`md ready`.
//
// --all exists because tombstones arrive in sets, not one at a time - a
// `md convert --to-git` migration recreates every id the tasks file ever
// held so none is reused, and each one it cannot find in the file's current
// state lands deleted. Restoring such a set whole also settles its internal
// dependencies in one step: a tombstone keeps its own DependsOn (see
// GitStore.Restore), so restoring ids piecemeal leaves edges pointing at
// tombstones, which readyTasks then ignores rather than honours.
type restoreCmd struct {
	globals *globals
	All     bool     `help:"Restore every deleted task"`
	JSON    bool     `help:"Output the restored tasks as JSON"`
	DryRun  bool     `opts:"name=dry-run" help:"Report what would be restored without writing anything"`
	IDs     []string `opts:"mode=arg,name=id" help:"Task IDs to restore (omit with --all)"`
}

func (c *restoreCmd) Run() error {
	if c.All && len(c.IDs) > 0 {
		return fmt.Errorf("--all takes no ids")
	}
	if !c.All && len(c.IDs) == 0 {
		return fmt.Errorf("no task ids given (use --all to restore every deleted task)")
	}
	ts, err := c.globals.tasks()
	if err != nil {
		return err
	}
	history, err := ts.GetHistory()
	if err != nil {
		return err
	}
	deleted := map[int]meads.Task{}
	known := map[int]bool{}
	for _, t := range history {
		known[t.ID] = true
		if t.Deleted {
			deleted[t.ID] = t
		}
	}

	var ids []int
	if c.All {
		for id := range deleted {
			ids = append(ids, id)
		}
		sort.Ints(ids)
	} else {
		for _, s := range c.IDs {
			n, err := strconv.Atoi(s)
			if err != nil {
				return fmt.Errorf("invalid task ID: %s", s)
			}
			if _, ok := deleted[n]; !ok {
				// Worth separating: "not deleted" on an id that does not
				// exist reads as "it's fine, it's still there", when in fact
				// nothing by that number is left - the likely cause being a
				// `md del --force`, which leaves nothing to restore.
				if !known[n] {
					return fmt.Errorf("task %d not found (an erased task leaves nothing to restore)", n)
				}
				return fmt.Errorf("task %d is not deleted", n)
			}
			ids = append(ids, n)
		}
	}
	if len(ids) == 0 {
		fmt.Println("no deleted tasks to restore")
		return nil
	}

	// Which restored tasks will still point at a tombstone once this batch
	// lands. Worth reporting because the effect is the SURPRISING direction:
	// readyTasks skips a dependency that is absent from the active set
	// altogether, and a deleted one has already been filtered out of it - so
	// the edge is silently ignored and the restored task can show up in
	// `md ready` despite depending on something that was never finished.
	// (See TestGitStore_Restore_AllowsStillDeletedDependency, which pins
	// this down; it is not the "stays blocked forever" that SoftDelete's own
	// doc comment implies.)
	restoring := make(map[int]bool, len(ids))
	for _, id := range ids {
		restoring[id] = true
	}
	danglingDeps := map[int][]int{}
	for _, id := range ids {
		for _, dep := range deleted[id].DependsOn {
			if _, isTombstone := deleted[dep]; isTombstone && !restoring[dep] {
				danglingDeps[id] = append(danglingDeps[id], dep)
			}
		}
	}

	if c.DryRun {
		fmt.Printf("would restore %d task(s)\n", len(ids))
		reportRestored(ids, deleted, danglingDeps, c.JSON)
		return nil
	}

	var restored []int
	for _, id := range ids {
		if err := ts.Restore(id); err != nil {
			// Report what already landed: a partial restore is a real
			// state the user has to reason about, and silently dropping
			// the ids that succeeded would hide it.
			if len(restored) > 0 {
				fmt.Fprintf(os.Stderr, "restored %d task(s) before failing\n", len(restored))
			}
			return fmt.Errorf("restoring task %d: %w", id, err)
		}
		restored = append(restored, id)
		task := deleted[id]
		task.Deleted = false
		postWebhook(c.globals, "restore", task)
	}
	autoPush(c.globals)

	fmt.Printf("restored %d task(s)\n", len(restored))
	reportRestored(restored, deleted, danglingDeps, c.JSON)
	return nil
}

// reportRestored prints the restored set, then one warning per task left
// pointing at a tombstone. The warnings go to stderr so --json's stdout
// stays machine-readable.
func reportRestored(ids []int, deleted map[int]meads.Task, danglingDeps map[int][]int, asJSON bool) {
	tasks := make([]meads.Task, 0, len(ids))
	for _, id := range ids {
		t := deleted[id]
		t.Deleted = false
		tasks = append(tasks, t)
	}
	printTasks(tasks, asJSON)
	danglingIDs := make([]int, 0, len(danglingDeps))
	for id := range danglingDeps {
		danglingIDs = append(danglingIDs, id)
	}
	sort.Ints(danglingIDs)
	for _, id := range danglingIDs {
		fmt.Fprintf(os.Stderr, "warning: task %d depends on still-deleted task(s) %v — those edges are ignored, so it can read as ready anyway (restore them too, or drop the dep with 'md rm-dep')\n", id, danglingDeps[id])
	}
}
