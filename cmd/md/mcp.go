package main

import (
	"context"
	"fmt"

	mcppkg "github.com/jpillora/meads/pkg/mcp"
	"github.com/jpillora/meads/pkg/meads"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type mcpCmd struct {
	globals *globals
}

func (c *mcpCmd) Run() error {
	if err := c.globals.modeConflictErr(); err != nil {
		return err
	}
	store, err := c.store()
	if err != nil {
		return err
	}
	s := mcppkg.NewServer(store, version)
	return s.Run(context.Background(), &mcp.StdioTransport{})
}

// store resolves the meads.Tasks to expose over MCP, mirroring
// globals.tasks()' three-way split: the forced branches (--file / explicit
// tasks file, then --git) construct directly, and the auto-detect branch
// delegates to meads.OpenTasksGit (which runs the one-shot clone
// resolution - see pkg/meads/clone.go). Every MCP tool
// (list/get/ready/add/update/delete/add_dependency/remove_dependency) maps
// cleanly onto the Tasks interface (see pkg/mcp/server.go), so - unlike
// webui, which also needs GitTasks.TaskRefOIDs for its change-poll watcher -
// plain meads.GitTasks is already everything git mode needs here; no tool
// stays gated.
func (c *mcpCmd) store() (meads.Tasks, error) {
	g := c.globals
	if g.FileMode || g.explicitTasksFile() {
		return meads.NewFileTasks(g.store(), g.git()), nil
	}
	if g.GitMode {
		if !g.inGitRepo() {
			return nil, fmt.Errorf("--git requires a git repository")
		}
		return meads.NewGitTasks(g.gitStore()), nil
	}
	return meads.OpenTasksGit(g.Dir, g.git())
}
