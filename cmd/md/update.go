package main

import (
	"fmt"
	"strconv"

	"github.com/jpillora/meads/pkg/meads"
)

type updateCmd struct {
	globals  *globals
	ID       string `opts:"mode=arg" help:"Task ID to update"`
	Status   string `help:"Set task status"`
	Priority string `help:"Set task priority"`
	Title    string `help:"Set task title"`
}

func (c *updateCmd) Run() error {
	id, err := strconv.Atoi(c.ID)
	if err != nil {
		return fmt.Errorf("invalid task ID: %s", c.ID)
	}
	var updated meads.Task
	err = meads.Update(c.globals.TasksFile, id, func(t *meads.Task) {
		if c.Status != "" {
			t.SetStatus(c.Status)
		}
		if c.Priority != "" {
			t.SetPriority(c.Priority)
		}
		if c.Title != "" {
			t.Title = c.Title
		}
		updated = *t
	})
	if err != nil {
		return err
	}
	postWebhook(c.globals, "update", updated)
	fmt.Printf("updated task %d\n", id)
	return nil
}
