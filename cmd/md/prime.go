package main

import (
	_ "embed"
	"fmt"
)

//go:embed AGENTS.md
var agentsMD string

//go:embed AGENTS_MCP.md
var agentsMCPMD string

type primeCmd struct {
	MCP bool `help:"Print MCP-oriented context (assumes MCP server is enabled)"`
}

func (c *primeCmd) Run() error {
	if c.MCP {
		fmt.Print(agentsMCPMD)
	} else {
		fmt.Print(agentsMD)
	}
	return nil
}
