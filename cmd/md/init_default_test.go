package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jpillora/meads/pkg/meads"
)

// Tests for `md init`'s backend default: git mode inside a repository, a
// TASKS.md outside one. Git mode is the better backend wherever it can run,
// and a repo is its only requirement - see initCmd.backend's doc comment.

// initDir runs `md init` (with whatever flags are set on cmd) against dir,
// with globals wired the way a real invocation in that directory would be.
func initDir(t *testing.T, dir string, apply func(*initCmd)) error {
	t.Helper()
	g := &globals{
		Git:       &meads.ExecGit{Dir: dir},
		Dir:       dir,
		TasksFile: filepath.Join(dir, "TASKS.md"),
	}
	cmd := &initCmd{globals: g}
	if apply != nil {
		apply(cmd)
	}
	return cmd.Run()
}

// gitInitDir makes dir a git repository, without any meads setup.
func gitInitDir(t *testing.T, dir string) {
	t.Helper()
	if err := (&meads.ExecGit{Dir: dir}).Run("init", "--quiet", "-b", "main", "."); err != nil {
		t.Fatal(err)
	}
}

func hasMeadsRefsIn(t *testing.T, dir string) bool {
	t.Helper()
	refs, err := meads.NewRefStore(&meads.ExecGit{Dir: dir}).ListRefs(meads.RefNamespace)
	if err != nil {
		t.Fatal(err)
	}
	return len(refs) > 0
}

func exists(dir, name string) bool {
	_, err := os.Stat(filepath.Join(dir, name))
	return err == nil
}

func TestInit_DefaultsToGitInsideARepo(t *testing.T) {
	dir := t.TempDir()
	gitInitDir(t, dir)

	if err := initDir(t, dir, nil); err != nil {
		t.Fatalf("init: %v", err)
	}
	if !hasMeadsRefsIn(t, dir) {
		t.Error("bare `md init` in a repository did not initialise git mode")
	}
	if exists(dir, "TASKS.md") {
		t.Error("bare `md init` in a repository created a tasks file as well")
	}
}

func TestInit_DefaultsToFileOutsideARepo(t *testing.T) {
	dir := t.TempDir() // deliberately never git init-ed

	if err := initDir(t, dir, nil); err != nil {
		t.Fatalf("init: %v", err)
	}
	if !exists(dir, "TASKS.md") {
		t.Error("bare `md init` outside a repository did not create TASKS.md")
	}
}

// The flags must beat the default in both directions, including the global
// --git/--file, which mean the same thing at the top level.
func TestInit_FlagsOverrideTheDefault(t *testing.T) {
	tests := []struct {
		name     string
		apply    func(*initCmd)
		wantGit  bool
		wantFile string
	}{
		{"--md in a repo", func(c *initCmd) { c.Markdown = true }, false, "TASKS.md"},
		{"--csv in a repo", func(c *initCmd) { c.CSV = true }, false, "TASKS.csv"},
		{"--git in a repo", func(c *initCmd) { c.Git = true }, true, ""},
		{"global --file in a repo", func(c *initCmd) { c.globals.FileMode = true }, false, "TASKS.md"},
		{"global --git in a repo", func(c *initCmd) { c.globals.GitMode = true }, true, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			gitInitDir(t, dir)

			if err := initDir(t, dir, tt.apply); err != nil {
				t.Fatalf("init: %v", err)
			}
			if got := hasMeadsRefsIn(t, dir); got != tt.wantGit {
				t.Errorf("git mode initialised = %v, want %v", got, tt.wantGit)
			}
			if tt.wantFile != "" && !exists(dir, tt.wantFile) {
				t.Errorf("%s was not created", tt.wantFile)
			}
		})
	}
}

// A contradictory pair must be refused rather than silently resolved by
// precedence - the user asked for two different things.
func TestInit_ConflictingFlagsError(t *testing.T) {
	tests := []struct {
		name  string
		apply func(*initCmd)
		want  string
	}{
		{"--git --md", func(c *initCmd) { c.Git, c.Markdown = true, true }, "--git and --md"},
		{"--git --csv", func(c *initCmd) { c.Git, c.CSV = true, true }, "--git and --csv"},
		{"global --git --file", func(c *initCmd) { c.globals.GitMode, c.globals.FileMode = true, true }, "--git and --file"},
		{"--md with global --git", func(c *initCmd) { c.Markdown, c.globals.GitMode = true, true }, "--git and --md"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			gitInitDir(t, dir)

			err := initDir(t, dir, tt.apply)
			if err == nil {
				t.Fatal("expected an error, got nil")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error = %q, want it to mention %q", err, tt.want)
			}
			// Nothing may have been created on the way to the refusal.
			if hasMeadsRefsIn(t, dir) || exists(dir, "TASKS.md") || exists(dir, "TASKS.csv") {
				t.Error("a refused init still created something")
			}
		})
	}
}

// TestInit_ExistingTasksFileWinsOverTheGitDefault: a repo with a TASKS.md has
// already chosen file mode. Quietly initialising git mode beside it would
// shadow the file - auto-detection prefers refs - and make its tasks vanish
// from `md list`. So the existing file wins, which surfaces InitTasks' own
// "already exists" refusal, plus directions.
func TestInit_ExistingTasksFileWinsOverTheGitDefault(t *testing.T) {
	for _, name := range []string{"TASKS.md", "TASKS.csv"} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			gitInitDir(t, dir)
			if err := os.WriteFile(filepath.Join(dir, name), []byte("# TASKS\n"), 0644); err != nil {
				t.Fatal(err)
			}

			err := initDir(t, dir, nil)
			if err == nil {
				t.Fatal("init over an existing tasks file should error, got nil")
			}
			if !strings.Contains(err.Error(), "already exists") {
				t.Errorf("error = %q, want it to say the file already exists", err)
			}
			if !strings.Contains(err.Error(), "--to-git") {
				t.Errorf("error = %q, want it to point at the migration route", err)
			}
			if hasMeadsRefsIn(t, dir) {
				t.Error("init created git-mode refs beside an existing tasks file, shadowing it")
			}
		})
	}
}

// The hint is only right when an existing file is the SOLE reason git mode was
// not chosen. Outside a repository there is no git mode to migrate to, so
// suggesting one would be nonsense.
func TestInit_ExistingTasksFileOutsideARepoHasNoHint(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "TASKS.md"), []byte("# TASKS\n"), 0644); err != nil {
		t.Fatal(err)
	}

	err := initDir(t, dir, nil)
	if err == nil {
		t.Fatal("init over an existing tasks file should error, got nil")
	}
	if strings.Contains(err.Error(), "--to-git") {
		t.Errorf("error = %q, should not suggest git mode outside a repository", err)
	}
}
