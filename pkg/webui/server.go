// Package webui hosts an HTTP server serving a JSON API and embedded web UI
// for a single TASKS.md/csv file. It is started by the `md webui` subcommand
// and consumed both by browsers and by the meads VS Code extension.
package webui

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/go-git/go-billy/v5"
	"github.com/jpillora/meads/pkg/meads"
)

// Config controls a Server.
type Config struct {
	// Store is the task backend to serve. *meads.Store (file mode) and the
	// git-mode adapter cmd/md/webui.go constructs both satisfy it - see
	// meads.TaskStore's doc comment. Richer capabilities that only one
	// backend can offer - a file to fsnotify, or ref oids to poll for
	// changes - are discovered through the optional fileLocator/
	// refSnapshotter interfaces (see watch.go and storeLocation) rather
	// than required by this field's type.
	Store meads.TaskStore
	// Host to bind, e.g. "127.0.0.1". Defaults to 127.0.0.1.
	Host string
	// Port to listen on. 0 = pick a random free port.
	Port int
	// Token required on every request (bearer or ?token=). Auto-generated if empty.
	Token string
	// Stdout format on start: "url", "json", or "none".
	Print string
	// Open browser after start (ignored when Print=none).
	Open bool
	// Dev mode: serve assets from disk rather than embed. Enabled via WEBUI_DEV=1.
	Dev bool
	// Stdout target for the start line. Defaults to os.Stdout.
	Stdout io.Writer
	// Stderr target for log messages. Defaults to os.Stderr.
	Stderr io.Writer
}

// Server is an HTTP server exposing the webui API.
type Server struct {
	cfg Config
	// listenerMu guards listener: Run() assigns it from its own goroutine
	// once the port is bound, while Addr()/URL() are meant to be polled from
	// a caller's separate goroutine until that happens (see cmd/md/webui.go's
	// waitAndOpen, and TestIntegration_GitMode_Webui_EndToEnd) - without a
	// lock that's an unsynchronized read/write on the same field.
	listenerMu sync.RWMutex
	listener   net.Listener
	http       *http.Server
	events     *eventBus
	bind       *bindHub
	watcher    *watcher
	startOnce  sync.Once
	shutdown   context.CancelFunc
}

// New builds a Server from cfg. Call Run to start.
func New(cfg Config) (*Server, error) {
	if cfg.Store == nil {
		return nil, fmt.Errorf("webui: Store is required")
	}
	if cfg.Host == "" {
		cfg.Host = "127.0.0.1"
	}
	if cfg.Print == "" {
		cfg.Print = "json"
	}
	if cfg.Stdout == nil {
		cfg.Stdout = os.Stdout
	}
	if cfg.Stderr == nil {
		cfg.Stderr = os.Stderr
	}
	if cfg.Token == "" {
		t, err := randomToken()
		if err != nil {
			return nil, err
		}
		cfg.Token = t
	}
	s := &Server{
		cfg:    cfg,
		events: newEventBus(),
		bind:   newBindHub(),
	}
	return s, nil
}

// Addr returns the final listen address. Only valid after Run returns a non-blocking URL.
func (s *Server) Addr() string {
	lis := s.getListener()
	if lis == nil {
		return ""
	}
	return lis.Addr().String()
}

// URL returns the base URL clients should use. Only valid after the listener is bound.
func (s *Server) URL() string {
	lis := s.getListener()
	if lis == nil {
		return ""
	}
	host := s.cfg.Host
	port := lis.Addr().(*net.TCPAddr).Port
	return fmt.Sprintf("http://%s:%d", host, port)
}

// getListener and setListener guard concurrent access to listener - see its
// field doc comment on Server.
func (s *Server) getListener() net.Listener {
	s.listenerMu.RLock()
	defer s.listenerMu.RUnlock()
	return s.listener
}

func (s *Server) setListener(lis net.Listener) {
	s.listenerMu.Lock()
	s.listener = lis
	s.listenerMu.Unlock()
}

// Token returns the bearer token required by all HTTP routes.
func (s *Server) Token() string { return s.cfg.Token }

// Run binds the listener, prints the start line, and blocks until ctx is canceled.
func (s *Server) Run(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	s.shutdown = cancel

	lis, err := net.Listen("tcp", fmt.Sprintf("%s:%d", s.cfg.Host, s.cfg.Port))
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	s.setListener(lis)

	mux := s.routes()
	s.http = &http.Server{Handler: withMiddleware(mux, s.cfg.Token)}

	if err := s.printStartLine(); err != nil {
		return err
	}

	// Start file watcher if possible. Failures are non-fatal.
	if w, err := startWatcher(ctx, s.cfg.Store, s.events, s.cfg.Stderr); err == nil {
		s.watcher = w
	} else {
		fmt.Fprintf(s.cfg.Stderr, "webui: watcher disabled: %v\n", err)
	}

	serveErr := make(chan error, 1)
	go func() {
		err := s.http.Serve(lis)
		if err != nil && err != http.ErrServerClosed {
			serveErr <- err
			return
		}
		serveErr <- nil
	}()

	select {
	case <-ctx.Done():
	case err := <-serveErr:
		if err != nil {
			return err
		}
	}

	// Graceful shutdown.
	shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelShutdown()
	_ = s.http.Shutdown(shutdownCtx)
	if s.watcher != nil {
		s.watcher.Close()
	}
	s.bind.closeAll()
	s.events.closeAll()
	return nil
}

// Stop signals Run to exit.
func (s *Server) Stop() {
	if s.shutdown != nil {
		s.shutdown()
	}
}

func (s *Server) printStartLine() error {
	if s.cfg.Print == "none" {
		return nil
	}
	path, format := storeLocation(s.cfg.Store)
	info := startInfo{
		URL:    s.URL(),
		Token:  s.cfg.Token,
		File:   path,
		Format: format,
	}
	switch s.cfg.Print {
	case "json":
		raw, err := json.Marshal(info)
		if err != nil {
			return err
		}
		fmt.Fprintf(s.cfg.Stdout, "%s\n", raw)
	case "url":
		fmt.Fprintf(s.cfg.Stdout, "%s/?token=%s\n", info.URL, info.Token)
	default:
		return fmt.Errorf("invalid --print value %q (want url|json|none)", s.cfg.Print)
	}
	return nil
}

type startInfo struct {
	URL    string `json:"url"`
	Token  string `json:"token"`
	File   string `json:"file"`
	Format string `json:"format"`
}

// fileLocator is implemented by file-mode stores (namely *meads.Store) to
// describe the on-disk file backing them. storeLocation and the fsnotify
// watcher (startFileWatcher, in watch.go) both use it. Git mode has no
// single file and does not implement it; storeLocation falls back to a
// fixed description in that case, and startWatcher falls back to
// refSnapshotter instead (see watch.go).
type fileLocator interface {
	FS() billy.Filesystem
	Path() string
}

// storeLocation returns a display path and a short format label ("md",
// "csv", or "git") for store, for the startup banner (printStartLine) and
// GET /api/file (handleFileInfo).
func storeLocation(store meads.TaskStore) (path, format string) {
	fl, ok := store.(fileLocator)
	if !ok {
		return meads.TasksRefPrefix + "*", "git"
	}
	path = filepath.Join(fl.FS().Root(), fl.Path())
	if strings.HasSuffix(fl.Path(), ".csv") {
		return path, "csv"
	}
	return path, "md"
}

func randomToken() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
