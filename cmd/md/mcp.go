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

// store resolves the meads.TaskStore to expose over MCP: *meads.Store in
// file mode (unchanged), or a git-mode adapter otherwise. Every MCP tool
// (list/get/ready/add/update/delete/add_dependency/remove_dependency) maps
// cleanly onto meads.TaskStore's five methods (see pkg/mcp/server.go), so -
// unlike webui, which also needs GitStore.TaskRefOIDs for its change-poll
// watcher - plain gitTaskStore (taskstore.go) is already everything git
// mode needs here; no tool stays gated.
func (c *mcpCmd) store() (meads.TaskStore, error) {
	if c.globals.mode() != modeGit {
		return c.globals.store(), nil
	}
	if !c.globals.inGitRepo() {
		return nil, fmt.Errorf("--git requires a git repository")
	}
	return gitTaskStore{gs: c.globals.gitStore()}, nil
}
