package main

import (
	"fmt"

	"github.com/jpillora/meads/pkg/prime"
)

type primeCmd struct {
	MCP   bool   `help:"Print MCP-oriented context (assumes MCP server is enabled)"`
	Write string `opts:"name=write" help:"Write the context into FILE (e.g. CLAUDE.md or AGENTS.md), replacing the existing md-prime block in place or appending it if absent"`
}

func (c *primeCmd) Run() error {
	content := prime.AgentsCLI
	if c.MCP {
		content = prime.AgentsMCP
	}
	if c.Write == "" {
		fmt.Print(content)
		return nil
	}
	action, err := prime.WriteFile(c.Write, content)
	if err != nil {
		return err
	}
	fmt.Printf("prime context %s: %s\n", action, c.Write)
	return nil
}
