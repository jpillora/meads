package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"syscall"
	"time"

	"github.com/jpillora/meads/pkg/meads"
	"github.com/jpillora/meads/pkg/webui"
)

type webuiCmd struct {
	globals *globals
	Port    int    `help:"TCP port (0 = pick a free one)" opts:"default=0"`
	Host    string `help:"Bind address" opts:"default=127.0.0.1"`
	Token   string `help:"Bearer token (auto-generated if empty)"`
	Open    bool   `help:"Open the UI in the default browser after start"`
	Print   string `help:"Startup line format: url|json|none" opts:"default=json"`
}

func (c *webuiCmd) Run() error {
	if err := c.globals.modeConflictErr(); err != nil {
		return err
	}
	store, err := c.store()
	if err != nil {
		return err
	}
	srv, err := webui.New(webui.Config{
		Store: store,
		Host:  c.Host,
		Port:  c.Port,
		Token: c.Token,
		Print: c.Print,
		Open:  c.Open,
		Dev:   os.Getenv("WEBUI_DEV") == "1",
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

// store resolves the meads.Tasks to serve over the web UI. File mode passes
// meads.FileTasks, which delegates FS()/Path() to the underlying
// *meads.Store so the fsnotify watcher and startup banner keep working (see
// pkg/webui's fileLocator). Git mode wraps meads.GitTasks in gitWatchStore
// so the ref-polling watcher can use GitStore.TaskRefOIDs (see pkg/webui's
// refSnapshotter).
func (c *webuiCmd) store() (meads.Tasks, error) {
	if c.globals.mode() != modeGit {
		return meads.NewFileTasks(c.globals.store(), c.globals.git()), nil
	}
	if !c.globals.inGitRepo() {
		return nil, fmt.Errorf("--git requires a git repository")
	}
	return gitWatchStore{meads.NewGitTasks(c.globals.gitStore())}, nil
}

// gitWatchStore is meads.GitTasks for the web UI's git-mode wiring: the
// embedded type already carries TaskRefOIDs (promoted here through
// embedding), which pkg/webui's refSnapshotter discovers structurally for
// its change-detection watcher (see pkg/webui/watch.go). TaskRefOIDs is not
// part of meads.Tasks itself: no CLI command needs it, only the watcher.
type gitWatchStore struct {
	meads.GitTasks
}

// waitAndOpen polls until the server has an address, then opens it in the browser.
func waitAndOpen(ctx context.Context, srv *webui.Server) {
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	for {
		if url := srv.URL(); url != "" {
			openBrowser(url + "/?token=" + srv.Token())
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
