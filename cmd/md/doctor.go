package main

import (
	"fmt"

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
// GitStore directly rather than going through the meads.Tasks seam: Doctor
// is a git-mode-only concept here (there is no file-backend equivalent of a
// fetched remote-tracking namespace to compare against). It reports two
// independent kinds of issue - repaired content mismatches and re-homed
// contended tasks (duplicates and divergences, all auto-applied by
// GitStore.Doctor's convergent renumbering, task 86), and circular
// dependencies, which can't be auto-fixed (which edge to cut is ambiguous)
// and block every future mutation, so they exit non-zero. Fixes never cause
// a non-zero exit on their own.
func (c *doctorCmd) runGit() error {
	gs := c.globals.gitStore()

	fixes, err := gs.Doctor()
	if err != nil {
		return err
	}
	for _, fix := range fixes {
		switch fix.Kind {
		case meads.DoctorFixMismatch:
			// A content/ref-name mismatch repair (GitStore.Doctor's doc
			// comment, case 1): the id itself never moved, only its stored
			// task.json content was corrected to match.
			fmt.Printf("Task %d id mismatch (ref vs stored content) detected and repaired.\n", fix.OldID)
		case meads.DoctorFixDiverged:
			fmt.Printf("Task %d diverged with the fetched remote. Local version renumbered to %d (the id now holds the remote version).\n", fix.OldID, fix.NewID)
		default:
			fmt.Printf("Duplicate ID %d detected. Renumbered to %d (local version moved; the id now holds the remote version).\n", fix.OldID, fix.NewID)
		}
	}

	cycles, err := gs.FindCycles()
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
