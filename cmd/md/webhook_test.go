package main

import (
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/jpillora/meads/pkg/meads"
)

func TestPostWebhook_NilGlobals(t *testing.T) {
	// Should not panic with nil globals.
	postWebhook(nil, "add", nil)
}

func TestPostWebhook_EmptyURL(t *testing.T) {
	// Should be a no-op when URL is empty.
	postWebhook(&globals{}, "add", nil)
}

func TestPostWebhook_HTTP(t *testing.T) {
	var received webhookPayload
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("content-type = %s, want application/json", ct)
		}
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &received); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	g := &globals{WebhookURI: ts.URL}
	postWebhook(g, "add", map[string]int{"id": 42})

	if !received.Meads {
		t.Error("meads = false, want true")
	}
	if received.Action != "add" {
		t.Errorf("action = %q, want %q", received.Action, "add")
	}
	data, ok := received.Data.(map[string]any)
	if !ok {
		t.Fatalf("data type = %T, want map[string]any", received.Data)
	}
	if id, _ := data["id"].(float64); id != 42 {
		t.Errorf("data.id = %v, want 42", data["id"])
	}
}

func TestPostWebhook_IncludesFile(t *testing.T) {
	var received webhookPayload
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &received)
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	// A relative tasks file must be resolved to an absolute path so a consumer
	// watching multiple tasks files can distinguish their events.
	g := &globals{WebhookURI: ts.URL, TasksFile: "TASKS.md"}
	postWebhook(g, "add", map[string]int{"id": 1})

	if !filepath.IsAbs(received.File) {
		t.Errorf("file = %q, want absolute path", received.File)
	}
	if filepath.Base(received.File) != "TASKS.md" {
		t.Errorf("file base = %q, want TASKS.md", filepath.Base(received.File))
	}

	// An already-absolute tasks file must pass through unchanged.
	abs := filepath.Join(t.TempDir(), "custom.md")
	g2 := &globals{WebhookURI: ts.URL, TasksFile: abs}
	postWebhook(g2, "add", nil)
	if received.File != abs {
		t.Errorf("file = %q, want %q", received.File, abs)
	}
}

func TestPostWebhook_Unix(t *testing.T) {
	dir := t.TempDir()
	socketPath := filepath.Join(dir, "test.sock")

	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()

	var received webhookPayload
	srv := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			body, _ := io.ReadAll(r.Body)
			json.Unmarshal(body, &received)
			w.WriteHeader(http.StatusOK)
		}),
	}
	go srv.Serve(listener)
	defer srv.Close()

	g := &globals{WebhookURI: "unix://" + socketPath}
	postWebhook(g, "delete", map[string]int{"id": 7})

	if !received.Meads {
		t.Error("meads = false, want true")
	}
	if received.Action != "delete" {
		t.Errorf("action = %q, want %q", received.Action, "delete")
	}
}

func TestPostWebhook_UnixWithPath(t *testing.T) {
	dir := t.TempDir()
	socketPath := filepath.Join(dir, "test.sock")

	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()

	var receivedPath string
	var received webhookPayload
	srv := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			receivedPath = r.URL.Path
			body, _ := io.ReadAll(r.Body)
			json.Unmarshal(body, &received)
			w.WriteHeader(http.StatusOK)
		}),
	}
	go srv.Serve(listener)
	defer srv.Close()

	g := &globals{WebhookURI: "unix://[" + socketPath + "]/hooks/meads"}
	postWebhook(g, "add", map[string]int{"id": 1})

	if receivedPath != "/hooks/meads" {
		t.Errorf("path = %q, want %q", receivedPath, "/hooks/meads")
	}
	if received.Action != "add" {
		t.Errorf("action = %q, want %q", received.Action, "add")
	}
}

func TestPostWebhook_ServerError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer ts.Close()

	// Should not panic or return error; just logs to stderr.
	g := &globals{WebhookURI: ts.URL}
	postWebhook(g, "add", nil)
}

func TestPostWebhook_Unreachable(t *testing.T) {
	// Should not panic; logs to stderr.
	g := &globals{WebhookURI: "http://127.0.0.1:1"}
	postWebhook(g, "add", nil)
}

func TestParseUnixURI(t *testing.T) {
	tests := []struct {
		input      string
		wantSocket string
		wantPath   string
	}{
		{"unix:///var/run/app.sock", "/var/run/app.sock", "/"},
		{"unix://[/var/run/app.sock]/webhook", "/var/run/app.sock", "/webhook"},
		{"unix://[/var/run/app.sock]/api/hooks", "/var/run/app.sock", "/api/hooks"},
		{"unix://[/var/run/app.sock]", "/var/run/app.sock", "/"},
		{"unix://[/var/run/app.sock]/", "/var/run/app.sock", "/"},
		{"unix://[/path:with:colons.sock]/hook", "/path:with:colons.sock", "/hook"},
	}
	for _, tt := range tests {
		sock, path := parseUnixURI(tt.input)
		if sock != tt.wantSocket {
			t.Errorf("parseUnixURI(%q) socket = %q, want %q", tt.input, sock, tt.wantSocket)
		}
		if path != tt.wantPath {
			t.Errorf("parseUnixURI(%q) path = %q, want %q", tt.input, path, tt.wantPath)
		}
	}
}

func TestWebhookHTTPURL(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"http://example.com/hook", "http://example.com/hook"},
		{"https://example.com/hook", "https://example.com/hook"},
		{"unix:///var/run/app.sock", "http://localhost/"},
		{"unix://[/var/run/app.sock]/webhook", "http://localhost/webhook"},
		{"unix://[/var/run/app.sock]/api/hooks", "http://localhost/api/hooks"},
	}
	for _, tt := range tests {
		got := webhookHTTPURL(tt.input)
		if got != tt.want {
			t.Errorf("webhookHTTPURL(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestTasksFileFlag(t *testing.T) {
	// Test that commands properly use the Store from globals
	// by creating a task in a custom file path and reading it back.
	dir := t.TempDir()
	file := filepath.Join(dir, "custom.md")
	store := meads.NewFileStore(file)

	g := &globals{Store: store, TasksFile: file}

	// Add a task via addCmd
	add := &addCmd{globals: g, Args: []string{"Test task"}}
	if err := add.Run(); err != nil {
		t.Fatalf("add: %v", err)
	}

	// Verify the custom file was created
	if _, err := os.Stat(file); err != nil {
		t.Fatalf("custom file not created: %v", err)
	}

	// List tasks via listCmd
	list := &listCmd{globals: g}
	if err := list.Run(); err != nil {
		t.Fatalf("list: %v", err)
	}

	// Get the task
	get := &getCmd{globals: g, IDs: []string{"1"}}
	if err := get.Run(); err != nil {
		t.Fatalf("get: %v", err)
	}

	// Update it
	update := &updateCmd{globals: g, ID: "1", Title: "Updated task"}
	if err := update.Run(); err != nil {
		t.Fatalf("update: %v", err)
	}

	// Set status
	ss := &setStatusCmd{globals: g, ID: "1", Status: "inprogress"}
	if err := ss.Run(); err != nil {
		t.Fatalf("set-status: %v", err)
	}

	// Ready (should be empty since task is inprogress)
	ready := &readyCmd{globals: g}
	if err := ready.Run(); err != nil {
		t.Fatalf("ready: %v", err)
	}

	// Add second task for dependency testing
	add2 := &addCmd{globals: g, Args: []string{"Second task"}}
	if err := add2.Run(); err != nil {
		t.Fatalf("add second: %v", err)
	}

	// Add dep
	dep := &addDepCmd{globals: g, Child: "2", Parent: "1"}
	if err := dep.Run(); err != nil {
		t.Fatalf("add-dep: %v", err)
	}

	// Delete first task
	del := &delCmd{globals: g, ID: "1"}
	if err := del.Run(); err != nil {
		t.Fatalf("del: %v", err)
	}

	// Verify the custom file has remaining task
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	content := string(data)
	if !contains(content, "Second task") {
		t.Errorf("custom file missing 'Second task':\n%s", content)
	}
	if contains(content, "Updated task") {
		t.Errorf("custom file still has deleted task:\n%s", content)
	}
}

func TestTasksFileWithWebhook(t *testing.T) {
	// Test that both features work together: custom file + webhook
	dir := t.TempDir()
	file := filepath.Join(dir, "tasks.md")
	store := meads.NewFileStore(file)

	var actions []string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var p webhookPayload
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &p)
		actions = append(actions, p.Action)
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	g := &globals{Store: store, TasksFile: file, WebhookURI: ts.URL}

	// Add
	add := &addCmd{globals: g, Args: []string{"Webhook test"}}
	if err := add.Run(); err != nil {
		t.Fatalf("add: %v", err)
	}

	// Update
	update := &updateCmd{globals: g, ID: "1", Priority: "P1"}
	if err := update.Run(); err != nil {
		t.Fatalf("update: %v", err)
	}

	// Set-status
	ss := &setStatusCmd{globals: g, ID: "1", Status: "closed"}
	if err := ss.Run(); err != nil {
		t.Fatalf("set-status: %v", err)
	}

	// Delete
	del := &delCmd{globals: g, ID: "1"}
	if err := del.Run(); err != nil {
		t.Fatalf("del: %v", err)
	}

	expected := []string{"add", "update", "update", "delete"}
	if len(actions) != len(expected) {
		t.Fatalf("actions = %v, want %v", actions, expected)
	}
	for i, want := range expected {
		if actions[i] != want {
			t.Errorf("actions[%d] = %q, want %q", i, actions[i], want)
		}
	}
}

func TestDeleteWebhookSendsFullTask(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "tasks.md")
	store := meads.NewFileStore(file)

	var last webhookPayload
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &last)
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	g := &globals{Store: store, TasksFile: file, WebhookURI: ts.URL}
	add := &addCmd{globals: g, Args: []string{"Doomed task"}}
	if err := add.Run(); err != nil {
		t.Fatalf("add: %v", err)
	}
	ss := &setStatusCmd{globals: g, ID: "1", Status: "inprogress"}
	if err := ss.Run(); err != nil {
		t.Fatalf("set-status: %v", err)
	}
	del := &delCmd{globals: g, ID: "1"}
	if err := del.Run(); err != nil {
		t.Fatalf("del: %v", err)
	}

	if last.Action != "delete" {
		t.Fatalf("action = %q, want delete", last.Action)
	}
	data, ok := last.Data.(map[string]any)
	if !ok {
		t.Fatalf("data type = %T, want map[string]any", last.Data)
	}
	if id, _ := data["id"].(float64); id != 1 {
		t.Errorf("data.id = %v, want 1", data["id"])
	}
	if title, _ := data["title"].(string); title != "Doomed task" {
		t.Errorf("data.title = %q, want %q", title, "Doomed task")
	}
	if status, _ := data["status"].(string); status != "inprogress" {
		t.Errorf("data.status = %q, want inprogress", status)
	}
	if deleted, _ := data["deleted"].(bool); !deleted {
		t.Errorf("data.deleted = %v, want true", data["deleted"])
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsSubstr(s, substr))
}

func containsSubstr(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// TestPostWebhook_GitModeAnchorsFileAtRepoRoot is task 83: in git mode the
// webhook's file field is a phantom (there is no tasks file), and it must
// name the REPOSITORY ROOT rather than the invocation's cwd.
//
// Consumers scope events by that path's directory - rais accepts an event
// only when filepath.Dir(file) is the terminal's working directory or an
// ancestor of it. refs/meads/* is repo-wide, so md run from a subdirectory
// is still the same store; anchoring at g.Dir would have named the
// subdirectory, which governs nothing, and the event would have been
// silently dropped.
func TestPostWebhook_GitModeAnchorsFileAtRepoRoot(t *testing.T) {
	var received webhookPayload
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &received)
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	h := gitModeHarness(t)
	sub := filepath.Join(h.dir, "internal", "deep")
	if err := os.MkdirAll(sub, 0755); err != nil {
		t.Fatal(err)
	}
	// Run from the subdirectory, exactly as an agent cd'd into a package
	// would.
	t.Chdir(sub)
	h.globals.Dir = sub
	h.globals.Git = &meads.ExecGit{Dir: sub}
	h.globals.WebhookURI = ts.URL

	if err := (&addCmd{globals: h.globals, Args: []string{"added from a subdirectory"}}).Run(); err != nil {
		t.Fatalf("add: %v", err)
	}

	root, err := filepath.EvalSymlinks(h.dir)
	if err != nil {
		t.Fatal(err)
	}
	gotDir, err := filepath.EvalSymlinks(filepath.Dir(received.File))
	if err != nil {
		t.Fatalf("resolving webhook file dir %q: %v", received.File, err)
	}
	if gotDir != root {
		t.Errorf("webhook file = %q (dir %q), want it anchored at the repo root %q", received.File, gotDir, root)
	}
	if filepath.Base(received.File) != "TASKS.md" {
		t.Errorf("webhook file base = %q, want TASKS.md", filepath.Base(received.File))
	}
	// The phantom must stay a phantom: git mode writes no tasks file.
	if _, err := os.Stat(received.File); !os.IsNotExist(err) {
		t.Errorf("git mode should not have created %s (stat err = %v)", received.File, err)
	}
}
