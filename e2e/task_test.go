package e2e

import (
	"encoding/json"
	"testing"

	"github.com/jpillora/meads/pkg/meads"
)

func TestMarshalJSON_DefaultPriority(t *testing.T) {
	task := meads.Task{ID: 1, Title: "Test", Status: "open"}
	b, err := json.Marshal(task)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]interface{}
	json.Unmarshal(b, &m)
	if m["priority"] != "P2" {
		t.Errorf("priority = %v, want P2", m["priority"])
	}
}

func TestMarshalJSON_DefaultType(t *testing.T) {
	task := meads.Task{ID: 1, Title: "Test"}
	b, err := json.Marshal(task)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]interface{}
	json.Unmarshal(b, &m)
	if m["type"] != "task" {
		t.Errorf("type = %v, want task", m["type"])
	}
}

func TestMarshalJSON_ExcludesKnownMeta(t *testing.T) {
	task := meads.Task{
		ID:    1,
		Title: "Test",
		Meta: map[string]string{
			"status":   "open",
			"priority": "P1",
			"type":     "bug",
			"custom":   "value",
		},
	}
	b, err := json.Marshal(task)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]interface{}
	json.Unmarshal(b, &m)
	meta, ok := m["meta"].(map[string]interface{})
	if !ok {
		t.Fatal("meta not present or wrong type")
	}
	for _, key := range []string{"status", "priority", "type"} {
		if _, exists := meta[key]; exists {
			t.Errorf("meta should not contain known key %q", key)
		}
	}
	if meta["custom"] != "value" {
		t.Errorf("meta[custom] = %v, want %q", meta["custom"], "value")
	}
}

func TestMarshalJSON_NormalizePriority(t *testing.T) {
	task := meads.Task{ID: 1, Title: "Test", Priority: "p1"}
	b, err := json.Marshal(task)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]interface{}
	json.Unmarshal(b, &m)
	if m["priority"] != "P1" {
		t.Errorf("priority = %v, want P1", m["priority"])
	}
}

func TestMarshalJSON_NilMeta(t *testing.T) {
	task := meads.Task{ID: 1, Title: "Test", Meta: nil}
	b, err := json.Marshal(task)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]interface{}
	json.Unmarshal(b, &m)
	if _, ok := m["meta"]; ok {
		t.Error("expected meta to be omitted for nil Meta")
	}
}

func TestSetStatus(t *testing.T) {
	task := meads.Task{ID: 1}
	task.SetStatus("inprogress")
	if task.Status != "inprogress" {
		t.Errorf("Status = %q", task.Status)
	}
	if task.Meta["status"] != "inprogress" {
		t.Errorf("Meta[status] = %q", task.Meta["status"])
	}
}

func TestSetType(t *testing.T) {
	task := meads.Task{ID: 1}
	task.SetType("feature")
	if task.Type != "feature" {
		t.Errorf("Type = %q", task.Type)
	}
	if task.Meta["type"] != "feature" {
		t.Errorf("Meta[type] = %q", task.Meta["type"])
	}
}

func TestSetCloseReason(t *testing.T) {
	task := meads.Task{ID: 1}
	task.SetCloseReason("duplicate")
	if task.CloseReason != "duplicate" {
		t.Errorf("CloseReason = %q", task.CloseReason)
	}
	if task.Meta["close-reason"] != "duplicate" {
		t.Errorf("Meta[close-reason] = %q", task.Meta["close-reason"])
	}
}

func TestSetTags(t *testing.T) {
	task := meads.Task{ID: 1}
	task.SetTags([]string{"backend", "api"})
	if len(task.Tags) != 2 || task.Tags[0] != "backend" || task.Tags[1] != "api" {
		t.Errorf("Tags = %v", task.Tags)
	}
	if task.Meta["tags"] != "backend,api" {
		t.Errorf("Meta[tags] = %q", task.Meta["tags"])
	}
}

func TestSetDependsOn(t *testing.T) {
	task := meads.Task{ID: 3}
	task.SetDependsOn([]int{1, 2})
	if len(task.DependsOn) != 2 || task.DependsOn[0] != 1 || task.DependsOn[1] != 2 {
		t.Errorf("DependsOn = %v", task.DependsOn)
	}
	if task.Meta["depends-on"] != "1,2" {
		t.Errorf("Meta[depends-on] = %q", task.Meta["depends-on"])
	}
}

func TestAddDep_NoDuplicate(t *testing.T) {
	task := meads.Task{ID: 2}
	task.AddDep(1)
	task.AddDep(1)
	if len(task.DependsOn) != 1 {
		t.Errorf("expected 1 dep, got %d", len(task.DependsOn))
	}
}

func TestSetMeta_AllKeys(t *testing.T) {
	task := meads.Task{ID: 1}

	task.SetMeta("status", "closed")
	if task.Status != "closed" {
		t.Errorf("Status = %q", task.Status)
	}

	task.SetMeta("priority", "P0")
	if task.Priority != "P0" {
		t.Errorf("Priority = %q", task.Priority)
	}

	task.SetMeta("type", "bug")
	if task.Type != "bug" {
		t.Errorf("Type = %q", task.Type)
	}

	task.SetMeta("depends-on", "1,2")
	if len(task.DependsOn) != 2 || task.DependsOn[0] != 1 || task.DependsOn[1] != 2 {
		t.Errorf("DependsOn = %v", task.DependsOn)
	}

	task.SetMeta("close-reason", "wontfix")
	if task.CloseReason != "wontfix" {
		t.Errorf("CloseReason = %q", task.CloseReason)
	}

	task.SetMeta("tags", "a,b,c")
	if len(task.Tags) != 3 {
		t.Errorf("Tags = %v", task.Tags)
	}

	task.SetMeta("custom", "val")
	if task.Meta["custom"] != "val" {
		t.Errorf("Meta[custom] = %q", task.Meta["custom"])
	}
}

func TestNormalizePriority(t *testing.T) {
	tests := []struct {
		input string
		want  string
		err   bool
	}{
		{"P0", "P0", false},
		{"P9", "P9", false},
		{"p1", "P1", false},
		{"0", "P0", false},
		{"5", "P5", false},
		{" P3 ", "P3", false},
		{"P10", "", true},
		{"", "", true},
		{"banana", "", true},
		{"PP1", "", true},
		{"P", "", true},
		{"-1", "", true},
	}
	for _, tt := range tests {
		got, err := meads.NormalizePriority(tt.input)
		if (err != nil) != tt.err {
			t.Errorf("NormalizePriority(%q): err=%v, wantErr=%v", tt.input, err, tt.err)
			continue
		}
		if got != tt.want {
			t.Errorf("NormalizePriority(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}
