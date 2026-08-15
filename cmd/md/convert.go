package main

import (
	"fmt"
	"sort"
	"strings"

	"github.com/jpillora/meads/pkg/meads"
)

type convertCmd struct {
	globals *globals
	File    string `opts:"mode=arg" help:"tasks file to convert (TASKS.md or TASKS.csv); with --to-git/--from-git, the file-mode side of the migration"`
	ToGit   bool   `opts:"mode=flag,name=to-git" help:"Migrate File into git mode (refs/meads/tasks/*) in the current repo, preserving ids and soft-deleted tasks"`
	FromGit bool   `opts:"mode=flag,name=from-git" help:"Migrate git mode (refs/meads/tasks/*) in the current repo into File, preserving ids and soft-deleted tasks"`
}

func (c *convertCmd) Run() error {
	if c.ToGit && c.FromGit {
		return fmt.Errorf("cannot use both --to-git and --from-git")
	}
	if c.ToGit {
		return c.runToGit()
	}
	if c.FromGit {
		return c.runFromGit()
	}
	return c.runFileToFile()
}

// runFileToFile is convert's original behaviour: swap TASKS.md <-> TASKS.csv
// by extension, reassigning fresh ids via AddMany. Soft-deleted (tombstone)
// tasks are not carried over - Get already excludes them, unchanged by
// git-mode migration phase 9.
func (c *convertCmd) runFileToFile() error {
	src := meads.NewFileStore(c.File)
	tasks, err := src.Get(nil)
	if err != nil {
		return err
	}
	var dstFile string
	if strings.HasSuffix(c.File, ".csv") {
		dstFile = strings.TrimSuffix(c.File, ".csv") + ".md"
	} else {
		dstFile = strings.TrimSuffix(c.File, ".md") + ".csv"
	}
	// Zero out IDs so AddMany can assign them fresh.
	for i := range tasks {
		tasks[i].ID = 0
	}
	dst := meads.NewFileStore(dstFile)
	if _, err := dst.AddMany(tasks); err != nil {
		return err
	}
	fmt.Printf("converted %d tasks: %s → %s\n", len(tasks), c.File, dstFile)
	return nil
}

// runToGit migrates File - every task it currently holds, including
// soft-deleted (tombstone) rows - into git mode (refs/meads/tasks/*) in the
// current repo, preserving ids exactly via GitStore.ImportTask rather than
// reassigning them. Refuses to run if git mode already has any tasks, so a
// migration never silently interleaves with (or clobbers) existing refs.
//
// The working file alone is not the whole picture: `md auto-delete` prunes
// closed tasks out of it entirely once committed (cmd/md/auto_delete.go), so
// they survive only in git history - `md get <id>` already recovers them from
// there (Store.GetWithHistory). Reading just the file here would silently
// free their ids for reuse and leave `md get <id>` unable to find them once
// the file-mode history is abandoned post-migration (TASKS #70). So every id
// ever committed is also recovered, via Store.AllHistoricalTasks - reusing
// GetWithHistory's own history-walking machinery rather than a second
// implementation - and imported too, soft-deleted, for any id the working
// file doesn't already hold a (newer) version of.
//
// This history walk always runs; there is no flag to skip it. It is one `git
// log` plus one `git show` per commit that ever touched File - the same
// per-commit cost `md get`/`md list --history` already pay on demand today -
// and on this repo's own ~100-commit TASKS.md history the whole walk took
// well under a second. --to-git is a rare, explicit, one-time migration, not
// a hot path, so trading a little more time for never silently losing a task
// id is the right default; an opt-out flag would also risk reintroducing
// exactly the bug this fixes, for a command that only ever runs once per
// repo.
func (c *convertCmd) runToGit() error {
	if !c.globals.inGitRepo() {
		return fmt.Errorf("--to-git requires a git repository")
	}
	rs := meads.NewRefStore(c.globals.git())
	existing, err := rs.ListRefs(meads.TasksRefPrefix)
	if err != nil {
		return fmt.Errorf("checking for existing git-mode tasks: %w", err)
	}
	if len(existing) > 0 {
		return fmt.Errorf("git mode already has tasks (%s already has refs); refusing to migrate into a non-empty target", meads.TasksRefPrefix)
	}

	src := meads.NewFileStore(c.File)
	tasks, err := src.GetAll()
	if err != nil {
		return err
	}
	present := make(map[int]bool, len(tasks))
	for _, t := range tasks {
		present[t.ID] = true
	}
	// Ids pruned from the working file but still present in git history:
	// recovered and imported soft-deleted, in ascending order purely for
	// deterministic output (ImportTask itself doesn't care - see its doc
	// comment). An id present in both is skipped here: the working file's
	// version was already collected above and is the newer of the two.
	historical := src.AllHistoricalTasks(c.globals.git())
	recoveredIDs := make([]int, 0, len(historical))
	for id := range historical {
		if !present[id] {
			recoveredIDs = append(recoveredIDs, id)
		}
	}
	sort.Ints(recoveredIDs)
	for _, id := range recoveredIDs {
		t := historical[id]
		t.Deleted = true // pruned because it was closed/deleted; must not resurrect as live work
		tasks = append(tasks, t)
	}

	gs := c.globals.gitStore()
	for _, t := range tasks {
		if err := gs.ImportTask(t); err != nil {
			return fmt.Errorf("importing task %d: %w", t.ID, err)
		}
	}
	fmt.Printf("converted %d tasks (%d recovered from git history): %s → %s*\n", len(tasks), len(recoveredIDs), c.File, meads.TasksRefPrefix)
	return nil
}

// runFromGit migrates git mode (refs/meads/tasks/*) in the current repo -
// every task, including soft-deleted ones (GitStore.LoadAll) - into File,
// preserving ids exactly via Store.ImportAll. Refuses to run if File already
// exists, so a migration never silently merges into (or clobbers) existing
// file content.
//
// Git-sourced tasks arrive with an empty Meta for every field-backed key
// (Task.MarshalJSON excludes them all, and Task has no UnmarshalJSON to put
// them back), which used to need a syncMetaFromFields pre-pass here so the
// markdown formatter would not drop status/priority/type/depends-on/
// close-reason on the way out. FormatTask now reads those from the dedicated
// fields itself - as FormatCSV always did - so the pre-pass is gone (TASKS
// #92); there is nothing left for this to fix up.
func (c *convertCmd) runFromGit() error {
	if !c.globals.inGitRepo() {
		return fmt.Errorf("--from-git requires a git repository")
	}
	gs := c.globals.gitStore()
	tasks, err := gs.LoadAll()
	if err != nil {
		return err
	}
	dst := meads.NewFileStore(c.File)
	if err := dst.ImportAll(tasks); err != nil {
		return err
	}
	fmt.Printf("converted %d tasks: %s* → %s\n", len(tasks), meads.TasksRefPrefix, c.File)
	return nil
}
