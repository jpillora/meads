package main

import (
	"fmt"

	"github.com/jpillora/meads/pkg/meads"
)

type beadsImportCmd struct {
	globals *globals
}

func (c *beadsImportCmd) Run() error {
	imp, err := meads.GetImporter("beads")
	if err != nil {
		return err
	}
	result, err := c.globals.store().RunImport(imp)
	if err != nil {
		return err
	}
	fmt.Printf("imported %d, skipped %d\n", result.Imported, result.Skipped)
	return nil
}
