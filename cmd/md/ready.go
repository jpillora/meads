package main

import (
	"fmt"

	"github.com/jpillora/meads/pkg/meads"
)

type readyCmd struct{}

func (c *readyCmd) Run() error {
	tasks, err := meads.Ready(tasksFile)
	if err != nil {
		return err
	}
	for _, t := range tasks {
		fmt.Printf("%s %s\n", t.ID, t.Title)
	}
	return nil
}
