package main

import (
	"errors"
	"fmt"
	"strings"

	"github.com/jpillora/meads/pkg/meads"
)

type doctorCmd struct {
	globals *globals
}

func (c *doctorCmd) Run() error {
	if err := c.globals.modeConflictErr(); err != nil {
		return err
	}
	if c.globals.mode() == modeGit {
		return c.runGit()
	}
	return c.runFile()
}

// runFile is doctor's original file-backend behaviour, unchanged: renumber
// duplicate ids (Store.Doctor), then report any circular dependency, which
// can't be auto-fixed (which edge to cut is ambiguous) and blocks every
// future mutation, so it exits non-zero.
func (c *doctorCmd) runFile() error {
	s := c.globals.store()
	fixes, err := s.Doctor()
	if err != nil {
		return err
	}
	for _, fix := range fixes {
		fmt.Printf("Duplicate ID %d detected. Renumbered to %d.\n", fix.OldID, fix.NewID)
	}
	cycles, err := s.FindCycles()
	if err != nil {
		return err
	}
	for _, cycle := range cycles {
		fmt.Printf("Circular dependency detected: %s\n", meads.FormatCycle(cycle))
	}
	if len(fixes) == 0 && len(cycles) == 0 {
		fmt.Println("no issues found")
		return nil
	}
	if len(cycles) > 0 {
		noun := "dependency"
		if len(cycles) > 1 {
			noun = "dependencies"
		}
		return fmt.Errorf("%d circular %s detected; remove a dependency to break each cycle", len(cycles), noun)
	}
	return nil
}

// runGit is doctor's git-mode counterpart (task 65 phase 8), calling
// GitStore directly rather than going through the taskStore seam: Doctor and
// Diverged are git-mode-only concepts (there is no file-backend equivalent
// of a fetched remote-tracking namespace to compare against), so forcing
// them into the cross-backend interface would just mean a dummy
// implementation on the file side. It reports three independent kinds of
// issue - renumbered duplicate ids, circular dependencies, and diverged
// tasks - and exits non-zero if anything needed a human, i.e. cycles or
// divergences (duplicate-id fixes are applied automatically, like the file
// backend, and never cause a non-zero exit on their own).
func (c *doctorCmd) runGit() error {
	gs := c.globals.gitStore()

	fixes, err := gs.Doctor()
	if err != nil {
		return err
	}
	for _, fix := range fixes {
		if fix.OldID == fix.NewID {
			// A content/ref-name mismatch repair (GitStore.Doctor's doc
			// comment, case 1): the id itself never moved, only its stored
			// task.json content was corrected to match.
			fmt.Printf("Task %d id mismatch (ref vs stored content) detected and repaired.\n", fix.OldID)
			continue
		}
		fmt.Printf("Duplicate ID %d detected. Renumbered to %d.\n", fix.OldID, fix.NewID)
	}

	cycles, err := gs.FindCycles()
	if err != nil {
		return err
	}
	for _, cycle := range cycles {
		fmt.Printf("Circular dependency detected: %s\n", meads.FormatCycle(cycle))
	}

	// Diverged reports edit/edit conflicts between local state and the last
	// `git fetch`'s remote-tracking copy (refs/meads-remote/*) - see its doc
	// comment (pkg/meads/gitdiverge.go). It never writes anything: meads
	// does not auto-merge diverging task state (task 65's MVP requirement),
	// so this is purely a report of what the two sides say, with an explicit
	// reassurance that local work is safe.
	diverged, err := gs.Diverged()
	if err != nil {
		return err
	}
	for _, d := range diverged {
		fmt.Printf("Task %d has diverged: local and remote both changed since a common ancestor (merge base %s).\n", d.ID, d.MergeBase)
		fmt.Printf("  local:  title=%q status=%s\n", d.Local.Title, d.Local.Status)
		fmt.Printf("  remote: title=%q status=%s\n", d.Remote.Title, d.Remote.Status)
	}
	if len(diverged) > 0 {
		fmt.Println("Local changes are committed and safe. Automatic reconciliation is not implemented yet (meads task 65); resolve manually - meads will NOT force-push over either side.")
	}

	if len(fixes) == 0 && len(cycles) == 0 && len(diverged) == 0 {
		fmt.Println("no issues found")
		return nil
	}

	var problems []string
	if len(cycles) > 0 {
		noun := "dependency"
		if len(cycles) > 1 {
			noun = "dependencies"
		}
		problems = append(problems, fmt.Sprintf("%d circular %s detected; remove a dependency to break each cycle", len(cycles), noun))
	}
	if len(diverged) > 0 {
		noun := "task"
		if len(diverged) > 1 {
			noun = "tasks"
		}
		problems = append(problems, fmt.Sprintf("%d diverged %s detected; resolve manually", len(diverged), noun))
	}
	if len(problems) > 0 {
		return errors.New(strings.Join(problems, "; "))
	}
	return nil
}
