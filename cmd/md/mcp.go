package main

import (
	"context"

	mcppkg "github.com/jpillora/meads/pkg/mcp"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type mcpCmd struct {
	globals *globals
}

func (c *mcpCmd) Run() error {
	if err := c.globals.modeConflictErr(); err != nil {
		return err
	}
	if c.globals.mode() == modeGit {
		return errGitModeUnsupported("mcp")
	}
	s := mcppkg.NewServer(c.globals.store(), version)
	return s.Run(context.Background(), &mcp.StdioTransport{})
}
