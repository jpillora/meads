package main

type readyCmd struct {
	globals *globals
	JSON    bool   `help:"Output tasks as JSON"`
	Limit   int    `opts:"short=n" help:"Limit number of results (0 means no limit)"`
	Offset  int    `help:"Skip this many results before applying the limit"`
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
	// Filter before pagination, so offsets and limits count what is actually shown.
	tasks = filterByTag(tasks, c.Tag)
	tasks, err = paginateTasks(tasks, c.Limit, c.Offset)
	if err != nil {
		return err
	}
	return printTasks(tasks, c.JSON)
}
