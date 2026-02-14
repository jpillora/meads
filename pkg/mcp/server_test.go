package mcp_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	mcppkg "github.com/jpillora/meads/pkg/mcp"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// setup creates a temp dir with an empty TASKS.md and returns an MCP client session.
func setup(t *testing.T) *mcp.ClientSession {
	t.Helper()
	dir := t.TempDir()
	file := filepath.Join(dir, "TASKS.md")
	if err := os.WriteFile(file, []byte(""), 0644); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	server := mcppkg.NewServer(file, "test")
	st, ct := mcp.NewInMemoryTransports()
	if _, err := server.Connect(ctx, st, nil); err != nil {
		t.Fatal(err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "test"}, nil)
	cs, err := client.Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cs.Close() })
	return cs
}

func TestListTools(t *testing.T) {
	cs := setup(t)
	ctx := context.Background()

	res, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}

	expected := map[string]bool{
		"list_tasks":     true,
		"get_task":       true,
		"ready_tasks":    true,
		"add_task":       true,
		"update_task":    true,
		"delete_task":    true,
		"add_dependency": true,
	}
	if len(res.Tools) != len(expected) {
		t.Fatalf("expected %d tools, got %d", len(expected), len(res.Tools))
	}
	for _, tool := range res.Tools {
		if !expected[tool.Name] {
			t.Errorf("unexpected tool: %s", tool.Name)
		}
	}
}

func TestAddAndGet(t *testing.T) {
	cs := setup(t)
	ctx := context.Background()

	// Add a task
	addRes := callTool(t, cs, ctx, "add_task", map[string]any{
		"title":    "Test task",
		"priority": "P1",
		"type":     "bug",
		"body":     "Some details",
	})
	var addOut struct{ ID int }
	unmarshalContent(t, addRes, &addOut)
	if addOut.ID == 0 {
		t.Fatal("expected non-zero task ID")
	}

	// Get it back
	getRes := callTool(t, cs, ctx, "get_task", map[string]any{
		"id": addOut.ID,
	})
	var task struct {
		ID       int    `json:"id"`
		Title    string `json:"title"`
		Status   string `json:"status"`
		Priority string `json:"priority"`
		Type     string `json:"type"`
		Body     string `json:"body"`
	}
	unmarshalContent(t, getRes, &task)
	if task.Title != "Test task" {
		t.Errorf("expected title 'Test task', got %q", task.Title)
	}
	if task.Priority != "P1" {
		t.Errorf("expected priority 'P1', got %q", task.Priority)
	}
	if task.Type != "bug" {
		t.Errorf("expected type 'bug', got %q", task.Type)
	}
	if task.Status != "open" {
		t.Errorf("expected status 'open', got %q", task.Status)
	}
	if task.Body != "Some details" {
		t.Errorf("expected body 'Some details', got %q", task.Body)
	}
}

func TestListTasks(t *testing.T) {
	cs := setup(t)
	ctx := context.Background()

	// Add 2 tasks
	callTool(t, cs, ctx, "add_task", map[string]any{"title": "Task A"})
	callTool(t, cs, ctx, "add_task", map[string]any{"title": "Task B"})

	// List all
	listRes := callTool(t, cs, ctx, "list_tasks", map[string]any{})
	var tasks []struct {
		Title string `json:"title"`
	}
	unmarshalContent(t, listRes, &tasks)
	if len(tasks) != 2 {
		t.Fatalf("expected 2 tasks, got %d", len(tasks))
	}
}

func TestReadyTasks(t *testing.T) {
	cs := setup(t)
	ctx := context.Background()

	// Add 2 tasks, second depends on first
	res1 := callTool(t, cs, ctx, "add_task", map[string]any{"title": "Parent"})
	var out1 struct{ ID int }
	unmarshalContent(t, res1, &out1)

	res2 := callTool(t, cs, ctx, "add_task", map[string]any{"title": "Child"})
	var out2 struct{ ID int }
	unmarshalContent(t, res2, &out2)

	// Add dependency: child depends on parent
	callTool(t, cs, ctx, "add_dependency", map[string]any{
		"child_id":  out2.ID,
		"parent_id": out1.ID,
	})

	// Only parent should be ready (child is blocked)
	readyRes := callTool(t, cs, ctx, "ready_tasks", map[string]any{})
	var ready []struct {
		ID    int    `json:"id"`
		Title string `json:"title"`
	}
	unmarshalContent(t, readyRes, &ready)
	if len(ready) != 1 {
		t.Fatalf("expected 1 ready task, got %d", len(ready))
	}
	if ready[0].Title != "Parent" {
		t.Errorf("expected ready task 'Parent', got %q", ready[0].Title)
	}
}

func TestUpdateTask(t *testing.T) {
	cs := setup(t)
	ctx := context.Background()

	// Add a task
	addRes := callTool(t, cs, ctx, "add_task", map[string]any{"title": "Update me"})
	var addOut struct{ ID int }
	unmarshalContent(t, addRes, &addOut)

	// Update status
	callTool(t, cs, ctx, "update_task", map[string]any{
		"id":     addOut.ID,
		"status": "closed",
	})

	// Verify
	getRes := callTool(t, cs, ctx, "get_task", map[string]any{"id": addOut.ID})
	var task struct {
		Status string `json:"status"`
	}
	unmarshalContent(t, getRes, &task)
	if task.Status != "closed" {
		t.Errorf("expected status 'closed', got %q", task.Status)
	}
}

func TestDeleteTask(t *testing.T) {
	cs := setup(t)
	ctx := context.Background()

	// Add a task
	addRes := callTool(t, cs, ctx, "add_task", map[string]any{"title": "Delete me"})
	var addOut struct{ ID int }
	unmarshalContent(t, addRes, &addOut)

	// Delete it
	callTool(t, cs, ctx, "delete_task", map[string]any{"id": addOut.ID})

	// Verify it's gone
	res, err := cs.CallTool(ctx, &mcp.CallToolParams{
		Name:      "get_task",
		Arguments: map[string]any{"id": addOut.ID},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Fatal("expected error for deleted task")
	}
}

func TestAddDependency(t *testing.T) {
	cs := setup(t)
	ctx := context.Background()

	// Add 2 tasks
	res1 := callTool(t, cs, ctx, "add_task", map[string]any{"title": "Parent"})
	var out1 struct{ ID int }
	unmarshalContent(t, res1, &out1)

	res2 := callTool(t, cs, ctx, "add_task", map[string]any{"title": "Child"})
	var out2 struct{ ID int }
	unmarshalContent(t, res2, &out2)

	// Add dependency
	callTool(t, cs, ctx, "add_dependency", map[string]any{
		"child_id":  out2.ID,
		"parent_id": out1.ID,
	})

	// Verify DependsOn
	getRes := callTool(t, cs, ctx, "get_task", map[string]any{"id": out2.ID})
	var task struct {
		DependsOn []int `json:"depends_on"`
	}
	unmarshalContent(t, getRes, &task)
	if len(task.DependsOn) != 1 || task.DependsOn[0] != out1.ID {
		t.Errorf("expected depends_on=[%d], got %v", out1.ID, task.DependsOn)
	}
}

func TestGetTaskNotFound(t *testing.T) {
	cs := setup(t)
	ctx := context.Background()

	res, err := cs.CallTool(ctx, &mcp.CallToolParams{
		Name:      "get_task",
		Arguments: map[string]any{"id": 999},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Fatal("expected error for non-existent task")
	}
}

// helpers

func callTool(t *testing.T, cs *mcp.ClientSession, ctx context.Context, name string, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	res, err := cs.CallTool(ctx, &mcp.CallToolParams{
		Name:      name,
		Arguments: args,
	})
	if err != nil {
		t.Fatalf("CallTool %s: %v", name, err)
	}
	if res.IsError {
		text := ""
		if len(res.Content) > 0 {
			if tc, ok := res.Content[0].(*mcp.TextContent); ok {
				text = tc.Text
			}
		}
		t.Fatalf("CallTool %s returned error: %s", name, text)
	}
	return res
}

func unmarshalContent(t *testing.T, res *mcp.CallToolResult, v any) {
	t.Helper()
	if len(res.Content) == 0 {
		t.Fatal("empty content in result")
	}
	tc, ok := res.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("expected TextContent, got %T", res.Content[0])
	}
	if err := json.Unmarshal([]byte(tc.Text), v); err != nil {
		t.Fatalf("unmarshal content: %v\nraw: %s", err, tc.Text)
	}
}
