package main

import (
	"fmt"

	"github.com/jpillora/meads/pkg/meads"
)

type delCmd struct {
	ID string `opts:"mode=arg" help:"Task ID to delete"`
}

func (c *delCmd) Run() error {
	if err := meads.Delete(tasksFile, c.ID); err != nil {
		return err
	}
	fmt.Printf("deleted task %s\n", c.ID)
	return nil
}
