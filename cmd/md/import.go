package main

import (
	"fmt"

	"github.com/jpillora/meads/pkg/meads"
)

// errGitModeUnsupported reports that cmd's file-backend implementation has
// no GitStore equivalent yet (e.g. Store.RunImport, beads-import's only
// source of tasks), so running it against a git-mode repo would silently
// operate on an unrelated or nonexistent TasksFile rather than the active
// refs/meads/* store. Commands not wired to the meads.Tasks seam call this
// instead of guessing. mcp and webui used to be gated through here too
// (task 65 phase 8); phase 9 wired both to GitStore directly (see mcp.go's
// and webui.go's own store methods), so this is now down to beads-import
// alone.
func errGitModeUnsupported(cmd string) error {
	return fmt.Errorf("%s: not supported in git mode yet", cmd)
}

type beadsImportCmd struct {
	globals *globals
}

func (c *beadsImportCmd) Run() error {
	if err := c.globals.modeConflictErr(); err != nil {
		return err
	}
	if c.globals.mode() == modeGit {
		return errGitModeUnsupported("beads-import")
	}
	imp, err := meads.GetImporter("beads")
	if err != nil {
		return err
	}
	result, err := c.globals.store().RunImport(imp)
	if err != nil {
		return err
	}
	fmt.Printf("imported %d, skipped %d\n", result.Imported, result.Skipped)
	return nil
}
