package main

import (
	"context"

	mcppkg "github.com/jpillora/meads/pkg/mcp"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type mcpCmd struct{}

func (c *mcpCmd) Run() error {
	s := mcppkg.NewServer(tasksFile, version)
	return s.Run(context.Background(), &mcp.StdioTransport{})
}
