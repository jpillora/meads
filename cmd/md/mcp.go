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
	s := mcppkg.NewServer(c.globals.store(), version)
	return s.Run(context.Background(), &mcp.StdioTransport{})
}
