package mcp

import (
	"context"
	"fmt"

	"github.com/jpillora/meads/pkg/meads"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// NewServer creates an MCP server exposing task management tools.
// The file parameter is the path to the TASKS.md file.
func NewServer(file, version string) *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{
		Name:    "meads",
		Version: version,
	}, nil)

	// list_tasks - List all tasks
	mcp.AddTool(s, &mcp.Tool{
		Name:        "list_tasks",
		Description: "List all tasks",
	}, func(_ context.Context, _ *mcp.CallToolRequest, _ listTasksInput) (*mcp.CallToolResult, any, error) {
		tasks, err := meads.Get(file, nil)
		if err != nil {
			return nil, nil, err
		}
		return nil, tasks, nil
	})

	// get_task - Get a specific task by ID
	mcp.AddTool(s, &mcp.Tool{
		Name:        "get_task",
		Description: "Get a specific task by ID",
	}, func(_ context.Context, _ *mcp.CallToolRequest, input getTaskInput) (*mcp.CallToolResult, any, error) {
		tasks, err := meads.Get(file, []int{input.ID})
		if err != nil {
			return nil, nil, err
		}
		return nil, tasks[0], nil
	})

	// ready_tasks - List open tasks not blocked by dependencies
	mcp.AddTool(s, &mcp.Tool{
		Name:        "ready_tasks",
		Description: "List open tasks not blocked by dependencies, sorted by priority",
	}, func(_ context.Context, _ *mcp.CallToolRequest, _ readyTasksInput) (*mcp.CallToolResult, any, error) {
		tasks, err := meads.Ready(file)
		if err != nil {
			return nil, nil, err
		}
		return nil, tasks, nil
	})

	// add_task - Add a new task
	mcp.AddTool(s, &mcp.Tool{
		Name:        "add_task",
		Description: "Add a new task",
	}, func(_ context.Context, _ *mcp.CallToolRequest, input addTaskInput) (*mcp.CallToolResult, any, error) {
		if input.Title == "" {
			return nil, nil, fmt.Errorf("title is required")
		}
		t := meads.Task{Title: input.Title}
		status := input.Status
		if status == "" {
			status = "open"
		}
		t.SetStatus(status)
		if input.Priority != "" {
			t.SetPriority(input.Priority)
		}
		if input.Type != "" {
			t.SetType(input.Type)
		}
		if input.Body != "" {
			t.Body = input.Body
		}
		id, err := meads.Add(file, t)
		if err != nil {
			return nil, nil, err
		}
		return nil, addTaskOutput{ID: id}, nil
	})

	// update_task - Update an existing task
	mcp.AddTool(s, &mcp.Tool{
		Name:        "update_task",
		Description: "Update an existing task by ID",
	}, func(_ context.Context, _ *mcp.CallToolRequest, input updateTaskInput) (*mcp.CallToolResult, any, error) {
		err := meads.Update(file, input.ID, func(t *meads.Task) {
			if input.Status != "" {
				t.SetStatus(input.Status)
			}
			if input.Priority != "" {
				t.SetPriority(input.Priority)
			}
			if input.Title != "" {
				t.Title = input.Title
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
	}, func(_ context.Context, _ *mcp.CallToolRequest, input deleteTaskInput) (*mcp.CallToolResult, any, error) {
		if err := meads.Delete(file, input.ID); err != nil {
			return nil, nil, err
		}
		return textResult("deleted"), nil, nil
	})

	// add_dependency - Add a dependency between tasks
	mcp.AddTool(s, &mcp.Tool{
		Name:        "add_dependency",
		Description: "Make a child task depend on a parent task",
	}, func(_ context.Context, _ *mcp.CallToolRequest, input addDependencyInput) (*mcp.CallToolResult, any, error) {
		err := meads.Update(file, input.ChildID, func(t *meads.Task) {
			t.AddDep(input.ParentID)
		})
		if err != nil {
			return nil, nil, err
		}
		return textResult("dependency added"), nil, nil
	})

	return s
}

// Input/output types for tools

type listTasksInput struct{}

type getTaskInput struct {
	ID int `json:"id" jsonschema:"task ID to retrieve,required"`
}

type readyTasksInput struct{}

type addTaskInput struct {
	Title    string `json:"title" jsonschema:"task title,required"`
	Status   string `json:"status,omitempty" jsonschema:"task status (draft, open, inprogress, closed)"`
	Priority string `json:"priority,omitempty" jsonschema:"task priority (P0-P9)"`
	Type     string `json:"type,omitempty" jsonschema:"task type (bug, task, feature)"`
	Body     string `json:"body,omitempty" jsonschema:"task description body"`
}

type addTaskOutput struct {
	ID int `json:"id"`
}

type updateTaskInput struct {
	ID       int    `json:"id" jsonschema:"task ID to update,required"`
	Status   string `json:"status,omitempty" jsonschema:"new status (draft, open, inprogress, closed)"`
	Priority string `json:"priority,omitempty" jsonschema:"new priority (P0-P9)"`
	Title    string `json:"title,omitempty" jsonschema:"new title"`
}

type deleteTaskInput struct {
	ID int `json:"id" jsonschema:"task ID to delete,required"`
}

type addDependencyInput struct {
	ChildID  int `json:"child_id" jsonschema:"ID of the child (dependent) task,required"`
	ParentID int `json:"parent_id" jsonschema:"ID of the parent (dependency) task,required"`
}

func textResult(msg string) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: msg}},
	}
}
