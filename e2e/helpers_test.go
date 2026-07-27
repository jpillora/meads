package e2e

import (
	"fmt"
	"strings"
	"testing"

	"github.com/go-git/go-billy/v5/memfs"
	"github.com/jpillora/meads/pkg/meads"
)

func newMDStore(t *testing.T) *meads.Store {
	t.Helper()
	fs := memfs.New()
	return meads.NewStore(fs, "TASKS.md")
}

func newCSVStore(t *testing.T) *meads.Store {
	t.Helper()
	fs := memfs.New()
	return meads.NewStore(fs, "TASKS.csv")
}

// fakeGit implements meads.Git for testing GetHistory.
type fakeGit struct {
	commits map[string]string // "hash:file" -> content
	log     []string          // ordered commit hashes (newest first)
}

func (g *fakeGit) Run(args ...string) error { return nil }

func (g *fakeGit) Output(args ...string) (string, error) {
	if len(args) >= 4 && args[0] == "log" {
		if len(g.log) == 0 {
			return "", nil
		}
		return strings.Join(g.log, "\n"), nil
	}
	if len(args) == 2 && args[0] == "show" && strings.Contains(args[1], ":") {
		content, ok := g.commits[args[1]]
		if !ok {
			return "", fmt.Errorf("not found: %s", args[1])
		}
		return content, nil
	}
	return "", fmt.Errorf("fakeGit: unsupported command: %v", args)
}

func (g *fakeGit) OutputWithInput(stdin string, args ...string) (string, error) {
	return g.Output(args...)
}

func (g *fakeGit) OutputRaw(args ...string) ([]byte, error) {
	out, err := g.Output(args...)
	return []byte(out), err
}

func (g *fakeGit) OutputRawWithInput(stdin string, args ...string) ([]byte, error) {
	return g.OutputRaw(args...)
}

// fakeGitError always returns errors.
type fakeGitError struct{}

func (g *fakeGitError) Run(args ...string) error              { return fmt.Errorf("git error") }
func (g *fakeGitError) Output(args ...string) (string, error) { return "", fmt.Errorf("git error") }
func (g *fakeGitError) OutputWithInput(stdin string, args ...string) (string, error) {
	return "", fmt.Errorf("git error")
}
func (g *fakeGitError) OutputRaw(args ...string) ([]byte, error) { return nil, fmt.Errorf("git error") }
func (g *fakeGitError) OutputRawWithInput(stdin string, args ...string) ([]byte, error) {
	return nil, fmt.Errorf("git error")
}
