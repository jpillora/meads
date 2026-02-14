package main

import "github.com/jpillora/meads/pkg/meads"

type listCmd struct {
	JSON bool `help:"Output tasks as JSON"`
}

func (c *listCmd) Run() error {
	tasks, err := meads.Get(tasksFile, nil)
	if err != nil {
		return err
	}
	return printTasks(tasks, c.JSON)
}
