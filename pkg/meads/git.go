package meads

import (
	"os/exec"
	"strings"
)

// Git abstracts git CLI operations for testability.
type Git interface {
	// Run executes a git command and returns an error if it fails.
	Run(args ...string) error
	// Output executes a git command and returns its stdout.
	Output(args ...string) (string, error)
}

// ExecGit implements Git by shelling out to the git CLI.
type ExecGit struct {
	Dir string
}

func (g *ExecGit) Run(args ...string) error {
	cmd := exec.Command("git", args...)
	if g.Dir != "" {
		cmd.Dir = g.Dir
	}
	return cmd.Run()
}

func (g *ExecGit) Output(args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	if g.Dir != "" {
		cmd.Dir = g.Dir
	}
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}
