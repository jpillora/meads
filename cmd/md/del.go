package main

import (
	"fmt"
	"os"
	"strconv"

	"github.com/jpillora/meads/pkg/meads"
)

type delCmd struct {
	globals *globals
	Force   bool   `help:"Erase the task instead of tombstoning it — unrecoverable, and frees its id for reuse"`
	ID      string `opts:"mode=arg" help:"Task ID to delete"`
}

func (c *delCmd) Run() error {
	id, err := strconv.Atoi(c.ID)
	if err != nil {
		return fmt.Errorf("invalid task ID: %s", c.ID)
	}
	ts, err := c.globals.tasks()
	if err != nil {
		return err
	}
	tasks, err := ts.Get([]int{id})
	if err != nil {
		return err
	}
	if c.Force {
		return c.runForce(id, tasks[0])
	}
	if err := ts.Delete(id); err != nil {
		return err
	}
	deleted := tasks[0]
	deleted.Deleted = true
	postWebhook(c.globals, "delete", deleted)
	scheduleSync(c.globals)
	fmt.Printf("deleted task %d\n", id)
	return nil
}

// runForce erases the task rather than tombstoning it.
//
// The warning it prints is the point of the separate path. An ordinary
// delete is reversible (`md restore`) and, more importantly, keeps the id
// spent forever - a tombstone is what makes "task 412" mean one thing for
// the life of the repo. Erasing the HIGHEST id gives that number back: both
// backends derive the next id from what still exists (GitStore.NextID walks
// the task refs; nextID in tombstone.go takes the max task or the "max-id"
// mark), so the next `md add` reuses it and every existing reference to the
// old task - commit messages, branch names, links in other descriptions -
// silently retargets. Saying which id is now reusable, and whether it is the
// one at risk, is the only mitigation available, so it is never suppressed.
func (c *delCmd) runForce(id int, task meads.Task) error {
	ts, err := c.globals.tasks()
	if err != nil {
		return err
	}
	history, err := ts.GetHistory()
	if err != nil {
		return err
	}
	highest := 0
	for _, t := range history {
		if t.ID > highest {
			highest = t.ID
		}
	}
	if err := ts.HardDelete(id); err != nil {
		return err
	}
	postWebhook(c.globals, "delete", task)
	scheduleSync(c.globals)
	fmt.Printf("erased task %d — unrecoverable, not a tombstone\n", id)
	if id == highest {
		fmt.Fprintf(os.Stderr, "warning: %d was the highest id, so the next 'md add' will reuse it for a different task\n", id)
	}
	return nil
}
