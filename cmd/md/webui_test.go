package main

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestWebui_EndToEnd builds the md binary, spawns `md webui --port=0`, parses
// the stdout start line, and hits /api/tasks with the discovered token.
func TestWebui_EndToEnd(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping binary spawn test in short mode")
	}
	// Build the binary into a temp dir.
	bin := filepath.Join(t.TempDir(), "md")
	build := exec.Command("go", "build", "-o", bin, ".")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}

	h := newHarness(t)
	h.addTask("From test")

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "--tasks-file", h.globals.TasksFile, "webui", "--port=0", "--print=json")
	cmd.Dir = h.dir
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer func() {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
		_ = cmd.Wait()
	}()

	line, err := readStartLine(stdout, 5*time.Second)
	if err != nil {
		t.Fatalf("read start line: %v", err)
	}
	var info struct {
		URL   string `json:"url"`
		Token string `json:"token"`
	}
	if err := json.Unmarshal([]byte(line), &info); err != nil {
		t.Fatalf("parse start line %q: %v", line, err)
	}
	if info.URL == "" || info.Token == "" {
		t.Fatalf("incomplete start info: %+v", info)
	}

	// GET /api/tasks with the token.
	req, _ := http.NewRequest(http.MethodGet, info.URL+"/api/tasks", nil)
	req.Header.Set("Authorization", "Bearer "+info.Token)
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("GET /api/tasks: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	var tasks []map[string]any
	if err := json.Unmarshal(body, &tasks); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(tasks) != 1 {
		t.Errorf("expected 1 task, got %d: %s", len(tasks), body)
	}
	if got, _ := tasks[0]["title"].(string); got != "From test" {
		t.Errorf("unexpected title: %v", tasks[0])
	}
}

func readStartLine(r io.Reader, timeout time.Duration) (string, error) {
	done := make(chan struct{})
	var line string
	var err error
	go func() {
		defer close(done)
		s := bufio.NewScanner(r)
		for s.Scan() {
			t := s.Text()
			if strings.HasPrefix(t, "MEADS_WEBUI ") {
				line = strings.TrimPrefix(t, "MEADS_WEBUI ")
				return
			}
		}
		err = s.Err()
		if err == nil {
			err = io.EOF
		}
	}()
	select {
	case <-done:
		return line, err
	case <-time.After(timeout):
		return "", context.DeadlineExceeded
	}
}
