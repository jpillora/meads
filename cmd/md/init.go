package main

import (
	"fmt"
	"os"

	"github.com/jpillora/meads/pkg/meads"
)

type initCmd struct {
	globals *globals
	CSV     bool `help:"Create a TASKS.csv file instead of TASKS.md"`
}

func (c *initCmd) Run() error {
	file := "TASKS.md"
	content := ""
	if c.CSV {
		file = "TASKS.csv"
		content = meads.InitCSV()
	}
	if _, err := os.Stat(file); err == nil {
		return fmt.Errorf("%s already exists", file)
	}
	if err := os.WriteFile(file, []byte(content), 0644); err != nil {
		return fmt.Errorf("creating %s: %w", file, err)
	}
	fmt.Printf("created %s\n", file)
	return nil
}
