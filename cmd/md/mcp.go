package main

import (
	"context"

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
	store, err := c.globals.tasks()
	if err != nil {
		return err
	}
	if store.Backend() == meads.BackendGit {
		if err := c.globals.gitStore().CheckGitRefProtocol(); err != nil {
			return err
		}
	}
	s := mcppkg.NewServer(store, version)
	return s.Run(context.Background(), &mcp.StdioTransport{})
}
