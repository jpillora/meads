package main

import (
	"fmt"

	"github.com/jpillora/meads/pkg/prime"
)

type primeCmd struct {
	globals *globals
	MCP     bool   `help:"Print MCP-oriented context (assumes MCP server is enabled)"`
	Write   string `opts:"name=write" help:"Write the context into FILE (e.g. CLAUDE.md or AGENTS.md), replacing the existing md-prime block in place or appending it if absent"`
}

func (c *primeCmd) Run() error {
	if c.globals != nil {
		if err := c.globals.modeConflictErr(); err != nil {
			return err
		}
	}
	content := c.content()
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

// content picks the CLI or MCP context for whichever storage mode is
// actually active, so prime always describes what is really there rather
// than unconditionally assuming file mode: in git mode there is no tasks
// file to read or edit, tasks are refs, and a few file-mode-only commands
// (auto-save/auto-delete/beads-import) don't apply - see
// pkg/prime/AGENTS_CLI_GIT.md and AGENTS_MCP_GIT.md.
//
// This deliberately uses globals.mode() - the authoritative, subprocess-
// backed detector every other command uses to pick its backend - not the
// fast filesystem-only heuristic help visibility uses (see
// cmd/md/help_visibility.go): prime is a deliberate, one-off invocation, not
// something run on every `md --help`, so correctness is worth the one git
// subprocess call.
//
// A nil globals (only reachable in a test constructing primeCmd directly,
// e.g. &primeCmd{}) is treated as file mode, matching this command's
// longstanding default and keeping every existing caller of that form
// working unchanged - main() always wires a real globals for the actual CLI.
func (c *primeCmd) content() string {
	git := c.globals != nil && c.globals.mode() == modeGit
	switch {
	case c.MCP && git:
		return prime.AgentsMCPGit
	case c.MCP:
		return prime.AgentsMCP
	case git:
		return prime.AgentsCLIGit
	default:
		return prime.AgentsCLI
	}
}
