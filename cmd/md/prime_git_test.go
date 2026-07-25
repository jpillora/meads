package main

import (
	"strings"
	"testing"

	"github.com/jpillora/meads/pkg/prime"
)

// Tests for task 69's `md prime` git-mode awareness: prime must describe
// whichever mode is actually active (globals.mode(), not the fast
// help-visibility heuristic - see prime.go's content() doc comment) rather
// than unconditionally assuming file mode.

// gitOnlyPhrase is a phrase only the git-mode content should ever contain.
//
// fileModeRulePhrase is the file-mode Rules' exact instruction to treat a
// tasks file as real and off-limits to edit directly. Its presence is what a
// file-mode agent needs and a git-mode agent must NOT see (checking for the
// bare substring "TASKS.md" instead would be wrong: git-mode content
// legitimately mentions "TASKS.md" when explaining that no such file exists
// here - see AGENTS_CLI_GIT.md's Overview - so the absence of that literal
// filename is not itself a meaningful assertion).
//
// gitModeRulePhrase is the git-mode Rules' equivalent positive statement.
var (
	gitOnlyPhrase      = "refs/meads/tasks"
	fileModeRulePhrase = "Do NOT read or edit the task file"
	gitModeRulePhrase  = "There is no task file"
)

func TestPrimeCmd_GitMode_CLI(t *testing.T) {
	h := gitModeHarness(t)
	out := capturePrimeStdout(t, func() error { return (&primeCmd{globals: h.globals}).Run() })

	if strings.Contains(out, fileModeRulePhrase) {
		t.Errorf("git-mode CLI prime output must not carry over the file-mode rule verbatim (%q):\n%s", fileModeRulePhrase, out)
	}
	if !strings.Contains(out, gitModeRulePhrase) {
		t.Errorf("git-mode CLI prime output should state %q:\n%s", gitModeRulePhrase, out)
	}
	if !strings.Contains(out, gitOnlyPhrase) {
		t.Errorf("git-mode CLI prime output should mention %q:\n%s", gitOnlyPhrase, out)
	}
	if !strings.Contains(out, "git mode") {
		t.Errorf("git-mode CLI prime output should say it is in git mode:\n%s", out)
	}
	// Must call out which commands don't apply, so an agent doesn't try
	// them blind and get confused by a silent no-op or a bare error.
	for _, cmd := range []string{"auto-save", "auto-delete", "beads-import"} {
		if !strings.Contains(out, cmd) {
			t.Errorf("git-mode CLI prime output should mention %q is unavailable/no-op:\n%s", cmd, out)
		}
	}
	// Baseline content (commands, workflows) must still be present - this
	// isn't a wholesale replacement, just a different Overview/Rules.
	if !strings.Contains(out, "md ready") || !strings.Contains(out, "md add") {
		t.Errorf("git-mode CLI prime output dropped baseline command examples:\n%s", out)
	}
}

func TestPrimeCmd_GitMode_MCP(t *testing.T) {
	h := gitModeHarness(t)
	out := capturePrimeStdout(t, func() error { return (&primeCmd{globals: h.globals, MCP: true}).Run() })

	if strings.Contains(out, fileModeRulePhrase) {
		t.Errorf("git-mode MCP prime output must not carry over the file-mode rule verbatim (%q):\n%s", fileModeRulePhrase, out)
	}
	if !strings.Contains(out, gitModeRulePhrase) {
		t.Errorf("git-mode MCP prime output should state %q:\n%s", gitModeRulePhrase, out)
	}
	if !strings.Contains(out, gitOnlyPhrase) {
		t.Errorf("git-mode MCP prime output should mention %q:\n%s", gitOnlyPhrase, out)
	}
	if !strings.Contains(out, "MCP Server") {
		t.Errorf("git-mode MCP prime output missing the MCP section:\n%s", out)
	}
	// The MCP tool surface is unchanged between modes (see mcp.go: no tool
	// stays gated), so the tool list itself must still be present.
	for _, tool := range []string{"ready_tasks", "add_task", "update_task", "delete_task", "add_dependency"} {
		if !strings.Contains(out, tool) {
			t.Errorf("git-mode MCP prime output dropped tool %q:\n%s", tool, out)
		}
	}
}

func TestPrimeCmd_FileMode_StillFileContent(t *testing.T) {
	// A real file-mode harness (not just nil globals) must resolve to the
	// unchanged file-mode content, proving content() isn't just "nil -> file,
	// anything else -> git".
	h := newHarness(t)
	out := capturePrimeStdout(t, func() error { return (&primeCmd{globals: h.globals}).Run() })
	if !strings.Contains(out, fileModeRulePhrase) {
		t.Errorf("file-mode prime output should still state %q:\n%s", fileModeRulePhrase, out)
	}
	if strings.Contains(out, "git mode") {
		t.Errorf("file-mode prime output should not claim to be in git mode:\n%s", out)
	}
	if out != prime.AgentsCLI {
		t.Error("file-mode prime output (with a real globals wired) should be byte-identical to prime.AgentsCLI")
	}
}

func TestPrimeCmd_NilGlobals_DefaultsToFileMode(t *testing.T) {
	// Existing tests (prime_test.go) construct &primeCmd{} with no globals
	// at all - this must keep behaving exactly as before.
	out := capturePrimeStdout(t, func() error { return (&primeCmd{}).Run() })
	if out != prime.AgentsCLI {
		t.Error("a primeCmd with nil globals should print prime.AgentsCLI unchanged")
	}
}

func TestPrimeCmd_GitMode_FlagConflictErrors(t *testing.T) {
	h := gitModeHarness(t)
	h.globals.FileMode = true // GitMode is already true from gitModeHarness
	err := (&primeCmd{globals: h.globals}).Run()
	if err == nil {
		t.Fatal("prime with both --git and --file should error, got nil")
	}
}

// --write also needs to route through the same mode-aware content in git
// mode, not just plain stdout.
func TestPrimeCmd_GitMode_Write(t *testing.T) {
	h := gitModeHarness(t)
	path := t.TempDir() + "/AGENTS.md"
	if err := (&primeCmd{globals: h.globals, Write: path}).Run(); err != nil {
		t.Fatalf("prime --write in git mode: %v", err)
	}
	got := readPrimeFile(t, path)
	if strings.Contains(got, fileModeRulePhrase) {
		t.Errorf("--write in git mode wrote file-mode content:\n%s", got)
	}
	if !strings.Contains(got, gitOnlyPhrase) {
		t.Errorf("--write in git mode should have written git-mode content:\n%s", got)
	}
}

// Sanity: the embedded git-mode strings are non-empty and distinct from
// their file-mode counterparts (a copy/paste that embedded the wrong file
// would otherwise pass every content-based test above by accident, if both
// files happened to be identical).
func TestPrimeGitContent_DistinctFromFileContent(t *testing.T) {
	if prime.AgentsCLIGit == "" || prime.AgentsMCPGit == "" {
		t.Fatal("git-mode prime content must not be empty")
	}
	if prime.AgentsCLIGit == prime.AgentsCLI {
		t.Error("AgentsCLIGit must differ from AgentsCLI")
	}
	if prime.AgentsMCPGit == prime.AgentsMCP {
		t.Error("AgentsMCPGit must differ from AgentsMCP")
	}
}
