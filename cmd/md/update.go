package main

import (
	"fmt"
	"strconv"

	"github.com/jpillora/meads/pkg/meads"
)

type updateCmd struct {
	globals         *globals
	ID              string   `opts:"mode=arg" help:"Task ID to update"`
	Status          string   `help:"Set task status (draft, open, inprogress, blocked, closed)"`
	Priority        string   `help:"Set task priority (P0-P9 or 0-9)"`
	Title           string   `help:"Set task title"`
	Type            string   `help:"Set task type (bug, task, feature, idea)"`
	Tags            tagsFlag `opts:"mode=flag" help:"Replace task tags — comma-separated, each lowercase letters, numbers and dashes ('--tags=' clears them all)"`
	AddTags         string   `help:"Add tags, keeping the existing ones — comma-separated"`
	RmTags          string   `help:"Remove tags, keeping the rest — comma-separated"`
	Description     string   `help:"Set task description inline — markdown with JSON-style escapes (\\n, \\t, \\uXXXX, etc.)"`
	DescriptionFile string   `opts:"name=description-file" help:"Path to a markdown file to use as the task description; use - to read stdin (for example, a quoted HEREDOC). Content is taken literally, with trailing blank lines trimmed"`
	StatusReason    string   `help:"Set status reason"`
}

// tagsFlag is a --tags value that remembers whether the flag was passed at
// all, so an empty '--tags=' can mean "clear every tag" while an absent
// flag keeps meaning "leave tags alone" - the distinction a plain string
// field cannot make, and every other update flag simply does without. opts
// drives it through its Setter interface (the flag.Value seam), so Set is
// only ever called for a flag that really appeared on the command line.
//
// The field needs an explicit `opts:"mode=flag"`: opts defaults a
// struct-typed field to mode=embedded and would recurse into it looking for
// nested options instead of registering --tags at all.
type tagsFlag struct {
	set   bool
	value string
}

func (f *tagsFlag) Set(s string) error {
	f.set, f.value = true, s
	return nil
}

// String is on the value receiver so opts' help rendering (which formats
// the field value, not its address) prints the tags rather than the struct.
func (f tagsFlag) String() string { return f.value }

func (c *updateCmd) Run() error {
	id, err := strconv.Atoi(c.ID)
	if err != nil {
		return fmt.Errorf("invalid task ID: %s", c.ID)
	}
	if c.Status != "" {
		if err := meads.ValidateStatus(c.Status); err != nil {
			return err
		}
	}
	if c.Type != "" {
		if err := meads.ValidateType(c.Type); err != nil {
			return err
		}
	}
	// --description and --description-file are mutually exclusive
	if c.Description != "" && c.DescriptionFile != "" {
		return fmt.Errorf("cannot use both --description and --description-file")
	}
	// --tags replaces the whole set, so combining it with the incremental
	// flags would make the result depend on which one won.
	if c.Tags.set && (c.AddTags != "" || c.RmTags != "") {
		return fmt.Errorf("cannot use --tags with --add-tags or --rm-tags")
	}
	setTags, err := meads.NormalizeTags(c.Tags.value)
	if err != nil {
		return err
	}
	addTags, err := meads.NormalizeTags(c.AddTags)
	if err != nil {
		return err
	}
	rmTags, err := meads.NormalizeTags(c.RmTags)
	if err != nil {
		return err
	}
	description := decodeJSONEscapes(c.Description)
	if c.DescriptionFile != "" {
		description, err = readDescriptionFile(c.DescriptionFile, c.globals.stdinReader())
		if err != nil {
			return err
		}
	}
	priority := c.Priority
	if priority != "" {
		var perr error
		priority, perr = meads.NormalizePriority(priority)
		if perr != nil {
			return perr
		}
	}
	ts, err := c.globals.tasks()
	if err != nil {
		return err
	}
	var updated meads.Task
	err = ts.Update(id, func(t *meads.Task) {
		if c.Status != "" {
			t.SetStatus(c.Status)
		}
		if priority != "" {
			t.SetPriority(priority)
		}
		if c.Title != "" {
			t.Title = c.Title
		}
		if c.Type != "" {
			t.SetType(c.Type)
		}
		if c.Tags.set {
			t.SetTags(setTags)
		} else if len(addTags) > 0 || len(rmTags) > 0 {
			t.SetTags(t.Tags.Add(addTags).Remove(rmTags))
		}
		if description != "" {
			t.Description = description
		}
		if c.StatusReason != "" {
			t.StatusReason = c.StatusReason
		}
		updated = *t
	})
	if err != nil {
		return err
	}
	postWebhook(c.globals, "update", updated)
	scheduleSync(c.globals)
	fmt.Printf("updated task %d\n", id)
	return nil
}
