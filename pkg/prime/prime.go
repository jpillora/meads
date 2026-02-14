package prime

import _ "embed"

// AgentsCLI contains LLM context instructions for using md via CLI commands.
//
//go:embed AGENTS.md
var AgentsCLI string

// AgentsMCP contains LLM context instructions for using md via MCP tools.
//
//go:embed AGENTS_MCP.md
var AgentsMCP string
