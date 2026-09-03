package main

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/go-git/go-billy/v5/memfs"
	"github.com/jpillora/meads/pkg/meads"
)

const tasksFile = "TASKS.csv"

type request struct {
	Operation string          `json:"operation"`
	Tasks     []meads.Task    `json:"tasks"`
	ID        int             `json:"id,omitempty"`
	Parent    int             `json:"parent,omitempty"`
	Input     json.RawMessage `json:"input,omitempty"`
}

type response struct {
	OK    bool        `json:"ok"`
	Task  *meads.Task `json:"task,omitempty"`
	Error string      `json:"error,omitempty"`
}

// apply is the browser core's single, transport-neutral entry point. The
// caller supplies a snapshot of ref-backed task JSON; a memfs Store lets the
// real Meads implementation apply its defaults, timestamps and graph checks.
func apply(data []byte) []byte {
	var req request
	if err := json.Unmarshal(data, &req); err != nil {
		return marshalResponse(response{Error: "invalid request: " + err.Error()})
	}

	fs := memfs.New()
	f, err := fs.Create(tasksFile)
	if err != nil {
		return marshalResponse(response{Error: err.Error()})
	}
	if _, err := io.WriteString(f, meads.FormatCSV(meads.File{Tasks: req.Tasks})); err != nil {
		_ = f.Close()
		return marshalResponse(response{Error: err.Error()})
	}
	if err := f.Close(); err != nil {
		return marshalResponse(response{Error: err.Error()})
	}

	store := meads.NewStore(fs, tasksFile)
	var task meads.Task
	switch req.Operation {
	case "create":
		if err := json.Unmarshal(req.Input, &task); err != nil {
			return marshalResponse(response{Error: "invalid task: " + err.Error()})
		}
		if err := normaliseCreate(&task); err != nil {
			return marshalResponse(response{Error: err.Error()})
		}
		id, err := store.Add(task)
		if err != nil {
			return marshalResponse(response{Error: err.Error()})
		}
		task, err = readTask(store, id)
		if err != nil {
			return marshalResponse(response{Error: err.Error()})
		}
	case "update", "soft-delete":
		if req.ID <= 0 {
			return marshalResponse(response{Error: "task ID must be positive"})
		}
		candidate, err := taskFromSnapshot(req.Tasks, req.ID)
		if err != nil {
			return marshalResponse(response{Error: err.Error()})
		}
		if req.Operation == "soft-delete" {
			candidate.Deleted = true
		} else {
			var patch map[string]json.RawMessage
			if err := json.Unmarshal(req.Input, &patch); err != nil {
				return marshalResponse(response{Error: "invalid patch: " + err.Error()})
			}
			if err := applyPatch(&candidate, patch); err != nil {
				return marshalResponse(response{Error: err.Error()})
			}
		}
		err = store.Update(req.ID, func(current *meads.Task) { *current = candidate })
		if err != nil {
			return marshalResponse(response{Error: err.Error()})
		}
		task, err = readTask(store, req.ID)
		if err != nil {
			return marshalResponse(response{Error: err.Error()})
		}
	case "add-dependency", "remove-dependency":
		if req.ID <= 0 || req.Parent <= 0 {
			return marshalResponse(response{Error: "task and dependency IDs must be positive"})
		}
		err := store.Update(req.ID, func(current *meads.Task) {
			if req.Operation == "add-dependency" {
				current.AddDep(req.Parent)
				sort.Ints(current.DependsOn)
				current.SetDependsOn(dedupeInts(current.DependsOn))
			} else {
				current.RemoveDep(req.Parent)
			}
		})
		if err != nil {
			return marshalResponse(response{Error: err.Error()})
		}
		task, err = readTask(store, req.ID)
		if err != nil {
			return marshalResponse(response{Error: err.Error()})
		}
	default:
		return marshalResponse(response{Error: fmt.Sprintf("unknown operation %q", req.Operation)})
	}

	return marshalResponse(response{OK: true, Task: &task})
}

func normaliseCreate(task *meads.Task) error {
	if strings.TrimSpace(task.Title) == "" {
		return fmt.Errorf("title is required")
	}
	if task.Status == "" {
		task.SetStatus("open")
	} else if err := meads.ValidateStatus(task.Status); err != nil {
		return err
	} else {
		task.SetStatus(task.Status)
	}
	if task.Priority == "" {
		task.SetPriority("P2")
	} else {
		priority, err := meads.NormalizePriority(task.Priority)
		if err != nil {
			return err
		}
		task.SetPriority(priority)
	}
	if task.Type == "" {
		task.SetType("task")
	} else if err := meads.ValidateType(task.Type); err != nil {
		return err
	} else {
		task.SetType(task.Type)
	}
	tags, err := task.Tags.Normalize()
	if err != nil {
		return err
	}
	task.SetTags(tags)
	task.DependsOn = dedupeInts(task.DependsOn)
	sort.Ints(task.DependsOn)
	if len(task.DependsOn) > 0 {
		task.SetDependsOn(task.DependsOn)
	}
	return nil
}

func applyPatch(task *meads.Task, patch map[string]json.RawMessage) error {
	if raw, ok := patch["title"]; ok {
		if err := json.Unmarshal(raw, &task.Title); err != nil {
			return fmt.Errorf("invalid title: %w", err)
		}
		if strings.TrimSpace(task.Title) == "" {
			return fmt.Errorf("title is required")
		}
	}
	if raw, ok := patch["description"]; ok {
		if err := json.Unmarshal(raw, &task.Description); err != nil {
			return fmt.Errorf("invalid description: %w", err)
		}
	}
	if raw, ok := patch["status"]; ok {
		var value string
		if err := json.Unmarshal(raw, &value); err != nil {
			return fmt.Errorf("invalid status: %w", err)
		}
		if err := meads.ValidateStatus(value); err != nil {
			return err
		}
		task.SetStatus(value)
	}
	if raw, ok := patch["status_reason"]; ok {
		if err := json.Unmarshal(raw, &task.StatusReason); err != nil {
			return fmt.Errorf("invalid status reason: %w", err)
		}
	}
	if raw, ok := patch["priority"]; ok {
		var value string
		if err := json.Unmarshal(raw, &value); err != nil {
			return fmt.Errorf("invalid priority: %w", err)
		}
		value, err := meads.NormalizePriority(value)
		if err != nil {
			return err
		}
		task.SetPriority(value)
	}
	if raw, ok := patch["type"]; ok {
		var value string
		if err := json.Unmarshal(raw, &value); err != nil {
			return fmt.Errorf("invalid type: %w", err)
		}
		if err := meads.ValidateType(value); err != nil {
			return err
		}
		task.SetType(value)
	}
	if raw, ok := patch["tags"]; ok {
		var tags meads.Tags
		if err := json.Unmarshal(raw, &tags); err != nil {
			return fmt.Errorf("invalid tags: %w", err)
		}
		tags, err := tags.Normalize()
		if err != nil {
			return err
		}
		task.SetTags(tags)
	}
	if raw, ok := patch["depends_on"]; ok {
		var deps []int
		if err := json.Unmarshal(raw, &deps); err != nil {
			return fmt.Errorf("invalid dependencies: %w", err)
		}
		deps = dedupeInts(deps)
		sort.Ints(deps)
		task.SetDependsOn(deps)
	}
	if raw, ok := patch["agent_id"]; ok {
		if err := json.Unmarshal(raw, &task.AgentID); err != nil {
			return fmt.Errorf("invalid agent ID: %w", err)
		}
	}
	if raw, ok := patch["files_in_scope"]; ok {
		if err := json.Unmarshal(raw, &task.FilesInScope); err != nil {
			return fmt.Errorf("invalid files in scope: %w", err)
		}
	}
	return nil
}

func readTask(store *meads.Store, id int) (meads.Task, error) {
	f, err := store.FS().Open(store.Path())
	if err != nil {
		return meads.Task{}, err
	}
	defer f.Close()
	data, err := io.ReadAll(f)
	if err != nil {
		return meads.Task{}, err
	}
	for _, task := range meads.ParseCSV(string(data)).Tasks {
		if task.ID == id {
			return task, nil
		}
	}
	return meads.Task{}, fmt.Errorf("task %d not found after mutation", id)
}

func taskFromSnapshot(tasks []meads.Task, id int) (meads.Task, error) {
	for _, task := range tasks {
		if task.ID == id && !task.Deleted {
			return task, nil
		}
	}
	return meads.Task{}, fmt.Errorf("task %d not found", id)
}

func dedupeInts(values []int) []int {
	seen := make(map[int]bool, len(values))
	out := make([]int, 0, len(values))
	for _, value := range values {
		if !seen[value] {
			seen[value] = true
			out = append(out, value)
		}
	}
	return out
}

func marshalResponse(value response) []byte {
	data, err := json.Marshal(value)
	if err != nil {
		return []byte(`{"ok":false,"error":"could not encode response"}`)
	}
	return data
}
