package main

import (
	_ "embed"
	"fmt"
)

//go:embed AGENTS.md
var agentsMD string

type primeCmd struct{}

func (c *primeCmd) Run() error {
	fmt.Print(agentsMD)
	return nil
}
