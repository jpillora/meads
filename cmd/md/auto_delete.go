package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/jpillora/meads/pkg/meads"
)

type autoDeleteCmd struct {
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

	// Safety check 2: TASKS.md must have no changes at all
	if !c.isTasksFileClean() {
		return nil // Silently exit if TASKS.md has changes
	}

	// Find and delete all closed tasks
	tasks, err := meads.Get(tasksFile, nil)
	if err != nil {
		return fmt.Errorf("reading tasks: %w", err)
	}

	var closedIDs []int
	for _, t := range tasks {
		if t.Status == "closed" {
			closedIDs = append(closedIDs, t.ID)
		}
	}

	if len(closedIDs) == 0 {
		return nil // Nothing to delete
	}

	// Delete each closed task
	for _, id := range closedIDs {
		if err := meads.Delete(tasksFile, id); err != nil {
			return fmt.Errorf("deleting task %d: %w", id, err)
		}
		fmt.Fprintf(os.Stderr, "md: auto-deleted closed task %d\n", id)
	}

	// Amend the commit to include deletions
	cmd := exec.Command("git", "add", tasksFile)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("staging TASKS.md: %w", err)
	}

	cmd = exec.Command("git", "commit", "--amend", "--no-edit")
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("amending commit: %w", err)
	}

	return nil
}

func (c *autoDeleteCmd) isOnDefaultBranch() bool {
	// Get current branch
	out, err := exec.Command("git", "rev-parse", "--abbrev-ref", "HEAD").Output()
	if err != nil {
		return false
	}
	currentBranch := strings.TrimSpace(string(out))

	// Get default branch
	out, err = exec.Command("git", "symbolic-ref", "refs/remotes/origin/HEAD").Output()
	if err != nil {
		// Fallback: check if current branch is main or master
		return currentBranch == "main" || currentBranch == "master"
	}
	defaultBranch := strings.TrimSpace(string(out))
	defaultBranch = strings.TrimPrefix(defaultBranch, "refs/remotes/origin/")

	return currentBranch == defaultBranch
}

func (c *autoDeleteCmd) isTasksFileClean() bool {
	// Check for unstaged changes
	cmd := exec.Command("git", "diff", "--quiet", "HEAD", "--", tasksFile)
	if err := cmd.Run(); err != nil {
		return false // Has unstaged changes
	}

	// Check for staged changes
	cmd = exec.Command("git", "diff", "--quiet", "--cached", "--", tasksFile)
	if err := cmd.Run(); err != nil {
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
	// Find git root
	out, err := exec.Command("git", "rev-parse", "--git-dir").Output()
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
