package main

import (
	"fmt"
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
	gs := c.globals.gitStore()
	for _, t := range tasks {
		if err := gs.ImportTask(t); err != nil {
			return fmt.Errorf("importing task %d: %w", t.ID, err)
		}
	}
	fmt.Printf("converted %d tasks: %s → %s*\n", len(tasks), c.File, meads.TasksRefPrefix)
	return nil
}

// runFromGit migrates git mode (refs/meads/tasks/*) in the current repo -
// every task, including soft-deleted ones (GitStore.LoadAll) - into File,
// preserving ids exactly via Store.ImportAll. Refuses to run if File already
// exists, so a migration never silently merges into (or clobbers) existing
// file content.
func (c *convertCmd) runFromGit() error {
	if !c.globals.inGitRepo() {
		return fmt.Errorf("--from-git requires a git repository")
	}
	gs := c.globals.gitStore()
	tasks, err := gs.LoadAll()
	if err != nil {
		return err
	}
	for i := range tasks {
		tasks[i] = syncMetaFromFields(tasks[i])
	}
	dst := meads.NewFileStore(c.File)
	if err := dst.ImportAll(tasks); err != nil {
		return err
	}
	fmt.Printf("converted %d tasks: %s* → %s\n", len(tasks), meads.TasksRefPrefix, c.File)
	return nil
}

// syncMetaFromFields brings t.Meta into agreement with t's own dedicated
// fields for every key the markdown/CSV formatters read from Meta rather
// than the field itself (status, priority, type, depends-on, close-reason,
// tags - see FormatTask/FormatCSV). This is required for EVERY git-mode
// source, not just an edge case: Task.MarshalJSON deliberately excludes
// every known meta key from the "meta" JSON object it writes (to avoid
// duplicating e.g. "status" against the dedicated top-level field), and
// Task has no custom UnmarshalJSON to reconstruct them - so a task read
// back from a git ref (GitStore.LoadAll, Get, etc.) always has an empty
// Meta for these keys, regardless of how it was created or last mutated.
// GitStore.Claim (gitmutate.go, which also sets Status directly rather
// than through SetStatus) is one illustrative way to reach this, but the
// gap exists for every git-sourced task. Deleted/StatusReason/AgentID/
// FilesInScope need no such treatment: FormatTask/FormatCSV already read
// those straight from their dedicated fields, never from Meta (see
// markdown.go's FormatTask).
func syncMetaFromFields(t meads.Task) meads.Task {
	if t.Status != "" {
		t.SetStatus(t.Status)
	}
	if t.Priority != "" {
		t.SetPriority(t.Priority)
	}
	if t.Type != "" {
		t.SetType(t.Type)
	}
	if len(t.DependsOn) > 0 {
		t.SetDependsOn(t.DependsOn)
	}
	if t.CloseReason != "" {
		t.SetCloseReason(t.CloseReason)
	}
	if len(t.Tags) > 0 {
		t.SetTags(t.Tags)
	}
	return t
}
