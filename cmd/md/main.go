package main

import (
	"fmt"
	"os"
	"os/exec"

	"github.com/jpillora/meads/pkg/meads"
	"github.com/jpillora/opts"
)

var version = "0.0.0-dev"

func defaultTasksFile() string {
	if v := os.Getenv("MEADS_TASK_FILE"); v != "" {
		return v
	}
	if v := os.Getenv("MD_TASKS"); v != "" {
		return v
	}
	if _, err := os.Stat("TASKS.csv"); err == nil {
		return "TASKS.csv"
	}
	return "TASKS.md"
}

func defaultWebhookURI() string {
	return os.Getenv("MEADS_WEBHOOK_URI")
}

type globals struct {
	Store      *meads.Store `opts:"-"`
	Git        meads.Git    `opts:"-"`
	TasksFile  string       `help:"the tasks markdown file to manage (env MEADS_TASK_FILE)"`
	WebhookURI string       `help:"a uri to POST to with {meads:true,action,data}; http(s):// or unix:///path/to/sock or unix://[/path/to/sock]/http/path (env MEADS_WEBHOOK_URI)"`
	Dir        string       `opts:"-"`
}

// gitCommand creates an exec.Command for git with Dir set.
// Used by hook management (enable/disable/checkStatus) which needs exec.Cmd directly.
func (g *globals) gitCommand(args ...string) *exec.Cmd {
	cmd := exec.Command("git", args...)
	if g.Dir != "" {
		cmd.Dir = g.Dir
	}
	return cmd
}

// warnCycles prints a stderr warning for each circular dependency in the tasks
// file. A cycle silently deadlocks the tasks it spans — they can never become
// ready — so read commands surface it rather than letting it go unnoticed. It
// writes to stderr to keep stdout (including --json) clean, and stays quiet on
// read errors, which the calling command reports through its own path.
func warnCycles(g *globals) {
	cycles, err := g.store().FindCycles()
	if err != nil {
		return
	}
	for _, cycle := range cycles {
		fmt.Fprintf(os.Stderr, "warning: circular dependency: %s (run 'md doctor')\n", meads.FormatCycle(cycle))
	}
}

// store returns the Store, lazily initializing from TasksFile if not set.
func (g *globals) store() *meads.Store {
	if g.Store == nil {
		g.Store = meads.NewFileStore(g.TasksFile)
	}
	return g.Store
}

// git returns the Git implementation, lazily initializing if not set.
func (g *globals) git() meads.Git {
	if g.Git == nil {
		g.Git = &meads.ExecGit{Dir: g.Dir}
	}
	return g.Git
}

type root struct {
	Globals     globals        `opts:"mode=embedded"`
	Add         addCmd         `opts:"mode=cmd,group=Basic" help:"Add a new task"`
	Create      createCmd      `opts:"mode=cmd,group=Basic" help:"Create a new task (alias for add)"`
	Get         getCmd         `opts:"mode=cmd,group=Basic" help:"Get tasks by ID"`
	List        listCmd        `opts:"mode=cmd,group=Basic" help:"List all tasks"`
	Del         delCmd         `opts:"mode=cmd,group=Basic" help:"Delete a task by ID"`
	Update      updateCmd      `opts:"mode=cmd,group=Basic" help:"Update a task by ID"`
	SetStatus   setStatusCmd   `opts:"mode=cmd,name=set-status,group=Basic" help:"Set a task's status"`
	AddDep      addDepCmd      `opts:"mode=cmd,name=add-dep,group=Basic" help:"Add a dependency to a task"`
	Ready       readyCmd       `opts:"mode=cmd,group=Basic" help:"List open tasks not blocked by dependencies"`
	Init        initCmd        `opts:"mode=cmd,group=Misc" help:"Initialize a new tasks file"`
	Convert     convertCmd     `opts:"mode=cmd,group=Misc" help:"Convert between TASKS.md and TASKS.csv formats"`
	Prime       primeCmd       `opts:"mode=cmd,group=Misc" help:"Print LLM context for using md"`
	Mcp         mcpCmd         `opts:"mode=cmd,group=Misc" help:"Start MCP server over stdio"`
	Webui       webuiCmd       `opts:"mode=cmd,group=Misc" help:"Launch web UI for this TASKS file"`
	Doctor      doctorCmd      `opts:"mode=cmd,group=Misc" help:"Detect and fix duplicate task IDs"`
	AutoDelete  autoDeleteCmd  `opts:"mode=cmd,name=auto-delete,group=Misc" help:"Auto-delete closed tasks via git hook"`
	AutoSave    autoSaveCmd    `opts:"mode=cmd,name=auto-save,group=Misc" help:"Auto-stage the tasks file in every commit via git hook"`
	BeadsImport beadsImportCmd `opts:"mode=cmd,name=beads-import,group=Beads" help:"Import tasks from beads"`
	BeadsNuke   nukeCmd        `opts:"mode=cmd,name=beads-nuke,group=Beads" help:"Completely remove beads from the current repository"`
}

func main() {
	c := root{}
	c.Globals.TasksFile = defaultTasksFile()
	c.Globals.WebhookURI = defaultWebhookURI()
	g := &c.Globals
	c.Add.globals = g
	c.Create.globals = g

	c.Get.globals = g
	c.List.globals = g
	c.Del.globals = g
	c.Update.globals = g
	c.SetStatus.globals = g
	c.AddDep.globals = g
	c.Ready.globals = g
	c.Init.globals = g
	c.Convert.globals = g
	c.BeadsImport.globals = g
	c.Mcp.globals = g
	c.Webui.globals = g
	c.Doctor.globals = g
	c.AutoDelete.globals = g
	c.AutoSave.globals = g
	c.BeadsNuke.globals = g

	p := opts.New(&c).
		Name("md").
		Version(version).
		Summary("Git-native task tracking in a single Markdown file").
		Repo("https://github.com/jpillora/meads").
		Parse()
	if !p.IsRunnable() {
		fmt.Println(p.Help())
		return
	}
	p.RunFatal()
}
