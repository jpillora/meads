package main

type readyCmd struct {
	globals *globals
	JSON    bool   `help:"Output tasks as JSON"`
	Limit   int    `opts:"short=n" help:"Limit number of results"`
	Tag     string `help:"Filter by tag — comma-separated to require all of them (e.g. api,backend)"`
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
	// Filter before the limit, so --limit counts what is actually shown.
	tasks = filterByTag(tasks, c.Tag)
	if c.Limit > 0 && len(tasks) > c.Limit {
		tasks = tasks[:c.Limit]
	}
	return printTasks(tasks, c.JSON)
}
