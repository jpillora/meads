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

// store resolves the meads.Tasks to expose over MCP: meads.FileTasks in
// file mode, or meads.GitTasks in git mode. Every MCP tool
// (list/get/ready/add/update/delete/add_dependency/remove_dependency) maps
// cleanly onto the Tasks interface (see pkg/mcp/server.go), so - unlike
// webui, which also needs GitTasks.TaskRefOIDs for its change-poll watcher -
// plain meads.GitTasks is already everything git mode needs here; no tool
// stays gated.
func (c *mcpCmd) store() (meads.Tasks, error) {
	if c.globals.mode() != modeGit {
		return meads.NewFileTasks(c.globals.store(), c.globals.git()), nil
	}
	if !c.globals.inGitRepo() {
		return nil, fmt.Errorf("--git requires a git repository")
	}
	return meads.NewGitTasks(c.globals.gitStore()), nil
}
