package main

import (
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
)

var taskHeadingRe = regexp.MustCompile(`^## (\d{4}) `)

func cmdAdd(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: md add <title> [body]")
	}
	title := args[0]
	var body string
	if len(args) > 1 {
		body = strings.Join(args[1:], " ")
	}
	// Ensure TASKS.md exists.
	if _, err := os.Stat(tasksFile); os.IsNotExist(err) {
		if err := os.WriteFile(tasksFile, []byte(""), 0644); err != nil {
			return fmt.Errorf("creating %s: %w", tasksFile, err)
		}
	}
	_, content, err := acquireLock()
	if err != nil {
		return err
	}
	nextID := nextTaskID(content)
	entry := formatTask(nextID, title, body)
	var result string
	if strings.TrimSpace(content) == "" {
		result = entry
	} else {
		result = content + "\n" + entry
	}
	if err := releaseLock(result); err != nil {
		return fmt.Errorf("writing %s: %w", tasksFile, err)
	}
	fmt.Printf("added task %s\n", nextID)
	return nil
}

// nextTaskID scans content for the highest existing ## NNNN heading and returns NNNN+1 formatted as 4 digits.
func nextTaskID(content string) string {
	max := 0
	for _, line := range strings.Split(content, "\n") {
		m := taskHeadingRe.FindStringSubmatch(line)
		if m != nil {
			n, _ := strconv.Atoi(m[1])
			if n > max {
				max = n
			}
		}
	}
	return fmt.Sprintf("%04d", max+1)
}

// formatTask builds the markdown block for a new task.
func formatTask(id, title, body string) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "## %s %s\n", id, title)
	sb.WriteString("\n")
	sb.WriteString("* status: open\n")
	if body != "" {
		sb.WriteString("\n")
		sb.WriteString(body)
		sb.WriteString("\n")
	}
	return sb.String()
}
