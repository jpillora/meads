package main

import (
	"fmt"
	"strconv"

	"github.com/jpillora/meads/pkg/meads"
)

type updateCmd struct {
	globals      *globals
	ID           string `opts:"mode=arg" help:"Task ID to update"`
	Status       string `help:"Set task status (draft, open, inprogress, blocked, closed)"`
	Priority     string `help:"Set task priority (P0-P9 or 0-9)"`
	Title        string `help:"Set task title"`
	Type         string `help:"Set task type (bug, task, feature, idea)"`
	Description  string `help:"Set task description — markdown with JSON-style escapes (\\n, \\t, \\uXXXX, etc.)"`
	StatusReason string `help:"Set status reason"`
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
	priority := c.Priority
	if priority != "" {
		var perr error
		priority, perr = meads.NormalizePriority(priority)
		if perr != nil {
			return perr
		}
	}
	description := decodeJSONEscapes(c.Description)
	var updated meads.Task
	err = c.globals.store().Update(id, func(t *meads.Task) {
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
