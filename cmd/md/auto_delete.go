package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/go-git/go-billy/v5/util"
)

type autoDeleteCmd struct {
	globals *globals
	Disable bool `opts:"mode=flag" help:"Disable auto-delete by removing the pre-commit hook"`
	Status  bool `opts:"mode=flag" help:"Check if auto-delete is enabled"`
}

// autoDeleteBlock prunes committed closed tasks and stages the result. It lives
// beside the auto-save block in the same pre-commit hook.
var autoDeleteBlock = hookBlock{
	marker:  "# md auto-delete hook",
	comment: "Automatically delete closed tasks when committing to default branch",
	command: "md auto-delete",
}

func (c *autoDeleteCmd) Run() error {
	// Check if we're running from the git hook
	if os.Getenv("GITHOOK") == "1" {
		return c.runFromHook()
	}

	// Normal mode: manage hook installation
	if c.Status {
		return c.checkStatus()
	}
	if c.Disable {
		return c.disable()
	}
	return c.enable()
}

func (c *autoDeleteCmd) runFromHook() error {
	// In git mode there is nothing to prune: soft-deleted tasks keep their
	// ref forever (GitStore.SoftDelete never removes a ref), and there is no
	// working-tree tasks file to rewrite. Checked first, before even the
	// default-branch check below, so a stray/leftover TASKS.md in a
	// git-mode repo (e.g. from before migrating - see `md convert --to-git`)
	// is never touched either.
	if c.globals.mode() == modeGit {
		return nil
	}

	// Safety check: Must be on default branch
	if !c.isOnDefaultBranch() {
		return nil
	}

	// Skip if tasks file doesn't exist
	if _, err := os.Stat(c.globals.TasksFile); os.IsNotExist(err) {
		return nil
	}

	// During a rebase/merge/cherry-pick, skip: staging races index.lock and
	// rewriting closed tasks while old commits are replayed is wrong anyway.
	if sequencerInProgress(c.globals) {
		return nil
	}

	store := c.globals.store()
	git := c.globals.git()

	// Save backup for recovery — if git add fails after modifying
	// the file, we restore it so the working tree is consistent.
	backup, err := util.ReadFile(store.FS(), store.Path())
	if err != nil {
		return fmt.Errorf("reading %s: %w", store.Path(), err)
	}

	result, err := store.AutoClean(git)
	if err != nil {
		util.WriteFile(store.FS(), store.Path(), backup, 0644)
		return fmt.Errorf("auto-clean: %w", err)
	}
	if result == nil {
		return nil // Nothing to do
	}

	for _, id := range result.Removed {
		fmt.Fprintf(os.Stderr, "md: removed closed task %d\n", id)
	}

	// Stage the changes to be included in the current commit
	if err := git.Run("add", c.globals.TasksFile); err != nil {
		util.WriteFile(store.FS(), store.Path(), backup, 0644)
		return fmt.Errorf("staging %s: %w", store.Path(), err)
	}

	return nil
}

func (c *autoDeleteCmd) isOnDefaultBranch() bool {
	git := c.globals.git()
	currentBranch, err := git.Output("rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return false
	}
	defaultBranch, err := git.Output("symbolic-ref", "refs/remotes/origin/HEAD")
	if err != nil {
		// Try to auto-detect from remote
		if err := git.Run("remote", "set-head", "origin", "--auto"); err != nil {
			// Network unavailable or no remote; check remote tracking branches
			for _, name := range []string{"main", "master", currentBranch} {
				if err := git.Run("rev-parse", "--verify", "refs/remotes/origin/"+name); err == nil {
					return currentBranch == name
				}
			}
			return false
		}
		defaultBranch, err = git.Output("symbolic-ref", "refs/remotes/origin/HEAD")
		if err != nil {
			return currentBranch == "main" || currentBranch == "master"
		}
	}
	defaultBranch = strings.TrimPrefix(defaultBranch, "refs/remotes/origin/")
	return currentBranch == defaultBranch
}

func (c *autoDeleteCmd) enable() error {
	// Migrate away from the old post-commit hook if present.
	c.cleanupOldPostCommitHook()

	installed, err := autoDeleteBlock.install(c.globals)
	if err != nil {
		return err
	}
	if installed {
		fmt.Println("auto-delete enabled")
	} else {
		fmt.Println("auto-delete is already enabled")
	}
	return nil
}

func (c *autoDeleteCmd) disable() error {
	removed, err := autoDeleteBlock.remove(c.globals)
	if err != nil {
		return err
	}
	if removed {
		fmt.Println("auto-delete disabled")
	} else {
		fmt.Println("auto-delete is not enabled")
	}
	return nil
}

func (c *autoDeleteCmd) checkStatus() error {
	on, err := autoDeleteBlock.installed(c.globals)
	if err != nil {
		return err
	}
	fmt.Printf("auto-delete: %s\n", enabledLabel(on))
	return nil
}

func (c *autoDeleteCmd) cleanupOldPostCommitHook() {
	out, err := c.globals.gitCommand("rev-parse", "--absolute-git-dir").Output()
	if err != nil {
		return
	}
	gitDir := strings.TrimSpace(string(out))
	oldHookPath := filepath.Join(gitDir, "hooks", "post-commit")

	content, err := os.ReadFile(oldHookPath)
	if err != nil {
		return
	}
	if !strings.Contains(string(content), autoDeleteBlock.marker) {
		return
	}

	// Remove our hook content from the post-commit hook
	newContent := normalizeHook(strings.Replace(string(content), autoDeleteBlock.body(), "", 1))
	if newContent == "" {
		os.Remove(oldHookPath)
	} else {
		os.WriteFile(oldHookPath, []byte(newContent+"\n"), 0755)
	}
}
