package meads

import (
	"os"
	"strings"
	"testing"
)

func TestUpdate_Description(t *testing.T) {
	path := tempTaskFile(t, "")
	// Create a task to update.
	id, err := Add(path, Task{Title: "Test task", Status: "open"})
	if err != nil {
		t.Fatalf("Add failed: %v", err)
	}
	// Update with a simple description.
	err = Update(path, id, func(t *Task) {
		t.Description = "simple description"
	})
	if err != nil {
		t.Fatalf("Update failed: %v", err)
	}
	tasks, err := Get(path, []int{id})
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if tasks[0].Description != "simple description" {
		t.Errorf("Description = %q, want %q", tasks[0].Description, "simple description")
	}
}

func TestUpdate_MultilineDescription(t *testing.T) {
	path := tempTaskFile(t, "")
	id, err := Add(path, Task{Title: "Crash report", Status: "open"})
	if err != nil {
		t.Fatalf("Add failed: %v", err)
	}
	multilineDesc := `rais-control-prod crashed with: panic: reflect: call of reflect.Value.Set on zero Value.

Stack trace:
- reflect.Value.Set (reflect/value.go:2126)
- encoding/json/v2.makeMapArshaler.func2 (arshal_default.go:820)
- encoding/json/v2.makeDefaultArshaler.makeStructArshaler.func6 (arshal_default.go:1142)

A nil map value inside the state struct causes reflect.Value.Set on a zero value.`

	err = Update(path, id, func(t *Task) {
		t.Description = multilineDesc
	})
	if err != nil {
		t.Fatalf("Update failed: %v", err)
	}
	// Read back and verify the multiline description survives the round-trip.
	tasks, err := Get(path, []int{id})
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if tasks[0].Description != multilineDesc {
		t.Errorf("Description round-trip failed.\ngot:\n%s\nwant:\n%s", tasks[0].Description, multilineDesc)
	}
	// Also verify the raw file contains the description text.
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile failed: %v", err)
	}
	if !strings.Contains(string(data), "Stack trace:") {
		t.Error("raw file does not contain multiline description content")
	}
}

func TestUpdate_DescriptionReplace(t *testing.T) {
	path := tempTaskFile(t, "")
	id, err := Add(path, Task{Title: "Task with description", Status: "open", Description: "original description"})
	if err != nil {
		t.Fatalf("Add failed: %v", err)
	}
	// Replace the description via update.
	err = Update(path, id, func(t *Task) {
		t.Description = "replaced description"
	})
	if err != nil {
		t.Fatalf("Update failed: %v", err)
	}
	tasks, err := Get(path, []int{id})
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if tasks[0].Description != "replaced description" {
		t.Errorf("Description = %q, want %q", tasks[0].Description, "replaced description")
	}
}
