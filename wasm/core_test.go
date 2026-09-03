package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func runCore(t *testing.T, payload string) response {
	t.Helper()
	var got response
	if err := json.Unmarshal(apply([]byte(payload)), &got); err != nil {
		t.Fatal(err)
	}
	return got
}

func TestCreateUsesMeadsDefaultsAndNextID(t *testing.T) {
	got := runCore(t, `{"operation":"create","tasks":[{"id":7,"title":"old","deleted":true}],"input":{"title":"New","tags":["API","api"]}}`)
	if !got.OK || got.Task == nil {
		t.Fatalf("unexpected response: %+v", got)
	}
	if got.Task.ID != 8 || got.Task.Status != "open" || got.Task.Priority != "P2" || got.Task.Type != "task" {
		t.Fatalf("defaults/ID not applied: %+v", got.Task)
	}
	if len(got.Task.Tags) != 1 || got.Task.Tags[0] != "api" || got.Task.Meta["created"] == "" {
		t.Fatalf("normalisation/timestamp not applied: %+v", got.Task)
	}
}

func TestUpdateRejectsDependencyCycle(t *testing.T) {
	got := runCore(t, `{"operation":"add-dependency","id":1,"parent":2,"tasks":[{"id":1,"title":"one"},{"id":2,"title":"two","depends_on":[1]}]}`)
	if got.OK || !strings.Contains(got.Error, "circular dependency") {
		t.Fatalf("expected cycle error, got %+v", got)
	}
}

func TestUpdateNormalisesFieldsAndPreservesUnknownMeta(t *testing.T) {
	got := runCore(t, `{"operation":"update","id":1,"tasks":[{"id":1,"title":"one","meta":{"custom":"yes","created":"2026-01-01T00:00:00Z"}}],"input":{"priority":"3","tags":["Web","web"],"description":"next"}}`)
	if !got.OK || got.Task == nil {
		t.Fatalf("unexpected response: %+v", got)
	}
	if got.Task.Priority != "P3" || got.Task.Description != "next" || got.Task.Meta["custom"] != "yes" || got.Task.Meta["updated"] == "" {
		t.Fatalf("update semantics not applied: %+v", got.Task)
	}
}

func TestSoftDeleteReturnsGitModeTombstone(t *testing.T) {
	got := runCore(t, `{"operation":"soft-delete","id":1,"tasks":[{"id":1,"title":"one"}]}`)
	if !got.OK || got.Task == nil || !got.Task.Deleted || got.Task.Meta["updated"] == "" {
		t.Fatalf("soft delete was not canonicalized: %+v", got)
	}
}
