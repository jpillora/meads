package main

import "github.com/jpillora/meads/pkg/tui"

type tuiCmd struct {
	globals *globals
}

func (c *tuiCmd) Run() error {
	store, err := c.globals.tasks()
	if err != nil {
		return err
	}
	return tui.Run(store)
}
