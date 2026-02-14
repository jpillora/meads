package main

import (
	"fmt"
	"strings"
)

type delCmd struct {
	ID string `opts:"mode=arg" help:"Task ID to delete"`
}

func (c *delCmd) Run() error {
	_, content, err := acquireLock()
	if err != nil {
		return err
	}
	result, err := deleteTask(content, c.ID)
	if err != nil {
		// Release lock with original content on failure.
		releaseLock(content)
		return err
	}
	if err := releaseLock(result); err != nil {
		return fmt.Errorf("writing %s: %w", tasksFile, err)
	}
	fmt.Printf("deleted task %s\n", c.ID)
	return nil
}

// deleteTask removes the section for the given task ID from the markdown content.
// Task sections start with "## <id> " and end before the next "## " heading or EOF.
func deleteTask(content, id string) (string, error) {
	lines := strings.Split(content, "\n")
	prefix := "## " + id + " "
	start := -1
	end := len(lines)
	for i, line := range lines {
		if start == -1 {
			if strings.HasPrefix(line, prefix) {
				start = i
			}
		} else {
			if strings.HasPrefix(line, "## ") {
				end = i
				break
			}
		}
	}
	if start == -1 {
		return "", fmt.Errorf("task %s not found", id)
	}
	// Consume blank lines before the heading.
	for start > 0 && strings.TrimSpace(lines[start-1]) == "" {
		start--
	}
	// Consume trailing blank lines after the section body.
	for end < len(lines) && strings.TrimSpace(lines[end]) == "" {
		end++
	}
	// Remove lines [start, end).
	out := make([]string, 0, len(lines)-(end-start))
	out = append(out, lines[:start]...)
	// If there's content both before and after, add a blank separator.
	if start > 0 && end < len(lines) {
		out = append(out, "")
	}
	out = append(out, lines[end:]...)
	return strings.Join(out, "\n"), nil
}
