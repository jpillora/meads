package main

import "github.com/jpillora/opts"

const tasksFile = "TASKS.md"

var version = "0.0.0-dev"

type config struct {
	Add    addCmd    `opts:"mode=cmd" help:"Add a new task"`
	Get    getCmd    `opts:"mode=cmd" help:"Get tasks by ID"`
	List   listCmd   `opts:"mode=cmd" help:"List all tasks"`
	Del    delCmd    `opts:"mode=cmd" help:"Delete a task by ID"`
	Update updateCmd `opts:"mode=cmd" help:"Update a task by ID"`
	Ready  readyCmd  `opts:"mode=cmd" help:"List open tasks not blocked by dependencies"`
}

func main() {
	c := config{}
	opts.New(&c).
		Name("md").
		Version(version).
		Summary("Git-native task tracking in a single Markdown file").
		Repo("https://github.com/jpillora/meads").
		Parse().
		RunFatal()
}
