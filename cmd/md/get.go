package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"

	"github.com/jpillora/meads/pkg/meads"
)

type getCmd struct {
	JSON  bool     `help:"Output tasks as JSON"`
	Short bool     `help:"Output short format (ID TITLE)"`
	IDs   []string `opts:"mode=arg,min=1" help:"Task IDs to retrieve"`
}

func (c *getCmd) Run() error {
	ids := make([]int, len(c.IDs))
	for i, s := range c.IDs {
		n, err := strconv.Atoi(s)
		if err != nil {
			return fmt.Errorf("invalid task ID: %s", s)
		}
		ids[i] = n
	}
	tasks, err := meads.Get(tasksFile, ids)
	if err != nil {
		return err
	}
	if c.Short {
		return printTasks(tasks, c.JSON)
	}
	if c.JSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(tasks)
	}
	for _, t := range tasks {
		fmt.Print(meads.FormatTask(t))
	}
	return nil
}

func printTasks(tasks []meads.Task, asJSON bool) error {
	if asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(tasks)
	}
	for _, t := range tasks {
		fmt.Printf("%d. %s\n", t.ID, t.Title)
	}
	return nil
}
