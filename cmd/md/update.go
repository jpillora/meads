package main

import (
	"fmt"
	"strconv"

	"github.com/jpillora/meads/pkg/meads"
)

type updateCmd struct {
	ID       string `opts:"mode=arg" help:"Task ID to update"`
	Status   string `help:"Set task status"`
	Priority string `help:"Set task priority"`
	Title    string `help:"Set task title"`
}

func (c *updateCmd) Run() error {
	err := meads.Update(tasksFile, c.ID, func(t *meads.Task) {
		if c.Status != "" {
			t.SetStatus(c.Status)
		}
		if c.Priority != "" {
			if n, err := strconv.Atoi(c.Priority); err == nil {
				t.SetPriority(n)
			}
		}
		if c.Title != "" {
			t.Title = c.Title
		}
	})
	if err != nil {
		return err
	}
	fmt.Printf("updated task %s\n", c.ID)
	return nil
}
