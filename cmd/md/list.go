package main

import (
	"fmt"
	"os"

	"github.com/jpillora/meads/pkg/meads"
)

type listCmd struct {
	globals  *globals
	JSON     bool   `help:"Output tasks as JSON"`
	Md       bool   `opts:"name=md" help:"Output tasks as markdown"`
	History  bool   `opts:"short=-" help:"List all tasks from git history (including deleted)"`
	Limit    int    `opts:"short=n" help:"Limit number of results (0 means no limit)"`
	Offset   int    `help:"Skip this many results before applying the limit"`
	Status   string `help:"Filter by status (e.g. open, closed, inprogress, blocked)"`
	Priority string `help:"Filter by priority (e.g. P0, P1, P2)"`
	Type     string `help:"Filter by type (e.g. task, bug, feature, idea)"`
	Tag      string `help:"Filter by tag — comma-separated to require all of them (e.g. api,backend)"`
}

func (c *listCmd) Run() error {
	ts, err := c.globals.tasks()
	if err != nil {
		return err
	}
	var tasks []meads.Task
	if c.History {
		tasks, err = ts.GetHistory()
	} else {
		tasks, err = ts.Get(nil)
	}
	if err != nil {
		return err
	}
	warnCycles(c.globals)
	tasks = c.filterTasks(tasks)
	tasks, err = paginateTasks(tasks, c.Limit, c.Offset)
	if err != nil {
		return err
	}
	if len(tasks) == 0 && !c.JSON && !c.Md {
		fmt.Fprintln(os.Stderr, "no tasks found (run 'md add' to create one)")
		return nil
	}
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
	tasks = filterByTag(tasks, c.Tag)
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
		filtered = append(filtered, t)
	}
	return filtered
}

func paginateTasks(tasks []meads.Task, limit, offset int) ([]meads.Task, error) {
	if limit < 0 {
		return nil, fmt.Errorf("--limit must be non-negative")
	}
	if offset < 0 {
		return nil, fmt.Errorf("--offset must be non-negative")
	}
	if offset >= len(tasks) {
		return tasks[len(tasks):], nil
	}
	tasks = tasks[offset:]
	if limit > 0 && limit < len(tasks) {
		tasks = tasks[:limit]
	}
	return tasks, nil
}

// filterByTag returns only the tasks carrying every tag in the
// comma-separated spec; an empty spec returns tasks unchanged. Shared with
// `md ready`, which filters on tags and nothing else.
func filterByTag(tasks []meads.Task, spec string) []meads.Task {
	want := meads.ParseTags(spec)
	if len(want) == 0 {
		return tasks
	}
	var filtered []meads.Task
	for _, t := range tasks {
		if t.Tags.HasAll(want) {
			filtered = append(filtered, t)
		}
	}
	return filtered
}
