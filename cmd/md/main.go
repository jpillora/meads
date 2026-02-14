package main

import (
	"os"

	"github.com/jpillora/opts"
)

var version = "0.0.0-dev"

var tasksFile = getTasksFile()

func getTasksFile() string {
	if v := os.Getenv("MD_TASKS"); v != "" {
		return v
	}
	return "TASKS.md"
}

type config struct {
	Add        addCmd        `opts:"mode=cmd" help:"Add a new task"`
	Get        getCmd        `opts:"mode=cmd" help:"Get tasks by ID"`
	List       listCmd       `opts:"mode=cmd" help:"List all tasks"`
	Del        delCmd        `opts:"mode=cmd" help:"Delete a task by ID"`
	Update     updateCmd     `opts:"mode=cmd" help:"Update a task by ID"`
	SetStatus  setStatusCmd  `opts:"mode=cmd,name=set-status" help:"Set a task's status"`
	AddDep     addDepCmd     `opts:"mode=cmd,name=add-dep" help:"Add a dependency to a task"`
	Ready      readyCmd      `opts:"mode=cmd" help:"List open tasks not blocked by dependencies"`
	Import     importCmd     `opts:"mode=cmd" help:"Import tasks from an external source"`
	Prime      primeCmd      `opts:"mode=cmd" help:"Print LLM context for using md"`
	Mcp        mcpCmd        `opts:"mode=cmd" help:"Start MCP server over stdio"`
	AutoDelete autoDeleteCmd `opts:"mode=cmd,name=auto-delete" help:"Auto-delete closed tasks via git hook"`
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
