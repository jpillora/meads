package mcp

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jpillora/meads/pkg/meads"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// NewServer creates an MCP server exposing task management tools over
// store, which may be file-backed (meads.FileTasks) or ref-backed
// (meads.GitTasks) - every tool here maps cleanly onto the meads.Tasks
// interface, so nothing about this server is gated by which backend store
// is.
func NewServer(store meads.Tasks, version string) *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{
		Name:    "meads",
		Version: version,
	}, nil)

	// list_tasks - List all tasks
	mcp.AddTool(s, &mcp.Tool{
		Name:        "list_tasks",
		Description: "List all tasks",
	}, func(_ context.Context, _ *mcp.CallToolRequest, _ meads.ListTasksInput) (*mcp.CallToolResult, any, error) {
		tasks, err := store.Get(nil)
		if err != nil {
			return nil, nil, err
		}
		return nil, briefTasks(tasks), nil
	})

	// get_task - Get a specific task by ID
	mcp.AddTool(s, &mcp.Tool{
		Name:        "get_task",
		Description: "Get a specific task by ID",
	}, func(_ context.Context, _ *mcp.CallToolRequest, input meads.GetTaskInput) (*mcp.CallToolResult, any, error) {
		tasks, err := store.Get([]int{input.ID})
		if err != nil {
			return nil, nil, err
		}
		return nil, tasks[0], nil
	})

	// ready_tasks - List open tasks not blocked by dependencies
	mcp.AddTool(s, &mcp.Tool{
		Name:        "ready_tasks",
		Description: "List open tasks not blocked by dependencies, sorted by priority",
	}, func(_ context.Context, _ *mcp.CallToolRequest, _ meads.ReadyTasksInput) (*mcp.CallToolResult, any, error) {
		tasks, err := store.Ready()
		if err != nil {
			return nil, nil, err
		}
		return nil, briefTasks(tasks), nil
	})

	// add_task - Add a new task
	mcp.AddTool(s, &mcp.Tool{
		Name:        "add_task",
		Description: "Add a new task",
	}, func(_ context.Context, _ *mcp.CallToolRequest, input meads.AddTaskInput) (*mcp.CallToolResult, any, error) {
		if input.Title == "" {
			return nil, nil, fmt.Errorf("title is required")
		}
		t := meads.Task{Title: input.Title}
		status := input.Status
		if status == "" {
			status = "open"
		}
		if err := meads.ValidateStatus(status); err != nil {
			return nil, nil, err
		}
		t.SetStatus(status)
		if input.Priority != "" {
			p, perr := meads.NormalizePriority(input.Priority)
			if perr != nil {
				return nil, nil, perr
			}
			t.SetPriority(p)
		}
		if input.Type != "" {
			if err := meads.ValidateType(input.Type); err != nil {
				return nil, nil, err
			}
			t.SetType(input.Type)
		}
		if input.Description != "" {
			t.Description = input.Description
		}
		id, err := store.Add(t)
		if err != nil {
			return nil, nil, err
		}
		return nil, meads.AddTaskOutput{ID: id}, nil
	})

	// update_task - Update an existing task
	mcp.AddTool(s, &mcp.Tool{
		Name:        "update_task",
		Description: "Update an existing task by ID",
	}, func(_ context.Context, _ *mcp.CallToolRequest, input meads.UpdateTaskInput) (*mcp.CallToolResult, any, error) {
		if input.Status != "" {
			if err := meads.ValidateStatus(input.Status); err != nil {
				return nil, nil, err
			}
		}
		if input.Type != "" {
			if err := meads.ValidateType(input.Type); err != nil {
				return nil, nil, err
			}
		}
		priority := input.Priority
		if priority != "" {
			var perr error
			priority, perr = meads.NormalizePriority(priority)
			if perr != nil {
				return nil, nil, perr
			}
		}
		err := store.Update(input.ID, func(t *meads.Task) {
			if input.Status != "" {
				t.SetStatus(input.Status)
			}
			if priority != "" {
				t.SetPriority(priority)
			}
			if input.Title != "" {
				t.Title = input.Title
			}
			if input.Type != "" {
				t.SetType(input.Type)
			}
			if input.Description != "" {
				t.Description = input.Description
			}
			if input.StatusReason != "" {
				t.StatusReason = input.StatusReason
			}
		})
		if err != nil {
			return nil, nil, err
		}
		return textResult("updated"), nil, nil
	})

	// delete_task - Delete a task by ID
	mcp.AddTool(s, &mcp.Tool{
		Name:        "delete_task",
		Description: "Delete a task by ID",
	}, func(_ context.Context, _ *mcp.CallToolRequest, input meads.DeleteTaskInput) (*mcp.CallToolResult, any, error) {
		if err := store.Delete(input.ID); err != nil {
			return nil, nil, err
		}
		return textResult("deleted"), nil, nil
	})

	// add_dependency - Add a dependency between tasks
	mcp.AddTool(s, &mcp.Tool{
		Name:        "add_dependency",
		Description: "Make a child task depend on a parent task",
	}, func(_ context.Context, _ *mcp.CallToolRequest, input meads.AddDependencyInput) (*mcp.CallToolResult, any, error) {
		err := store.Update(input.ChildID, func(t *meads.Task) {
			t.AddDep(input.ParentID)
		})
		if err != nil {
			return nil, nil, err
		}
		return textResult("dependency added"), nil, nil
	})

	// remove_dependency - Remove a dependency between tasks
	mcp.AddTool(s, &mcp.Tool{
		Name:        "remove_dependency",
		Description: "Remove a child task's dependency on a parent task",
	}, func(_ context.Context, _ *mcp.CallToolRequest, input meads.AddDependencyInput) (*mcp.CallToolResult, any, error) {
		err := store.Update(input.ChildID, func(t *meads.Task) {
			t.RemoveDep(input.ParentID)
		})
		if err != nil {
			return nil, nil, err
		}
		return textResult("dependency removed"), nil, nil
	})

	return s
}

// BriefTask is a lightweight task representation for list responses.
// It omits Description, Tags, Meta, and CloseReason to save tokens.
type BriefTask struct {
	ID           int    `json:"id"`
	Title        string `json:"title"`
	Status       string `json:"status"`
	Priority     string `json:"priority"`
	Type         string `json:"type"`
	DependsOn    []int  `json:"depends_on,omitempty"`
	StatusReason string `json:"status_reason,omitempty"`
}

func briefTasks(tasks []meads.Task) []BriefTask {
	out := make([]BriefTask, len(tasks))
	for i, t := range tasks {
		// Marshal/unmarshal to get normalized fields (defaults applied).
		raw, _ := json.Marshal(t)
		_ = json.Unmarshal(raw, &out[i])
	}
	return out
}

func textResult(msg string) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: msg}},
	}
}
