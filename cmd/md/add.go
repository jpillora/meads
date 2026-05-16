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
	Args        []string `opts:"mode=arg,min=0" help:"Note: task descriptions are JSON-encoded markdown — '\\n' decodes to a real newline (in both modes below).\n\nTwo modes for setting task fields:\n\n(1) Free-form string. Pass a single argument and meads extracts:\n      type prefix:   bug: / task: / feature: / idea:   (optional)\n      priority:      P0-P9 anywhere in the string      (default P2)\n      title:         text before the first '.' or newline\n      description:   text after the first '.' or newline\n    Examples:\n      md add 'Fix the login bug'\n      md add 'bug: Fix login P1. Session cookie expires after 5min'\n      md add 'Fix login.\\nSession cookie expires.\\n\\nSteps to repro...'\n\n(2) Explicit flags. Set each field via --title, --type, --priority, etc.\n    Example:\n      md add --type=bug --priority=P1 --title='Fix login' \\\n             --description='## Steps\\n1. Repro\\n2. Patch'"`
	Title       string   `help:"Set task title"`
	Status      string   `help:"Set task status (draft, open, inprogress, closed)"`
	Priority    string   `help:"Set task priority (P0-P9 or 0-9)"`
	Type        string   `help:"Set task type (bug, task, feature, idea)"`
	DependsOn   string   `opts:"name=depends-on" help:"Set dependency task ID"`
	Description string   `help:"Set task description (JSON-encoded markdown)"`
	Draft       bool     `help:"Create task with draft status"`
}

type createCmd struct {
	addCmd
}

var (
	typeRe     = regexp.MustCompile(`^(bug|task|feature|idea):`)
	priorityRe = regexp.MustCompile(`\bP(\d)\b`)
)

func (c *addCmd) Run() error {
	return runAdd(c.globals, c.Args, c.Title, c.Status, c.Priority, c.Type, c.DependsOn, c.Description, c.Draft)
}

func runAdd(g *globals, args []string, title, status, priority, typ, dependsOn, description string, draft bool) error {
	// Task descriptions are JSON-encoded markdown — decode \n in both
	// the --description flag and the free-form positional arg.
	if description != "" {
		description = strings.ReplaceAll(description, `\n`, "\n")
	}
	// Parse args if provided
	if len(args) > 0 {
		input := strings.Join(args, " ")
		input = strings.ReplaceAll(input, `\n`, "\n")
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
		// (3) Extract title (everything before first period or newline), remainder is description
		var parsedTitle, parsedDescription string
		dotIdx := strings.Index(input, ".")
		nlIdx := strings.Index(input, "\n")
		// Use whichever delimiter comes first (-1 means not found)
		idx := dotIdx
		if idx < 0 || (nlIdx >= 0 && nlIdx < idx) {
			idx = nlIdx
		}
		if idx >= 0 {
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
	if err := meads.ValidateStatus(status); err != nil {
		return err
	}
	if typ != "" {
		if err := meads.ValidateType(typ); err != nil {
			return err
		}
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
