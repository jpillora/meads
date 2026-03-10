package main

import "fmt"

type doctorCmd struct {
	globals *globals
}

func (c *doctorCmd) Run() error {
	fixes, err := c.globals.store().Doctor()
	if err != nil {
		return err
	}
	if len(fixes) == 0 {
		fmt.Println("no issues found")
		return nil
	}
	for _, fix := range fixes {
		fmt.Printf("Duplicate ID %d detected. Renumbered to %d.\n", fix.OldID, fix.NewID)
	}
	return nil
}
