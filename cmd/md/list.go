package main

import (
	"fmt"

	"github.com/jpillora/meads/pkg/meads"
)

type listCmd struct {
	globals  *globals
	JSON     bool   `help:"Output tasks as JSON"`
	Md       bool   `opts:"name=md" help:"Output tasks as markdown"`
	History  bool   `opts:"short=-" help:"List all tasks from git history (including deleted)"`
	Status   string `help:"Filter by status (e.g. open, closed, inprogress)"`
	Priority string `help:"Filter by priority (e.g. P0, P1, P2)"`
	Type     string `help:"Filter by type (e.g. task, bug, feature)"`
	Tag      string `help:"Filter by tag"`
}

func (c *listCmd) Run() error {
	var tasks []meads.Task
	var err error
	if c.History {
		tasks, err = c.globals.store().GetHistory(c.globals.git())
	} else {
		tasks, err = c.globals.store().Get(nil)
	}
	if err != nil {
		return err
	}
	tasks = c.filterTasks(tasks)
	if c.Md {
		for _, t := range tasks {
			fmt.Print(meads.FormatTask(t))
		}
		return nil
	}
	return printTasks(tasks, c.JSON)
}

// filterTasks returns only tasks that match all specified filter flags.
func (c *listCmd) filterTasks(tasks []meads.Task) []meads.Task {
	if c.Status == "" && c.Priority == "" && c.Type == "" && c.Tag == "" {
		return tasks
	}
	var filtered []meads.Task
	for _, t := range tasks {
		if c.Status != "" && t.Status != c.Status {
			continue
		}
		if c.Priority != "" {
			tp := t.Priority
			if tp == "" {
				tp = "P2"
			}
			if tp != c.Priority {
				continue
			}
		}
		if c.Type != "" {
			tt := t.Type
			if tt == "" {
				tt = "task"
			}
			if tt != c.Type {
				continue
			}
		}
		if c.Tag != "" && !hasTag(t.Tags, c.Tag) {
			continue
		}
		filtered = append(filtered, t)
	}
	return filtered
}

// hasTag returns true if the given tag is present in the tags slice.
func hasTag(tags []string, tag string) bool {
	for _, t := range tags {
		if t == tag {
			return true
		}
	}
	return false
}
