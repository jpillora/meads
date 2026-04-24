package meads

// Input/output types shared between transport layers (MCP, HTTP, etc).
// jsonschema tags are used by the MCP SDK; plain encoding/json ignores them.

type ListTasksInput struct{}

type GetTaskInput struct {
	ID int `json:"id" jsonschema:"task ID to retrieve,required"`
}

type ReadyTasksInput struct{}

type AddTaskInput struct {
	Title       string `json:"title" jsonschema:"task title,required"`
	Status      string `json:"status,omitempty" jsonschema:"task status (draft, open, inprogress, closed)"`
	Priority    string `json:"priority,omitempty" jsonschema:"task priority (P0-P9)"`
	Type        string `json:"type,omitempty" jsonschema:"task type (bug, task, feature, idea)"`
	Description string `json:"description,omitempty" jsonschema:"task description"`
}

type AddTaskOutput struct {
	ID int `json:"id"`
}

type UpdateTaskInput struct {
	ID           int    `json:"id" jsonschema:"task ID to update,required"`
	Status       string `json:"status,omitempty" jsonschema:"new status (draft, open, inprogress, closed)"`
	Priority     string `json:"priority,omitempty" jsonschema:"new priority (P0-P9)"`
	Title        string `json:"title,omitempty" jsonschema:"new title"`
	Description  string `json:"description,omitempty" jsonschema:"new description"`
	StatusReason string `json:"status_reason,omitempty" jsonschema:"reason for status change"`
}

type DeleteTaskInput struct {
	ID int `json:"id" jsonschema:"task ID to delete,required"`
}

type AddDependencyInput struct {
	ChildID  int `json:"child_id" jsonschema:"ID of the child (dependent) task,required"`
	ParentID int `json:"parent_id" jsonschema:"ID of the parent (dependency) task,required"`
}
