package prime

import _ "embed"

// AgentsCLI contains LLM context instructions for using md via CLI commands
// in file mode (tasks live in TASKS.md/TASKS.csv).
//
//go:embed AGENTS_CLI.md
var AgentsCLI string

// AgentsMCP contains LLM context instructions for using md via MCP tools in
// file mode (tasks live in TASKS.md/TASKS.csv).
//
//go:embed AGENTS_MCP.md
var AgentsMCP string

// AgentsCLIGit is AgentsCLI's git-mode counterpart: tasks live as git refs
// (refs/meads/tasks/<id>), there is no tasks file, and a few file-mode-only
// commands (auto-save/auto-delete/beads-import) don't apply. See
// cmd/md/prime.go, which picks between the four Agents* strings based on
// the repo's actual active mode (globals.mode) and the --mcp flag.
//
//go:embed AGENTS_CLI_GIT.md
var AgentsCLIGit string

// AgentsMCPGit is AgentsMCP's git-mode counterpart - see AgentsCLIGit.
//
//go:embed AGENTS_MCP_GIT.md
var AgentsMCPGit string
