package main

import (
	"os"

	"github.com/jpillora/opts"
)

var version = "0.0.0-dev"

func defaultTasksFile() string {
	if v := os.Getenv("MD_TASKS"); v != "" {
		return v
	}
	return "TASKS.md"
}

type globals struct {
	TasksFile  string `help:"the tasks markdown file to manage"`
	WebhookURL string `help:"a url to POST to with {meads:true,action,data}"`
}

type root struct {
	Globals    globals       `opts:"mode=embedded"`
	Add        addCmd        `opts:"mode=cmd" help:"Add a new task"`
	Get        getCmd        `opts:"mode=cmd" help:"Get tasks by ID"`
	List       listCmd       `opts:"mode=cmd" help:"List all tasks"`
	Del        delCmd        `opts:"mode=cmd" help:"Delete a task by ID"`
	Update     updateCmd     `opts:"mode=cmd" help:"Update a task by ID"`
	SetStatus  setStatusCmd  `opts:"mode=cmd,name=set-status" help:"Set a task's status"`
	AddDep     addDepCmd     `opts:"mode=cmd,name=add-dep" help:"Add a dependency to a task"`
	Ready      readyCmd      `opts:"mode=cmd" help:"List open tasks not blocked by dependencies"`
	BeadsImport beadsImportCmd `opts:"mode=cmd,name=beads-import" help:"Import tasks from beads"`
	Prime      primeCmd      `opts:"mode=cmd" help:"Print LLM context for using md"`
	Mcp        mcpCmd        `opts:"mode=cmd" help:"Start MCP server over stdio"`
	AutoDelete autoDeleteCmd `opts:"mode=cmd,name=auto-delete" help:"Auto-delete closed tasks via git hook"`
	BeadsNuke  nukeCmd      `opts:"mode=cmd,name=beads-nuke" help:"Completely remove beads from the current repository"`
}

func main() {
	c := root{}
	c.Globals.TasksFile = defaultTasksFile()
	g := &c.Globals
	c.Add.globals = g
	c.Get.globals = g
	c.List.globals = g
	c.Del.globals = g
	c.Update.globals = g
	c.SetStatus.globals = g
	c.AddDep.globals = g
	c.Ready.globals = g
	c.BeadsImport.globals = g
	c.Mcp.globals = g
	c.AutoDelete.globals = g
	c.BeadsNuke.globals = g

	opts.New(&c).
		Name("md").
		Version(version).
		Summary("Git-native task tracking in a single Markdown file").
		Repo("https://github.com/jpillora/meads").
		Parse().
		RunFatal()
}
