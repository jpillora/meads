package main

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/jpillora/meads/pkg/meads"
)

type addCmd struct {
	globals     *globals
	Args        []string `opts:"mode=arg,min=0" help:"Input text (e.g. 'bug: Fix login P3')"`
	Title       string   `help:"Set task title"`
	Status      string   `help:"Set task status (draft, open, inprogress, closed)"`
	Priority    string   `help:"Set task priority (P0-P9 or 0-9)"`
	Type        string   `help:"Set task type (bug, task, feature)"`
	DependsOn   string   `opts:"name=depends-on" help:"Set dependency task ID"`
	Description string   `help:"Set task description"`
	Draft       bool     `help:"Create task with draft status"`
}

type createCmd struct {
	globals     *globals
	Args        []string `opts:"mode=arg,min=0" help:"Input text (e.g. 'bug: Fix login P3')"`
	Title       string   `help:"Set task title"`
	Status      string   `help:"Set task status (draft, open, inprogress, closed)"`
	Priority    string   `help:"Set task priority (P0-P9 or 0-9)"`
	Type        string   `help:"Set task type (bug, task, feature)"`
	DependsOn   string   `opts:"name=depends-on" help:"Set dependency task ID"`
	Description string   `help:"Set task description"`
	Draft       bool     `help:"Create task with draft status"`
}

var (
	typeRe     = regexp.MustCompile(`^(bug|task|feature):`)
	priorityRe = regexp.MustCompile(`\bP(\d)\b`)
)

func (c *addCmd) Run() error {
	return runAdd(c.globals, c.Args, c.Title, c.Status, c.Priority, c.Type, c.DependsOn, c.Description, c.Draft)
}

func (c *createCmd) Run() error {
	return runAdd(c.globals, c.Args, c.Title, c.Status, c.Priority, c.Type, c.DependsOn, c.Description, c.Draft)
}

func runAdd(g *globals, args []string, title, status, priority, typ, dependsOn, description string, draft bool) error {
	// Parse args if provided
	if len(args) > 0 {
		input := strings.Join(args, " ")
		// (1) Extract type prefix
		var parsedType string
		if m := typeRe.FindStringSubmatch(input); m != nil {
			parsedType = m[1]
			input = strings.TrimLeft(input[len(m[0]):], " ")
		}
		// (2) Extract priority
		var parsedPriority string
		if m := priorityRe.FindStringSubmatch(input); m != nil {
			parsedPriority = m[0] // full "P\d" match
			input = strings.TrimSpace(priorityRe.ReplaceAllString(input, ""))
		}
		// (3) Extract title (everything before first period), remainder is description
		var parsedTitle, parsedDescription string
		if idx := strings.Index(input, "."); idx >= 0 {
			parsedTitle = strings.TrimSpace(input[:idx])
			parsedDescription = strings.TrimSpace(input[idx+1:])
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
		if description != "" && parsedDescription != "" {
			return fmt.Errorf("description set by both flag and argument")
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
		if parsedDescription != "" {
			description = parsedDescription
		}
	}
	if title == "" {
		return fmt.Errorf("title is required")
	}
	// Default status
	if draft {
		if status != "" {
			return fmt.Errorf("cannot use --draft with --status")
		}
		status = "draft"
	}
	if status == "" {
		status = "open"
	}
	t := meads.Task{}
	t.Title = title
	t.SetStatus(status)
	if priority != "" {
		p, err := meads.NormalizePriority(priority)
		if err != nil {
			return err
		}
		t.SetPriority(p)
	}
	if dependsOn != "" {
		n, err := strconv.Atoi(dependsOn)
		if err != nil {
			return fmt.Errorf("invalid depends-on: %s", dependsOn)
		}
		t.SetDependsOn([]int{n})
	}
	if typ != "" {
		t.SetType(typ)
	}
	if description != "" {
		t.Description = description
	}
	id, err := g.store().Add(t)
	if err != nil {
		return err
	}
	t.ID = id
	postWebhook(g, "add", t)
	fmt.Printf("added task %d\n", id)
	return nil
}
