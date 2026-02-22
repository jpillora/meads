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
	if err := c.globals.store().Delete(id); err != nil {
		return err
	}
	postWebhook(c.globals, "delete", map[string]int{"id": id})
	fmt.Printf("deleted task %d\n", id)
	return nil
}
