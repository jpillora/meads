package main

import (
	"fmt"
	"os"
	"sort"
)

func cmdReady() error {
	data, err := os.ReadFile(tasksFile)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("reading %s: %w", tasksFile, err)
	}
	content := stripLockLines(string(data))
	tasks := ParseTasks(content)
	// Build a map of task status by ID.
	statusByID := make(map[string]string, len(tasks))
	for _, t := range tasks {
		statusByID[t.ID] = t.Status
	}
	// Filter to open tasks not blocked by an unclosed dependency.
	var ready []Task
	for _, t := range tasks {
		if t.Status != "open" {
			continue
		}
		if t.DependsOn != "" {
			depStatus, exists := statusByID[t.DependsOn]
			if exists && depStatus != "closed" {
				continue
			}
		}
		ready = append(ready, t)
	}
	// Sort by priority descending.
	sort.Slice(ready, func(i, j int) bool {
		return ready[i].Priority > ready[j].Priority
	})
	for _, t := range ready {
		fmt.Printf("%s %s\n", t.ID, t.Title)
	}
	return nil
}
