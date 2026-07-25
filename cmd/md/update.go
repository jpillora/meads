package main

import (
	"fmt"
	"os"
	"strconv"

	"github.com/jpillora/meads/pkg/meads"
)

type updateCmd struct {
	globals         *globals
	ID              string `opts:"mode=arg" help:"Task ID to update"`
	Status          string `help:"Set task status (draft, open, inprogress, blocked, closed)"`
	Priority        string `help:"Set task priority (P0-P9 or 0-9)"`
	Title           string `help:"Set task title"`
	Type            string `help:"Set task type (bug, task, feature, idea)"`
	Description     string `help:"Set task description — markdown with JSON-style escapes (\\n, \\t, \\uXXXX, etc.)"`
	DescriptionFile string `opts:"name=description-file" help:"Path to a markdown file to use as the task description"`
	StatusReason    string `help:"Set status reason"`
}

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
	description := decodeJSONEscapes(c.Description)
	if c.DescriptionFile != "" {
		data, err := os.ReadFile(c.DescriptionFile)
		if err != nil {
			return fmt.Errorf("reading description file: %w", err)
		}
		description = string(data)
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
	fmt.Printf("updated task %d\n", id)
	return nil
}
