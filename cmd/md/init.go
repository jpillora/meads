package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/jpillora/meads/pkg/meads"
)

type initCmd struct {
	globals  *globals
	CSV      bool `help:"Create a TASKS.csv file instead of using git mode"`
	Git      bool `help:"Force git mode (refs/meads/*), the default inside a git repository"`
	Markdown bool `opts:"name=md" help:"Force a TASKS.md file, even inside a git repository"`
}

func (c *initCmd) Run() error {
	b, err := c.backend()
	if err != nil {
		return err
	}
	if b == meads.BackendGit {
		return c.runGit()
	}
	res, err := meads.InitTasks(c.globals.Dir, b)
	if err != nil {
		return c.convertHint(err)
	}
	fmt.Printf("created %s\n", filepath.Base(res.Tasks.Location()))
	return nil
}

// convertHint appends the migration route to InitTasks' "already exists"
// refusal, but only when git mode would otherwise have been the default -
// i.e. an existing tasks file is the sole reason this is not a git-mode repo.
// Bare, the error says the file is there and leaves "so how do I get git mode?"
// unanswered at the exact moment it is being asked.
func (c *initCmd) convertHint(err error) error {
	if err == nil || c.CSV || c.Git || c.Markdown || c.globals.GitMode || c.globals.FileMode {
		return err
	}
	name, ok := c.existingTasksFile()
	if !ok || !c.globals.inGitRepo() {
		return err
	}
	return fmt.Errorf("%w\n\ngit mode is the default in a repository; migrate the existing file with:\n  md convert %s --to-git", err, name)
}

// backend picks what `md init` should create.
//
// Git mode is the default inside a git repository. It is the better backend
// wherever it can run - per-task refs give each task its own history, every
// linked worktree shares one task list, and concurrent writers compare-and-swap
// a ref instead of contending on one file - and its only requirement is a repo.
// Outside one it cannot run at all, so a plain TASKS.md is the fallback rather
// than an error.
//
// An existing tasks file overrides that default, and deliberately: a repo with
// a TASKS.md has already chosen, and quietly initialising git mode beside it
// would leave the file shadowed (auto-detection prefers refs) and its tasks
// invisible. Falling through to the file backend surfaces InitTasks' own
// "already exists" refusal, which convertHint below turns into directions.
//
// Explicit flags win over all of it: --git, --csv, --md, and the global
// --git/--file. Precedence is checked, not assumed, so a contradictory pair
// is an error rather than a silent winner.
func (c *initCmd) backend() (meads.Backend, error) {
	if err := c.globals.modeConflictErr(); err != nil {
		return 0, err
	}
	git := c.Git || c.globals.GitMode
	file := c.Markdown || c.globals.FileMode
	switch {
	case c.CSV && git:
		return 0, fmt.Errorf("cannot use both --git and --csv")
	case git && file:
		return 0, fmt.Errorf("cannot use both --git and --md")
	case c.CSV:
		return meads.BackendCSV, nil
	case git:
		return meads.BackendGit, nil
	case file:
		return meads.BackendMarkdown, nil
	}
	// The backend must MATCH the existing file, not merely be a file backend:
	// answering "TASKS.csv is here" with BackendMarkdown finds no collision
	// and cheerfully writes an empty TASKS.md beside it, leaving two tasks
	// files where the user has one.
	if name, ok := c.existingTasksFile(); ok {
		if strings.HasSuffix(name, ".csv") {
			return meads.BackendCSV, nil
		}
		return meads.BackendMarkdown, nil
	}
	if c.globals.inGitRepo() {
		return meads.BackendGit, nil
	}
	return meads.BackendMarkdown, nil
}

// existingTasksFile returns the name of a tasks file already in Dir, if any.
//
// It checks the names InitTasks itself would create, which are hardcoded
// there, so it deliberately ignores --tasks-file/MEADS_TASK_FILE: the question
// is whether an init would collide, not which file this invocation reads. CSV
// is checked first, matching bareDefaultTasksFile's own precedence.
func (c *initCmd) existingTasksFile() (string, bool) {
	for _, name := range []string{"TASKS.csv", "TASKS.md"} {
		if _, err := os.Stat(filepath.Join(c.globals.Dir, name)); err == nil {
			return name, true
		}
	}
	return "", false
}

// runGit initializes git mode in the current repo via meads.InitTasks and
// prints the outcome - the work (and all refusal checks) lives in pkg/meads
// so library callers get the same behaviour with no stdout side effects;
// this wrapper is printing only.
func (c *initCmd) runGit() error {
	res, err := meads.InitTasks(c.globals.Dir, meads.BackendGit)
	if err != nil {
		return err
	}
	if res.AdoptedTasks > 0 {
		// The repo was a clone of an already-initialised git-mode remote:
		// origin's refs were fetched, no fresh config ref seeded (see
		// meads.InitTasks).
		fmt.Printf("adopted %d task refs from origin\n", res.AdoptedTasks)
	} else {
		fmt.Printf("initialized git mode (%s*)\n", meads.RefNamespace)
	}
	printFetchRefspec(res.FetchRefspec)
	return nil
}

// printFetchRefspec reports an EnsureFetchRefspec outcome. Shared with
// convert.go and doctor.go, which reach the same setup step by other routes
// (meads.EnsureGitInit), so all three say the same thing about it.
func printFetchRefspec(outcome meads.FetchRefspecOutcome) {
	switch outcome {
	case meads.FetchRefspecNoOrigin:
		fmt.Println("no 'origin' remote configured — skipping fetch refspec setup")
	case meads.FetchRefspecAlreadyPresent:
		fmt.Println("fetch refspec already configured on origin")
	case meads.FetchRefspecAdded:
		fmt.Printf("added fetch refspec %s to origin\n", meads.FetchRefspec)
	}
}
