package main

import (
	"fmt"
	"strings"

	"github.com/jpillora/meads/pkg/meads"
)

type addCmd struct {
	Title string   `opts:"mode=arg" help:"Task title"`
	Body  []string `opts:"mode=arg" help:"Task description"`
}

func (c *addCmd) Run() error {
	t := meads.Task{
		Title:  c.Title,
		Status: "open",
		Meta:   map[string]string{"status": "open"},
		Body:   strings.Join(c.Body, " "),
	}
	id, err := meads.Add(tasksFile, t)
	if err != nil {
		return err
	}
	fmt.Printf("added task %s\n", id)
	return nil
}
