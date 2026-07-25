package mcp_test

import (
	"context"
	"os/exec"
	"testing"

	mcppkg "github.com/jpillora/meads/pkg/mcp"
	"github.com/jpillora/meads/pkg/meads"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Tests proving the MCP server works against a git-mode backend (task 66
// phase 9): every tool maps cleanly onto meads.TaskStore's five methods, so
// - unlike the file-mode tests above - nothing here is gated. See
// cmd/md/mcp.go for the equivalent CLI-level wiring (mcpCmd.store).

// gitTaskStoreForTest adapts *meads.GitStore to meads.TaskStore, mirroring
// cmd/md/taskstore.go's gitTaskStore (package main, so not importable from
// this external test package). See that type's doc comment for why the
// shapes need a thin adapter rather than reusing GitStore's Create/Update/
// SoftDelete directly.
type gitTaskStoreForTest struct {
	gs *meads.GitStore
}

func (a gitTaskStoreForTest) Get(ids []int) ([]meads.Task, error) { return a.gs.Get(ids) }
func (a gitTaskStoreForTest) Ready() ([]meads.Task, error)        { return a.gs.Ready() }

func (a gitTaskStoreForTest) Add(t meads.Task) (int, error) {
	created, err := a.gs.Create(t)
	if err != nil {
		return 0, err
	}
	return created.ID, nil
}

func (a gitTaskStoreForTest) Update(id int, fn func(*meads.Task)) error {
	_, err := a.gs.Update(id, func(t *meads.Task) (bool, error) {
		fn(t)
		return true, nil
	})
	return err
}

func (a gitTaskStoreForTest) Delete(id int) error {
	_, err := a.gs.SoftDelete(id)
	return err
}

// setupGit creates a real temporary git repository (t.TempDir()) and an MCP
// client session backed by GitStore through gitTaskStoreForTest, mirroring
// setup() above but over git mode instead of an in-memory file store.
func setupGit(t *testing.T) *mcp.ClientSession {
	t.Helper()
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"},
		{"config", "user.name", "Test"},
		{"config", "user.email", "test@test.com"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	store := gitTaskStoreForTest{gs: meads.NewGitStore(&meads.ExecGit{Dir: dir})}

	ctx := context.Background()
	server := mcppkg.NewServer(store, "test")
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

func TestGitMode_AddGetUpdateDeleteRoundTrip(t *testing.T) {
	cs := setupGit(t)
	ctx := context.Background()

	addRes := callTool(t, cs, ctx, "add_task", map[string]any{"title": "Git task", "priority": "P1", "type": "feature"})
	var addOut struct{ ID int }
	unmarshalContent(t, addRes, &addOut)
	if addOut.ID == 0 {
		t.Fatal("expected non-zero task ID")
	}

	callTool(t, cs, ctx, "update_task", map[string]any{"id": addOut.ID, "status": "inprogress"})

	getRes := callTool(t, cs, ctx, "get_task", map[string]any{"id": addOut.ID})
	var task struct {
		Status   string `json:"status"`
		Priority string `json:"priority"`
		Type     string `json:"type"`
	}
	unmarshalContent(t, getRes, &task)
	if task.Status != "inprogress" || task.Priority != "P1" || task.Type != "feature" {
		t.Errorf("task after update = %+v", task)
	}

	callTool(t, cs, ctx, "delete_task", map[string]any{"id": addOut.ID})
	res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "get_task", Arguments: map[string]any{"id": addOut.ID}})
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Fatal("expected error getting a deleted git-mode task")
	}
}

func TestGitMode_ReadyRespectsDependencies(t *testing.T) {
	cs := setupGit(t)
	ctx := context.Background()

	res1 := callTool(t, cs, ctx, "add_task", map[string]any{"title": "Parent"})
	var out1 struct{ ID int }
	unmarshalContent(t, res1, &out1)

	res2 := callTool(t, cs, ctx, "add_task", map[string]any{"title": "Child"})
	var out2 struct{ ID int }
	unmarshalContent(t, res2, &out2)

	callTool(t, cs, ctx, "add_dependency", map[string]any{"child_id": out2.ID, "parent_id": out1.ID})

	readyRes := callTool(t, cs, ctx, "ready_tasks", map[string]any{})
	var ready []struct {
		ID int `json:"id"`
	}
	unmarshalContent(t, readyRes, &ready)
	if len(ready) != 1 || ready[0].ID != out1.ID {
		t.Fatalf("ready_tasks = %v, want only the unblocked parent %d", ready, out1.ID)
	}

	callTool(t, cs, ctx, "remove_dependency", map[string]any{"child_id": out2.ID, "parent_id": out1.ID})
	getRes := callTool(t, cs, ctx, "get_task", map[string]any{"id": out2.ID})
	var child struct {
		DependsOn []int `json:"depends_on"`
	}
	unmarshalContent(t, getRes, &child)
	if len(child.DependsOn) != 0 {
		t.Errorf("depends_on after remove_dependency = %v, want none", child.DependsOn)
	}
}

func TestGitMode_ListTasks(t *testing.T) {
	cs := setupGit(t)
	ctx := context.Background()

	callTool(t, cs, ctx, "add_task", map[string]any{"title": "A"})
	callTool(t, cs, ctx, "add_task", map[string]any{"title": "B"})

	listRes := callTool(t, cs, ctx, "list_tasks", map[string]any{})
	var tasks []struct {
		Title string `json:"title"`
	}
	unmarshalContent(t, listRes, &tasks)
	if len(tasks) != 2 {
		t.Fatalf("list_tasks in git mode = %v, want 2 tasks", tasks)
	}
}
