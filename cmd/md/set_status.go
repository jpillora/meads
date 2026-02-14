package main

import (
	"fmt"
	"strconv"

	"github.com/jpillora/meads/pkg/meads"
)

type setStatusCmd struct {
	ID     string `opts:"mode=arg" help:"Task ID"`
	Status string `opts:"mode=arg" help:"New status (open, inprogress, closed)"`
}

func (c *setStatusCmd) Run() error {
	id, err := strconv.Atoi(c.ID)
	if err != nil {
		return fmt.Errorf("invalid task ID: %s", c.ID)
	}
	err = meads.Update(tasksFile, id, func(t *meads.Task) {
		t.SetStatus(c.Status)
	})
	if err != nil {
		return err
	}
	fmt.Printf("task %d status set to %s\n", id, c.Status)
	return nil
}
