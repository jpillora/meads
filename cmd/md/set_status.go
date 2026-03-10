package main

import (
	"fmt"
	"strconv"

	"github.com/jpillora/meads/pkg/meads"
)

type setStatusCmd struct {
	globals *globals
	ID      string `opts:"mode=arg" help:"Task ID"`
	Status  string `opts:"mode=arg" help:"New status (draft, open, inprogress, closed)"`
	Reason  string `help:"Reason for status change"`
}

func (c *setStatusCmd) Run() error {
	id, err := strconv.Atoi(c.ID)
	if err != nil {
		return fmt.Errorf("invalid task ID: %s", c.ID)
	}
	var updated meads.Task
	err = c.globals.store().Update(id, func(t *meads.Task) {
		t.SetStatus(c.Status)
		if c.Reason != "" {
			t.StatusReason = c.Reason
		}
		updated = *t
	})
	if err != nil {
		return err
	}
	postWebhook(c.globals, "update", updated)
	fmt.Printf("task %d status set to %s\n", id, c.Status)
	return nil
}
