package main

import (
	"fmt"
)

type readyCmd struct {
	globals *globals
	Limit   int `opts:"short=n" help:"Limit number of results"`
}

func (c *readyCmd) Run() error {
	tasks, err := c.globals.store().Ready()
	if err != nil {
		return err
	}
	if c.Limit > 0 && len(tasks) > c.Limit {
		tasks = tasks[:c.Limit]
	}
	for _, t := range tasks {
		fmt.Printf("%d. %s\n", t.ID, t.Title)
	}
	return nil
}
