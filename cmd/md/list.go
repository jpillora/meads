package main

import (
	"fmt"

	"github.com/jpillora/meads/pkg/meads"
)

type listCmd struct {
	globals *globals
	JSON    bool `help:"Output tasks as JSON"`
	Md      bool `opts:"name=md" help:"Output tasks as markdown"`
}

func (c *listCmd) Run() error {
	tasks, err := meads.Get(c.globals.TasksFile, nil)
	if err != nil {
		return err
	}
	if c.Md {
		for _, t := range tasks {
			fmt.Print(meads.FormatTask(t))
		}
		return nil
	}
	return printTasks(tasks, c.JSON)
}
