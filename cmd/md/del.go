package main

import (
	"fmt"
	"strconv"
)

type delCmd struct {
	globals *globals
	ID      string `opts:"mode=arg" help:"Task ID to delete"`
}

func (c *delCmd) Run() error {
	id, err := strconv.Atoi(c.ID)
	if err != nil {
		return fmt.Errorf("invalid task ID: %s", c.ID)
	}
	ts, err := c.globals.tasks()
	if err != nil {
		return err
	}
	tasks, err := ts.Get([]int{id})
	if err != nil {
		return err
	}
	if err := ts.Delete(id); err != nil {
		return err
	}
	deleted := tasks[0]
	deleted.Deleted = true
	postWebhook(c.globals, "delete", deleted)
	fmt.Printf("deleted task %d\n", id)
	return nil
}
