package main

import (
	"fmt"
	"strconv"

	"github.com/jpillora/meads/pkg/meads"
)

type rmDepCmd struct {
	globals *globals
	Child   string `opts:"mode=arg" help:"Child task ID"`
	Parent  string `opts:"mode=arg" help:"Parent task ID to remove as dependency"`
}

func (c *rmDepCmd) Run() error {
	child, err := strconv.Atoi(c.Child)
	if err != nil {
		return fmt.Errorf("invalid child task ID: %s", c.Child)
	}
	parent, err := strconv.Atoi(c.Parent)
	if err != nil {
		return fmt.Errorf("invalid parent task ID: %s", c.Parent)
	}
	ts, err := c.globals.tasks()
	if err != nil {
		return err
	}
	var updated meads.Task
	err = ts.Update(child, func(t *meads.Task) {
		t.RemoveDep(parent)
		updated = *t
	})
	if err != nil {
		return err
	}
	postWebhook(c.globals, "update", updated)
	fmt.Printf("task %d no longer depends on %d\n", child, parent)
	return nil
}
