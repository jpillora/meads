package main

import (
	"fmt"

	"github.com/jpillora/meads/pkg/prime"
)

type primeCmd struct {
	MCP bool `help:"Print MCP-oriented context (assumes MCP server is enabled)"`
}

func (c *primeCmd) Run() error {
	if c.MCP {
		fmt.Print(prime.AgentsMCP)
	} else {
		fmt.Print(prime.AgentsCLI)
	}
	return nil
}
