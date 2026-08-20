package main

import (
	"context"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"syscall"
	"time"

	"github.com/jpillora/meads/pkg/webui"
)

type webuiCmd struct {
	globals *globals
	Port    int    `help:"TCP port (0 = pick a free one)" opts:"default=0"`
	Host    string `help:"Bind address" opts:"default=127.0.0.1"`
	Token   string `help:"Bearer token (auto-generated if empty)"`
	NoToken bool   `opts:"name=no-token" help:"Serve without token auth - every request is allowed. Conflicts with --token"`
	Open    bool   `help:"Open the UI in the default browser after start"`
	Print   string `help:"Startup line format: url|json|none" opts:"default=json"`
}

func (c *webuiCmd) Run() error {
	if err := c.globals.modeConflictErr(); err != nil {
		return err
	}
	store, err := c.globals.tasks()
	if err != nil {
		return err
	}
	srv, err := webui.New(webui.Config{
		Store:   store,
		Host:    c.Host,
		Port:    c.Port,
		Token:   c.Token,
		NoToken: c.NoToken,
		Print:   c.Print,
		Open:    c.Open,
		Dev:     os.Getenv("WEBUI_DEV") == "1",
	})
	if err != nil {
		return err
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- srv.Run(ctx) }()

	if c.Open {
		go waitAndOpen(ctx, srv)
	}

	return <-done
}

// waitAndOpen polls until the server has an address, then opens it in the browser.
func waitAndOpen(ctx context.Context, srv *webui.Server) {
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	for {
		if url := srv.URL(); url != "" {
			openBrowser(webui.BrowseURL(url, srv.Token()))
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-deadline.C:
			return
		case <-ticker.C:
		}
	}
}

func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("cmd", "/c", "start", "", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	_ = cmd.Start()
}
