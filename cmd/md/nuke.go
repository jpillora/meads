package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type nukeCmd struct {
	globals *globals
	Force   bool `opts:"mode=flag" help:"Skip confirmation prompt"`
}

func (c *nukeCmd) Run() error {
	// Find the git root directory
	out, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return fmt.Errorf("not in a git repository")
	}
	repoRoot := strings.TrimSpace(string(out))

	if !c.Force {
		fmt.Print("This will completely remove beads from this repository. Continue? [y/N] ")
		var answer string
		fmt.Scanln(&answer)
		if answer != "y" && answer != "Y" {
			fmt.Println("aborted")
			return nil
		}
	}

	var errors []string

	// 1. Remove .beads/ directory
	beadsDir := filepath.Join(repoRoot, ".beads")
	if _, err := os.Stat(beadsDir); err == nil {
		if err := os.RemoveAll(beadsDir); err != nil {
			errors = append(errors, fmt.Sprintf("removing .beads/: %v", err))
		} else {
			fmt.Println("removed .beads/")
		}
	}

	// 2. Remove beads git hooks
	gitDir, err := gitDir()
	if err == nil {
		hooksDir := filepath.Join(gitDir, "hooks")
		for _, hook := range []string{"pre-commit", "post-merge", "pre-push", "post-checkout"} {
			c.removeBeadsHook(hooksDir, hook, &errors)
			// Remove beads backup files
			backup := filepath.Join(hooksDir, hook+".backup")
			if _, err := os.Stat(backup); err == nil {
				if err := os.Remove(backup); err != nil {
					errors = append(errors, fmt.Sprintf("removing %s.backup: %v", hook, err))
				} else {
					fmt.Printf("removed %s.backup\n", hook)
				}
			}
		}
		// Remove beads-worktrees directory
		worktreesDir := filepath.Join(gitDir, "beads-worktrees")
		if _, err := os.Stat(worktreesDir); err == nil {
			if err := os.RemoveAll(worktreesDir); err != nil {
				errors = append(errors, fmt.Sprintf("removing beads-worktrees: %v", err))
			} else {
				fmt.Println("removed .git/beads-worktrees/")
			}
		}
	}

	// 3. Remove beads merge driver from local git config
	exec.Command("git", "config", "--local", "--remove-section", "merge.beads").Run()
	fmt.Println("removed merge.beads git config")

	// 4. Clean up .gitattributes
	c.cleanFile(filepath.Join(repoRoot, ".gitattributes"), func(line string) bool {
		lower := strings.ToLower(line)
		return strings.Contains(line, "merge=beads") || strings.Contains(line, ".beads/") ||
			(strings.HasPrefix(strings.TrimSpace(line), "#") && strings.Contains(lower, "beads"))
	}, &errors)

	// 5. Clean up .gitignore
	c.cleanFile(filepath.Join(repoRoot, ".gitignore"), func(line string) bool {
		trimmed := strings.TrimSpace(line)
		return trimmed == ".beads/" || trimmed == ".beads"
	}, &errors)

	// 6. Remove "bd prime" hooks from Claude Code settings
	claudeSettingsPaths := []string{
		filepath.Join(repoRoot, ".claude", "settings.json"),
		filepath.Join(repoRoot, ".claude", "settings.local.json"),
	}
	if home, err := os.UserHomeDir(); err == nil {
		claudeSettingsPaths = append(claudeSettingsPaths,
			filepath.Join(home, ".claude", "settings.json"),
		)
	}
	for _, p := range claudeSettingsPaths {
		c.removeBeadsClaudeHooks(p, &errors)
	}

	// 7. Remove beads marketplace plugin
	if home, err := os.UserHomeDir(); err == nil {
		c.removeBeadsMarketplace(home, &errors)
	}

	if len(errors) > 0 {
		return fmt.Errorf("completed with errors:\n  %s", strings.Join(errors, "\n  "))
	}

	fmt.Println("beads has been completely removed")
	return nil
}

// removeBeadsHook removes a beads-installed hook file. If the hook contains
// only beads content, the file is deleted. Otherwise only the beads portion
// is removed.
func (c *nukeCmd) removeBeadsHook(hooksDir, name string, errors *[]string) {
	hookPath := filepath.Join(hooksDir, name)
	content, err := os.ReadFile(hookPath)
	if err != nil {
		if os.IsNotExist(err) {
			return
		}
		*errors = append(*errors, fmt.Sprintf("reading hook %s: %v", name, err))
		return
	}

	text := string(content)

	// Check if this is a beads hook (contains "bd" or "beads" references)
	if !isBeadsHook(text) {
		return
	}

	// If the entire hook is beads-related, just remove it
	if isEntirelyBeadsHook(text) {
		if err := os.Remove(hookPath); err != nil {
			*errors = append(*errors, fmt.Sprintf("removing hook %s: %v", name, err))
		} else {
			fmt.Printf("removed %s hook\n", name)
		}
		return
	}

	// Otherwise, remove just the beads portion
	cleaned := removeBeadsSection(text)
	if err := os.WriteFile(hookPath, []byte(cleaned), 0755); err != nil {
		*errors = append(*errors, fmt.Sprintf("updating hook %s: %v", name, err))
	} else {
		fmt.Printf("cleaned beads content from %s hook\n", name)
	}
}

// isBeadsHook checks if a hook file contains beads-related content.
func isBeadsHook(content string) bool {
	lower := strings.ToLower(content)
	return strings.Contains(lower, "beads") || strings.Contains(lower, " bd ")
}

// isEntirelyBeadsHook checks if the hook file only contains beads-related content
// (shebang + comments + beads commands).
func isEntirelyBeadsHook(content string) bool {
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if strings.HasPrefix(trimmed, "#") {
			continue
		}
		if strings.HasPrefix(trimmed, "#!/") {
			continue
		}
		// Check if the line is beads-related
		lower := strings.ToLower(trimmed)
		if strings.Contains(lower, "beads") || strings.Contains(lower, " bd ") ||
			strings.HasPrefix(trimmed, "if ") || strings.HasPrefix(trimmed, "fi") ||
			strings.HasPrefix(trimmed, "then") || strings.HasPrefix(trimmed, "else") ||
			strings.HasPrefix(trimmed, "exit") || strings.HasPrefix(trimmed, "BEADS_DIR") ||
			strings.HasPrefix(trimmed, "MAIN_REPO") {
			continue
		}
		// Found a non-beads, non-comment line
		return false
	}
	return true
}

// removeBeadsSection removes beads-related blocks from a hook script.
func removeBeadsSection(content string) string {
	lines := strings.Split(content, "\n")
	var result []string
	inBeadsBlock := false

	for _, line := range lines {
		lower := strings.ToLower(line)
		// Detect start of a beads block (comment marker)
		if strings.Contains(lower, "beads") && strings.HasPrefix(strings.TrimSpace(line), "#") {
			inBeadsBlock = true
			continue
		}
		if inBeadsBlock {
			trimmed := strings.TrimSpace(line)
			// End of block: empty line after an exit or fi
			if trimmed == "" {
				inBeadsBlock = false
				continue
			}
			continue
		}
		result = append(result, line)
	}

	return strings.Join(result, "\n")
}

// cleanFile removes lines matching the predicate from a file. If the file
// becomes empty (only whitespace), it is deleted.
func (c *nukeCmd) cleanFile(path string, shouldRemove func(string) bool, errors *[]string) {
	content, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return
		}
		*errors = append(*errors, fmt.Sprintf("reading %s: %v", filepath.Base(path), err))
		return
	}

	lines := strings.Split(string(content), "\n")
	var kept []string
	removed := 0
	for _, line := range lines {
		if shouldRemove(line) {
			removed++
			continue
		}
		kept = append(kept, line)
	}

	if removed == 0 {
		return
	}

	name := filepath.Base(path)

	// Check if file is now effectively empty
	joined := strings.TrimSpace(strings.Join(kept, "\n"))
	if joined == "" {
		if err := os.Remove(path); err != nil {
			*errors = append(*errors, fmt.Sprintf("removing %s: %v", name, err))
		} else {
			fmt.Printf("removed %s (now empty)\n", name)
		}
		return
	}

	// Write back cleaned content with trailing newline
	if err := os.WriteFile(path, []byte(joined+"\n"), 0644); err != nil {
		*errors = append(*errors, fmt.Sprintf("writing %s: %v", name, err))
	} else {
		fmt.Printf("cleaned %s\n", name)
	}
}

// removeBeadsClaudeHooks removes hooks containing "bd prime" from a Claude
// Code settings.json file. It precisely removes only individual hook handlers
// whose command contains "bd prime", cleaning up empty matcher groups and
// empty event arrays as it goes.
func (c *nukeCmd) removeBeadsClaudeHooks(path string, errors *[]string) {
	content, err := os.ReadFile(path)
	if err != nil {
		return // file doesn't exist, skip silently
	}

	// Parse as generic JSON to preserve all other fields
	var settings map[string]json.RawMessage
	if err := json.Unmarshal(content, &settings); err != nil {
		return
	}

	hooksRaw, ok := settings["hooks"]
	if !ok {
		return
	}

	// Parse hooks: map of event name -> array of matcher groups
	var hooks map[string]json.RawMessage
	if err := json.Unmarshal(hooksRaw, &hooks); err != nil {
		return
	}

	changed := false
	for eventName, matcherGroupsRaw := range hooks {
		var matcherGroups []json.RawMessage
		if err := json.Unmarshal(matcherGroupsRaw, &matcherGroups); err != nil {
			continue
		}

		var keptGroups []json.RawMessage
		for _, groupRaw := range matcherGroups {
			var group map[string]json.RawMessage
			if err := json.Unmarshal(groupRaw, &group); err != nil {
				keptGroups = append(keptGroups, groupRaw)
				continue
			}

			handlersRaw, ok := group["hooks"]
			if !ok {
				keptGroups = append(keptGroups, groupRaw)
				continue
			}

			var handlers []map[string]interface{}
			if err := json.Unmarshal(handlersRaw, &handlers); err != nil {
				keptGroups = append(keptGroups, groupRaw)
				continue
			}

			var keptHandlers []map[string]interface{}
			for _, h := range handlers {
				cmd, _ := h["command"].(string)
				if isBeadsPrimeCommand(cmd) {
					changed = true
					continue
				}
				keptHandlers = append(keptHandlers, h)
			}

			if len(keptHandlers) == 0 {
				// All handlers removed, drop the entire matcher group
				continue
			}

			// Re-serialize the group with filtered handlers
			newHandlers, _ := json.Marshal(keptHandlers)
			group["hooks"] = newHandlers
			newGroup, _ := json.Marshal(group)
			keptGroups = append(keptGroups, newGroup)
		}

		if len(keptGroups) == 0 {
			delete(hooks, eventName)
		} else {
			newGroups, _ := json.Marshal(keptGroups)
			hooks[eventName] = newGroups
		}
	}

	if !changed {
		return
	}

	// Serialize hooks back
	newHooks, _ := json.Marshal(hooks)
	settings["hooks"] = newHooks

	// Write back with indentation to match typical settings format
	out, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		*errors = append(*errors, fmt.Sprintf("serializing %s: %v", filepath.Base(path), err))
		return
	}

	if err := os.WriteFile(path, append(out, '\n'), 0600); err != nil {
		*errors = append(*errors, fmt.Sprintf("writing %s: %v", filepath.Base(path), err))
		return
	}

	rel, _ := filepath.Rel(".", path)
	if rel == "" || strings.HasPrefix(rel, "..") {
		rel = path
	}
	fmt.Printf("removed bd prime hooks from %s\n", rel)
}

// isBeadsPrimeCommand checks if a command string is a "bd prime" invocation.
func isBeadsPrimeCommand(cmd string) bool {
	cmd = strings.TrimSpace(cmd)
	return cmd == "bd prime" || strings.HasPrefix(cmd, "bd prime ")
}

// removeBeadsMarketplace removes the beads marketplace plugin from Claude Code.
func (c *nukeCmd) removeBeadsMarketplace(home string, errors *[]string) {
	pluginsDir := filepath.Join(home, ".claude", "plugins")

	// Remove the marketplace directory
	mktDir := filepath.Join(pluginsDir, "marketplaces", "beads-marketplace")
	if _, err := os.Stat(mktDir); err == nil {
		if err := os.RemoveAll(mktDir); err != nil {
			*errors = append(*errors, fmt.Sprintf("removing beads marketplace: %v", err))
		} else {
			fmt.Println("removed beads marketplace plugin")
		}
	}

	// Remove from known_marketplaces.json
	knownPath := filepath.Join(pluginsDir, "known_marketplaces.json")
	content, err := os.ReadFile(knownPath)
	if err != nil {
		return
	}

	var known map[string]json.RawMessage
	if err := json.Unmarshal(content, &known); err != nil {
		return
	}

	if _, ok := known["beads-marketplace"]; !ok {
		return
	}

	delete(known, "beads-marketplace")

	out, err := json.MarshalIndent(known, "", "  ")
	if err != nil {
		*errors = append(*errors, fmt.Sprintf("serializing known_marketplaces.json: %v", err))
		return
	}

	if err := os.WriteFile(knownPath, append(out, '\n'), 0644); err != nil {
		*errors = append(*errors, fmt.Sprintf("writing known_marketplaces.json: %v", err))
		return
	}

	fmt.Println("removed beads from known_marketplaces.json")
}

func gitDir() (string, error) {
	out, err := exec.Command("git", "rev-parse", "--git-dir").Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}
