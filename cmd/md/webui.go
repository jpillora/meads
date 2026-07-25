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

// store resolves the meads.TaskStore to serve over the web UI. File mode
// passes *meads.Store directly, unchanged, so the fsnotify watcher and
// startup banner can use its FS()/Path() (see pkg/webui's fileLocator). Git
// mode wraps gitTaskStore (taskstore.go) in gitWatchStore so the
// ref-polling watcher can use GitStore.TaskRefOIDs (see pkg/webui's
// refSnapshotter) - a bare gitTaskStore has no such method, since no CLI
// command needs it.
func (c *webuiCmd) store() (meads.TaskStore, error) {
	if c.globals.mode() != modeGit {
		return c.globals.store(), nil
	}
	if !c.globals.inGitRepo() {
		return nil, fmt.Errorf("--git requires a git repository")
	}
	return gitWatchStore{gitTaskStore{gs: c.globals.gitStore()}}, nil
}

// gitWatchStore adds pkg/webui's ref-polling watch support on top of
// gitTaskStore's CRUD methods, which already satisfy meads.TaskStore
// structurally (see taskstore.go's doc comment on gitTaskStore - Go
// interfaces are matched by method set, not by declared type, so embedding
// is enough). TaskRefOIDs is not part of taskStore itself: no CLI command
// needs it, only the web UI's change-detection watcher (see
// pkg/webui/watch.go's refSnapshotter).
type gitWatchStore struct {
	gitTaskStore
}

func (g gitWatchStore) TaskRefOIDs() (map[string]meads.OID, error) {
	return g.gs.TaskRefOIDs()
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
