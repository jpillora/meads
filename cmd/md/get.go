package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/jpillora/meads/pkg/meads"
)

type getCmd struct {
	JSON bool     `help:"Output tasks as JSON"`
	IDs  []string `opts:"mode=arg,min=1" help:"Task IDs to retrieve"`
}

func (c *getCmd) Run() error {
	tasks, err := meads.Get(tasksFile, c.IDs)
	if err != nil {
		return err
	}
	return printTasks(tasks, c.JSON)
}

func printTasks(tasks []meads.Task, asJSON bool) error {
	if asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(tasks)
	}
	for _, t := range tasks {
		fmt.Printf("%s %s\n", t.ID, t.Title)
	}
	return nil
}
