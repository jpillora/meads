package main

import (
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/jpillora/meads/pkg/meads"
)

type addCmd struct {
	globals         *globals
	Args            []string `opts:"mode=arg,min=0" help:"Note: task descriptions are JSON-encoded markdown — '\\n', '\\t', '\\uXXXX', etc. all decode.\n\nTwo modes for setting task fields:\n\n(1) Free-form string. Pass a single argument and meads extracts:\n      type prefix:   bug: / task: / feature: / idea:   (optional)\n      priority:      P0-P9 anywhere in the string      (default P2)\n      title:         text before the first '. ' (period+space) or newline\n      description:   text after that split point\n    Examples:\n      md add 'Fix the login bug'\n      md add 'bug: Fix login P1. Session cookie expires after 5min'\n      md add 'Fix login.\\nSession cookie expires.\\n\\nSteps to repro...'\n\n(2) Explicit flags. Set each field via --title, --type, --priority, etc.\n    Example:\n      md add --type=bug --priority=P1 --title='Fix login' \\\n             --description='## Steps\\n1. Repro\\n2. Patch'"`
	Title           string   `help:"Set task title"`
	Status          string   `help:"Set task status (draft, open, inprogress, blocked, closed)"`
	Priority        string   `help:"Set task priority (P0-P9 or 0-9)"`
	Type            string   `help:"Set task type (bug, task, feature, idea)"`
	DependsOn       string   `opts:"name=depends-on" help:"Set dependency task ID"`
	Description     string   `help:"Set task description — markdown with JSON-style escapes (\\n, \\t, \\uXXXX, etc.)"`
	DescriptionFile string   `opts:"name=description-file" help:"Path to a markdown file to use as the task description"`
	Draft           bool     `help:"Create task with draft status (defaults to open)"`
}

type createCmd struct {
	addCmd
}

var (
	typeRe     = regexp.MustCompile(`^(bug|task|feature|idea):`)
	priorityRe = regexp.MustCompile(`\bP(\d)\b`)
	// title/description split: period+whitespace (sentence boundary) or bare newline.
	// Bare "." doesn't split, so URLs like http://foo.com stay intact.
	splitRe = regexp.MustCompile(`\.\s|\n`)
)

func (c *addCmd) Run() error {
	return runAdd(c.globals, c.Args, c.Title, c.Status, c.Priority, c.Type, c.DependsOn, c.Description, c.DescriptionFile, c.Draft)
}

// decodeJSONEscapes processes JSON-style escape sequences (\n, \t, \\, \", \uXXXX,
// etc.) so multi-line markdown fits in a single shell argument. Unrecognised
// escapes and unescaped bytes pass through unchanged.
func decodeJSONEscapes(s string) string {
	if !strings.ContainsRune(s, '\\') {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for len(s) > 0 {
		if s[0] != '\\' {
			b.WriteByte(s[0])
			s = s[1:]
			continue
		}
		r, _, rest, err := strconv.UnquoteChar(s, 0)
		if err != nil {
			b.WriteByte(s[0])
			s = s[1:]
			continue
		}
		b.WriteRune(r)
		s = rest
	}
	return b.String()
}

func runAdd(g *globals, args []string, title, status, priority, typ, dependsOn, description, descriptionFile string, draft bool) error {
	// --description and --description-file are mutually exclusive
	if description != "" && descriptionFile != "" {
		return fmt.Errorf("cannot use both --description and --description-file")
	}
	// Read description from file if provided
	if descriptionFile != "" {
		data, err := os.ReadFile(descriptionFile)
		if err != nil {
			return fmt.Errorf("reading description file: %w", err)
		}
		description = string(data)
	}
	// Task descriptions are JSON-encoded markdown — decode \n, \uXXXX, etc.
	// in both the --description flag and the free-form positional arg.
	description = decodeJSONEscapes(description)
	// Parse args if provided
	if len(args) > 0 {
		input := strings.Join(args, " ")
		input = decodeJSONEscapes(input)
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
		// (3) Split title/description at the first ". " (period+space) or newline.
		var parsedTitle, parsedDescription string
		if loc := splitRe.FindStringIndex(input); loc != nil {
			parsedTitle = strings.TrimSpace(input[:loc[0]])
			parsedDescription = strings.TrimSpace(input[loc[1]:])
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
