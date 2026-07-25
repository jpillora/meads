package main

import (
	"fmt"
	"strconv"

	"github.com/jpillora/meads/pkg/meads"
)

type addDepCmd struct {
	globals *globals
	Child   string `opts:"mode=arg" help:"Child task ID"`
	Parent  string `opts:"mode=arg" help:"Parent task ID to add as dependency"`
}

func (c *addDepCmd) Run() error {
	child, err := strconv.Atoi(c.Child)
	if err != nil {
		return fmt.Errorf("invalid child task ID: %s", c.Child)
	}
	parent, err := strconv.Atoi(c.Parent)
	if err != nil {
		return fmt.Errorf("invalid parent task ID: %s", c.Parent)
	}
	if child == parent {
		return fmt.Errorf("a task cannot depend on itself")
	}
	ts, err := c.globals.tasks()
	if err != nil {
		return err
	}
	var updated meads.Task
	err = ts.Update(child, func(t *meads.Task) {
		t.AddDep(parent)
		updated = *t
	})
	if err != nil {
		return err
	}
	postWebhook(c.globals, "update", updated)
	autoPush(c.globals)
	fmt.Printf("task %d now depends on %d\n", child, parent)
	return nil
}
