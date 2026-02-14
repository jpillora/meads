package main

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/jpillora/meads/pkg/meads"
)

type addCmd struct {
	Args      []string `opts:"mode=arg,min=0" help:"Input text (e.g. 'bug: Fix login P3')"`
	Title     string   `help:"Set task title"`
	Status    string   `help:"Set task status"`
	Priority  string   `help:"Set task priority (1-9)"`
	Type      string   `help:"Set task type (bug, task, feature)"`
	DependsOn string   `opts:"name=depends-on" help:"Set dependency task ID"`
	Body      string   `help:"Set task body"`
}

var (
	typeRe     = regexp.MustCompile(`^(bug|task|feature):`)
	priorityRe = regexp.MustCompile(`\bP(\d)\b`)
)

func (c *addCmd) Run() error {
	title := c.Title
	status := c.Status
	priority := c.Priority
	typ := c.Type
	dependsOn := c.DependsOn
	body := c.Body
	// Parse args if provided
	if len(c.Args) > 0 {
		input := strings.Join(c.Args, " ")
		// (1) Extract type prefix
		var parsedType string
		if m := typeRe.FindStringSubmatch(input); m != nil {
			parsedType = m[1]
			input = strings.TrimLeft(input[len(m[0]):], " ")
		}
		// (2) Extract priority
		var parsedPriority string
		if m := priorityRe.FindStringSubmatch(input); m != nil {
			parsedPriority = m[1]
			input = strings.TrimSpace(priorityRe.ReplaceAllString(input, ""))
		}
		// (3) Extract title (everything before first period)
		var parsedTitle string
		if idx := strings.Index(input, "."); idx >= 0 {
			parsedTitle = strings.TrimSpace(input[:idx])
		} else {
			parsedTitle = strings.TrimSpace(input)
		}
		// Check for conflicts between flags and parsed values
		if typ != "" && parsedType != "" {
			return fmt.Errorf("type set by both flag and argument")
		}
		if priority != "" && parsedPriority != "" {
			return fmt.Errorf("priority set by both flag and argument")
		}
		if title != "" && parsedTitle != "" {
			return fmt.Errorf("title set by both flag and argument")
		}
		// Apply parsed values
		if parsedType != "" {
			typ = parsedType
		}
		if parsedPriority != "" {
			priority = parsedPriority
		}
		if parsedTitle != "" {
			title = parsedTitle
		}
	}
	if title == "" {
		return fmt.Errorf("title is required")
	}
	// Default status
	if status == "" {
		status = "open"
	}
	t := meads.Task{}
	t.Title = title
	t.SetStatus(status)
	if priority != "" {
		n, err := strconv.Atoi(priority)
		if err != nil {
			return fmt.Errorf("invalid priority: %s", priority)
		}
		t.SetPriority(n)
	}
	if dependsOn != "" {
		t.DependsOn = dependsOn
		t.SetMeta("depends-on", dependsOn)
	}
	if typ != "" {
		t.SetMeta("type", typ)
	}
	if body != "" {
		t.Body = body
	}
	id, err := meads.Add(tasksFile, t)
	if err != nil {
		return err
	}
	fmt.Printf("added task %s\n", id)
	return nil
}
