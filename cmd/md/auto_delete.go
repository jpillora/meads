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

const hookMarker = "# md auto-delete hook"

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
	// Safety check 1: Must be on default branch
	if !c.isOnDefaultBranch() {
		return nil // Silently exit if not on default branch
	}

	// Safety check 2: Tasks file must have no uncommitted changes
	if !c.isTasksFileClean() {
		return nil // Silently exit if tasks file has changes
	}

	store := c.globals.store()
	git := c.globals.git()

	// Save backup for recovery — if anything fails after modifying
	// the file, we restore it so the tasks file always matches git HEAD.
	backup, err := util.ReadFile(store.FS(), store.Path())
	if err != nil {
		return fmt.Errorf("reading %s: %w", store.Path(), err)
	}

	// Get previous commit's file content for two-phase cleanup.
	// Phase 1 removes tasks that were already "deleted" in the previous commit.
	// Phase 2 marks "closed" tasks as "deleted" (preserving the closed state in this commit).
	prevContent, _ := git.Output("show", "HEAD~1:"+c.globals.TasksFile)

	result, err := store.AutoClean(prevContent)
	if err != nil {
		util.WriteFile(store.FS(), store.Path(), backup, 0644)
		return fmt.Errorf("auto-clean: %w", err)
	}
	if result == nil {
		return nil // Nothing to do
	}

	for _, id := range result.Removed {
		fmt.Fprintf(os.Stderr, "md: removed deleted task %d\n", id)
	}
	for _, id := range result.Marked {
		fmt.Fprintf(os.Stderr, "md: marked closed task %d as deleted\n", id)
	}

	// Stage the changes
	if err := git.Run("add", c.globals.TasksFile); err != nil {
		util.WriteFile(store.FS(), store.Path(), backup, 0644)
		return fmt.Errorf("staging %s: %w", store.Path(), err)
	}

	// Create a NEW commit (not amend) so the closed state is preserved in history.
	if err := git.Run("commit", "-m", "md: auto-clean tasks"); err != nil {
		// Restore file and unstage changes
		util.WriteFile(store.FS(), store.Path(), backup, 0644)
		git.Run("reset", "HEAD", "--", c.globals.TasksFile)
		return fmt.Errorf("committing: %w", err)
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

func (c *autoDeleteCmd) isTasksFileClean() bool {
	git := c.globals.git()

	// Check for unstaged changes
	if err := git.Run("diff", "--quiet", "HEAD", "--", c.globals.TasksFile); err != nil {
		return false // Has unstaged changes
	}

	// Check for staged changes
	if err := git.Run("diff", "--quiet", "--cached", "--", c.globals.TasksFile); err != nil {
		return false // Has staged changes
	}

	return true
}

func (c *autoDeleteCmd) enable() error {
	hookPath, err := c.getHookPath()
	if err != nil {
		return err
	}

	hookContent := c.generateHook()

	// Check if hook already exists with our marker
	if _, err := os.Stat(hookPath); err == nil {
		existingContent, err := os.ReadFile(hookPath)
		if err != nil {
			return fmt.Errorf("reading existing hook: %w", err)
		}
		if strings.Contains(string(existingContent), hookMarker) {
			fmt.Println("auto-delete is already enabled")
			return nil
		}

		// Prepend our hook to the existing content
		newContent := hookContent + string(existingContent)
		if err := os.WriteFile(hookPath, []byte(newContent), 0755); err != nil {
			return fmt.Errorf("writing hook: %w", err)
		}
	} else {
		// Create new hook
		if err := os.WriteFile(hookPath, []byte(hookContent), 0755); err != nil {
			return fmt.Errorf("creating hook: %w", err)
		}
	}

	fmt.Println("auto-delete enabled")
	return nil
}

func (c *autoDeleteCmd) disable() error {
	hookPath, err := c.getHookPath()
	if err != nil {
		return err
	}

	content, err := os.ReadFile(hookPath)
	if err != nil {
		if os.IsNotExist(err) {
			fmt.Println("auto-delete is not enabled")
			return nil
		}
		return fmt.Errorf("reading hook: %w", err)
	}

	hookStart := strings.Index(string(content), hookMarker)
	if hookStart == -1 {
		fmt.Println("auto-delete is not enabled")
		return nil
	}

	// Remove our hook content (from marker to end of our block)
	// Our hook ends with "fi\n\n" followed by the original content
	ourHook := c.generateHook()
	newContent := strings.Replace(string(content), ourHook, "", 1)

	// Remove trailing empty lines
	newContent = strings.TrimRight(newContent, "\n")

	if len(newContent) == 0 {
		os.Remove(hookPath)
	} else {
		os.WriteFile(hookPath, []byte(newContent+"\n"), 0755)
	}

	fmt.Println("auto-delete disabled")
	return nil
}

func (c *autoDeleteCmd) checkStatus() error {
	hookPath, err := c.getHookPath()
	if err != nil {
		return err
	}

	content, err := os.ReadFile(hookPath)
	if err != nil {
		if os.IsNotExist(err) {
			fmt.Println("auto-delete: disabled")
			return nil
		}
		return fmt.Errorf("reading hook: %w", err)
	}

	if strings.Contains(string(content), hookMarker) {
		fmt.Println("auto-delete: enabled")
	} else {
		fmt.Println("auto-delete: disabled")
	}
	return nil
}

func (c *autoDeleteCmd) getHookPath() (string, error) {
	// Find git root — uses gitCommand directly since this manages .git/hooks/
	out, err := c.globals.gitCommand("rev-parse", "--git-dir").Output()
	if err != nil {
		return "", fmt.Errorf("not in a git repository")
	}
	gitDir := strings.TrimSpace(string(out))
	return filepath.Join(gitDir, "hooks", "post-commit"), nil
}

func (c *autoDeleteCmd) generateHook() string {
	return fmt.Sprintf(`%s
# Automatically delete closed tasks when committing to default branch
if command -v md >/dev/null 2>&1; then
    GITHOOK=1 md auto-delete
fi

`, hookMarker)
}
