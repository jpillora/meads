package main

import (
	"fmt"

	"github.com/jpillora/meads/pkg/meads"
)

type importCmd struct {
	globals *globals
	Target  string `opts:"mode=arg" help:"Import target (e.g. beads)"`
}

func (c *importCmd) Run() error {
	imp, err := meads.GetImporter(c.Target)
	if err != nil {
		return err
	}
	result, err := meads.RunImport(c.globals.TasksFile, imp)
	if err != nil {
		return err
	}
	fmt.Printf("imported %d, skipped %d\n", result.Imported, result.Skipped)
	return nil
}
