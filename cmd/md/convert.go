package main

import (
	"fmt"
	"strings"

	"github.com/jpillora/meads/pkg/meads"
)

type convertCmd struct {
	globals *globals
	File    string `opts:"mode=arg" help:"source file to convert (TASKS.md or TASKS.csv)"`
}

func (c *convertCmd) Run() error {
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
