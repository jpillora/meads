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
		return errGitModeUnsupported("doctor")
	}
	s := c.globals.store()
	fixes, err := s.Doctor()
	if err != nil {
		return err
	}
	for _, fix := range fixes {
		fmt.Printf("Duplicate ID %d detected. Renumbered to %d.\n", fix.OldID, fix.NewID)
	}
	// Detect circular dependencies. These can't be auto-fixed (which edge to
	// cut is ambiguous), and a cycle present in the file blocks every future
	// mutation, so report each one and exit non-zero.
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
