package meads

import (
	"os"
	"strings"
	"testing"
)

func TestUpdate_Body(t *testing.T) {
	path := tempTaskFile(t, "")
	// Create a task to update.
	id, err := Add(path, Task{Title: "Test task", Status: "open"})
	if err != nil {
		t.Fatalf("Add failed: %v", err)
	}
	// Update with a simple body.
	err = Update(path, id, func(t *Task) {
		t.Body = "simple body"
	})
	if err != nil {
		t.Fatalf("Update failed: %v", err)
	}
	tasks, err := Get(path, []int{id})
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if tasks[0].Body != "simple body" {
		t.Errorf("Body = %q, want %q", tasks[0].Body, "simple body")
	}
}

func TestUpdate_MultilineBody(t *testing.T) {
	path := tempTaskFile(t, "")
	id, err := Add(path, Task{Title: "Crash report", Status: "open"})
	if err != nil {
		t.Fatalf("Add failed: %v", err)
	}
	multilineBody := `rais-control-prod crashed with: panic: reflect: call of reflect.Value.Set on zero Value.

Stack trace:
- reflect.Value.Set (reflect/value.go:2126)
- encoding/json/v2.makeMapArshaler.func2 (arshal_default.go:820)
- encoding/json/v2.makeDefaultArshaler.makeStructArshaler.func6 (arshal_default.go:1142)

A nil map value inside the state struct causes reflect.Value.Set on a zero value.`

	err = Update(path, id, func(t *Task) {
		t.Body = multilineBody
	})
	if err != nil {
		t.Fatalf("Update failed: %v", err)
	}
	// Read back and verify the multiline body survives the round-trip.
	tasks, err := Get(path, []int{id})
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if tasks[0].Body != multilineBody {
		t.Errorf("Body round-trip failed.\ngot:\n%s\nwant:\n%s", tasks[0].Body, multilineBody)
	}
	// Also verify the raw file contains the body text.
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile failed: %v", err)
	}
	if !strings.Contains(string(data), "Stack trace:") {
		t.Error("raw file does not contain multiline body content")
	}
}

func TestUpdate_BodyReplace(t *testing.T) {
	path := tempTaskFile(t, "")
	id, err := Add(path, Task{Title: "Task with body", Status: "open", Body: "original body"})
	if err != nil {
		t.Fatalf("Add failed: %v", err)
	}
	// Replace the body via update.
	err = Update(path, id, func(t *Task) {
		t.Body = "replaced body"
	})
	if err != nil {
		t.Fatalf("Update failed: %v", err)
	}
	tasks, err := Get(path, []int{id})
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if tasks[0].Body != "replaced body" {
		t.Errorf("Body = %q, want %q", tasks[0].Body, "replaced body")
	}
}
