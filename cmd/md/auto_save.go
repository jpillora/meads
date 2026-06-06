package main

import (
	"fmt"
	"os"
)

type autoSaveCmd struct {
	globals *globals
	Disable bool `opts:"mode=flag" help:"Disable auto-save by removing the pre-commit hook"`
	Status  bool `opts:"mode=flag" help:"Check if auto-save is enabled"`
}

// autoSaveBlock is the staging half of auto-delete: it keeps the tasks file
// staged so changes ride along in every commit, without pruning anything. It
// lives beside the auto-delete block in the same pre-commit hook.
var autoSaveBlock = hookBlock{
	marker:  "# md auto-save hook",
	comment: "Always stage the tasks file so it rides along in every commit",
	command: "md auto-save",
}

func (c *autoSaveCmd) Run() error {
	// Running inside the pre-commit hook: stage the tasks file.
	if os.Getenv("GITHOOK") == "1" {
		return c.runFromHook()
	}

	// Otherwise manage hook installation.
	if c.Status {
		on, err := autoSaveBlock.installed(c.globals)
		if err != nil {
			return err
		}
		fmt.Printf("auto-save: %s\n", enabledLabel(on))
		return nil
	}
	if c.Disable {
		removed, err := autoSaveBlock.remove(c.globals)
		if err != nil {
			return err
		}
		if removed {
			fmt.Println("auto-save disabled")
		} else {
			fmt.Println("auto-save is not enabled")
		}
		return nil
	}
	installed, err := autoSaveBlock.install(c.globals)
	if err != nil {
		return err
	}
	if installed {
		fmt.Println("auto-save enabled")
	} else {
		fmt.Println("auto-save is already enabled")
	}
	return nil
}

// runFromHook stages the tasks file so the in-progress commit captures the
// latest task changes. Unlike auto-delete it runs on every branch — staging is
// always safe — and never modifies file content, so there is nothing to back up.
func (c *autoSaveCmd) runFromHook() error {
	tasksFile := c.globals.TasksFile
	if _, err := os.Stat(tasksFile); os.IsNotExist(err) {
		return nil
	}
	if err := c.globals.git().Run("add", tasksFile); err != nil {
		return fmt.Errorf("staging %s: %w", tasksFile, err)
	}
	return nil
}

// enabledLabel maps a boolean to the status word used by hook status output.
func enabledLabel(on bool) string {
	if on {
		return "enabled"
	}
	return "disabled"
}
