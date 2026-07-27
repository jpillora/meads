package main

import (
	"fmt"
	"path/filepath"

	"github.com/jpillora/meads/pkg/meads"
)

type initCmd struct {
	globals *globals
	CSV     bool `help:"Create a TASKS.csv file instead of TASKS.md"`
	Git     bool `help:"Initialize git mode (refs/meads/*) in the current repo instead of creating a tasks file"`
}

func (c *initCmd) Run() error {
	if c.Git {
		if c.CSV {
			return fmt.Errorf("cannot use both --git and --csv")
		}
		return c.runGit()
	}
	b := meads.BackendMarkdown
	if c.CSV {
		b = meads.BackendCSV
	}
	res, err := meads.InitTasks(c.globals.Dir, b)
	if err != nil {
		return err
	}
	fmt.Printf("created %s\n", filepath.Base(res.Tasks.Location()))
	return nil
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
	fmt.Printf("initialized git mode (%s*)\n", meads.RefNamespace)
	switch res.FetchRefspec {
	case meads.FetchRefspecNoOrigin:
		fmt.Println("no 'origin' remote configured — skipping fetch refspec setup")
	case meads.FetchRefspecAlreadyPresent:
		fmt.Println("fetch refspec already configured on origin")
	case meads.FetchRefspecAdded:
		fmt.Printf("added fetch refspec %s to origin\n", meads.FetchRefspec)
	}
	return nil
}
