package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/jpillora/meads/pkg/meads"
)

type initCmd struct {
	globals *globals
	CSV     bool `help:"Create a TASKS.csv file instead of TASKS.md"`
	Git     bool `help:"Initialize git mode (refs/meads/*) in the current repo instead of creating a tasks file"`
}

func (c *initCmd) Run() error {
	if c.Git {
		if c.CSV {
			return fmt.Errorf("cannot use both --git and --csv")
		}
		return c.runGit()
	}
	file := "TASKS.md"
	content := ""
	if c.CSV {
		file = "TASKS.csv"
		content = meads.InitCSV()
	}
	if _, err := os.Stat(file); err == nil {
		return fmt.Errorf("%s already exists", file)
	}
	if err := os.WriteFile(file, []byte(content), 0644); err != nil {
		return fmt.Errorf("creating %s: %w", file, err)
	}
	fmt.Printf("created %s\n", file)
	return nil
}

// meadsFetchRefspec is the fetch refspec `md init --git` adds to origin so a
// plain `git fetch`/`git clone` picks up refs/meads/* - without it these
// refs are pushed and visible via `git ls-remote` but a default clone never
// downloads them (verified against GitHub and Gitea; see task 57's design
// doc). It is purely additive (git config --add, never a plain set) and
// deliberately says nothing about pushing: see ensureFetchRefspec.
const meadsFetchRefspec = "+refs/meads/*:refs/meads/*"

// runGit initializes git mode in the current repo: it errors clearly if not
// inside a git repo or if git mode is already initialized, otherwise writes
// a default config ref and (best-effort) configures the fetch refspec.
//
// It deliberately seeds nothing else - in particular no placeholder task.
// "No refs/meads/tasks/* refs" already means exactly "no tasks" (see
// globals.gitTaskRefsExist), so an empty task set needs no marker of its
// own. The config ref is the one thing worth writing up front: without it, a
// freshly-initialized repo with zero tasks would be indistinguishable from
// one that was never initialized at all (both have an empty refs/meads/*
// namespace), so a second `md init --git` would succeed again instead of
// erroring - which is exactly the clobber this command must refuse. Writing
// GitStore's own DefaultConfig() also means GitStore.Config() returns real
// (if default) values immediately rather than synthesizing them from an
// absent ref on every read.
func (c *initCmd) runGit() error {
	g := c.globals
	if !g.inGitRepo() {
		return fmt.Errorf("not in a git repository")
	}

	rs := meads.NewRefStore(g.git())
	existing, err := rs.ListRefs(meads.RefNamespace)
	if err != nil {
		return fmt.Errorf("checking for existing git-mode refs: %w", err)
	}
	if len(existing) > 0 {
		return fmt.Errorf("git mode is already initialized (%s already has refs)", meads.RefNamespace)
	}

	if err := g.gitStore().SetConfig(meads.DefaultConfig()); err != nil {
		return fmt.Errorf("writing default config: %w", err)
	}
	fmt.Printf("initialized git mode (%s*)\n", meads.RefNamespace)

	return ensureFetchRefspec(g)
}

// ensureFetchRefspec adds meadsFetchRefspec to origin's fetch refspecs. It is
// purely additive (git config --add), never replacing origin's existing
// fetch line(s), and idempotent: it first checks the configured refspecs and
// skips if meadsFetchRefspec is already among them, so re-running init-like
// setup never adds a duplicate. If there is no origin remote, it prints a
// message and returns nil rather than failing.
//
// It deliberately never touches remote.origin.push. Configuring ANY push
// refspec on a remote replaces git's default matching/simple push behaviour
// and would break ordinary `git push` for the user - see
// TestIntegration_InitGit_DoesNotBreakNormalPush. Phase 6 (async auto-push,
// task 63) passes an explicit refspec at push time instead.
func ensureFetchRefspec(g *globals) error {
	if err := g.git().Run("remote", "get-url", "origin"); err != nil {
		fmt.Println("no 'origin' remote configured — skipping fetch refspec setup")
		return nil
	}

	out, _ := g.git().Output("config", "--get-all", "remote.origin.fetch")
	for _, line := range strings.Split(out, "\n") {
		if strings.TrimSpace(line) == meadsFetchRefspec {
			fmt.Println("fetch refspec already configured on origin")
			return nil
		}
	}

	if err := g.git().Run("config", "--add", "remote.origin.fetch", meadsFetchRefspec); err != nil {
		return fmt.Errorf("setting fetch refspec: %w", err)
	}
	fmt.Printf("added fetch refspec %s to origin\n", meadsFetchRefspec)
	return nil
}
