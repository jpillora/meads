package main

type readyCmd struct {
	globals *globals
	JSON    bool `help:"Output tasks as JSON"`
	Limit   int  `opts:"short=n" help:"Limit number of results"`
}

func (c *readyCmd) Run() error {
	tasks, err := c.globals.store().Ready()
	if err != nil {
		return err
	}
	if c.Limit > 0 && len(tasks) > c.Limit {
		tasks = tasks[:c.Limit]
	}
	return printTasks(tasks, c.JSON)
}
