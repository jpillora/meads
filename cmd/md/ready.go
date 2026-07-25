package main

type readyCmd struct {
	globals *globals
	JSON    bool `help:"Output tasks as JSON"`
	Limit   int  `opts:"short=n" help:"Limit number of results"`
}

func (c *readyCmd) Run() error {
	ts, err := c.globals.tasks()
	if err != nil {
		return err
	}
	tasks, err := ts.Ready()
	if err != nil {
		return err
	}
	// A cycle hides its tasks from this list forever; flag it so the omission
	// isn't silent.
	warnCycles(c.globals)
	if c.Limit > 0 && len(tasks) > c.Limit {
		tasks = tasks[:c.Limit]
	}
	return printTasks(tasks, c.JSON)
}
