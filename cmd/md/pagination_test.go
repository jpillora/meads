package main

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"github.com/jpillora/meads/pkg/meads"
)

func TestPagination_GitModeListAndReady(t *testing.T) {
	h := gitModeHarness(t)
	gs := meads.NewGitStore(h.globals.git())
	for _, task := range []meads.Task{
		{ID: 1, Title: "one", Status: "open", Priority: "P2"},
		{ID: 2, Title: "two", Status: "open", Priority: "P0"},
		{ID: 3, Title: "three", Status: "open", Priority: "P1"},
		{ID: 4, Title: "four", Status: "open", Priority: "P0"},
		{ID: 5, Title: "five", Status: "closed", Priority: "P0"},
	} {
		if err := gs.ImportTask(task); err != nil {
			t.Fatalf("import task %d: %v", task.ID, err)
		}
	}

	listOut, err := captureStdout(t, (&listCmd{globals: h.globals, JSON: true, Limit: 2, Offset: 1}).Run)
	if err != nil {
		t.Fatalf("list pagination: %v", err)
	}
	if got := outputTaskIDs(t, listOut); got != "2,3" {
		t.Errorf("list --limit=2 --offset=1 IDs = %s, want 2,3", got)
	}

	readyOut, err := captureStdout(t, (&readyCmd{globals: h.globals, JSON: true, Limit: 2, Offset: 1}).Run)
	if err != nil {
		t.Fatalf("ready pagination: %v", err)
	}
	if got := outputTaskIDs(t, readyOut); got != "4,3" {
		t.Errorf("ready --limit=2 --offset=1 IDs = %s, want 4,3", got)
	}
}

func TestPagination_AppliesAfterFilters(t *testing.T) {
	h := newHarness(t)
	for _, task := range []struct {
		title string
		tags  string
	}{
		{title: "one", tags: "other"},
		{title: "two", tags: "api"},
		{title: "three", tags: "api"},
		{title: "four", tags: "api"},
	} {
		if err := (&addCmd{globals: h.globals, Args: []string{task.title}, Tags: task.tags}).Run(); err != nil {
			t.Fatalf("add %s: %v", task.title, err)
		}
	}

	listOut, err := captureStdout(t, (&listCmd{globals: h.globals, JSON: true, Limit: 1, Offset: 1, Tag: "api"}).Run)
	if err != nil {
		t.Fatalf("filtered list pagination: %v", err)
	}
	if got := outputTaskIDs(t, listOut); got != "3" {
		t.Errorf("list --tag=api --limit=1 --offset=1 IDs = %s, want 3", got)
	}

	readyOut, err := captureStdout(t, (&readyCmd{globals: h.globals, JSON: true, Limit: 1, Offset: 1, Tag: "api"}).Run)
	if err != nil {
		t.Fatalf("filtered ready pagination: %v", err)
	}
	if got := outputTaskIDs(t, readyOut); got != "3" {
		t.Errorf("ready --tag=api --limit=1 --offset=1 IDs = %s, want 3", got)
	}
}

func TestPagination_ValidationAndBounds(t *testing.T) {
	tasks := []meads.Task{{ID: 1}, {ID: 2}}
	for _, tc := range []struct {
		name   string
		limit  int
		offset int
		want   string
	}{
		{name: "negative limit", limit: -1, want: "--limit must be non-negative"},
		{name: "negative offset", offset: -1, want: "--offset must be non-negative"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := paginateTasks(tasks, tc.limit, tc.offset)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("paginateTasks error = %v, want %q", err, tc.want)
			}
		})
	}
	got, err := paginateTasks(tasks, 10, 10)
	if err != nil {
		t.Fatalf("offset past end: %v", err)
	}
	if got == nil || len(got) != 0 {
		t.Fatalf("offset past end = %#v, want a non-nil empty page", got)
	}
}

func outputTaskIDs(t *testing.T, output string) string {
	t.Helper()
	var tasks []meads.Task
	if err := json.Unmarshal([]byte(output), &tasks); err != nil {
		t.Fatalf("decode task output %q: %v", output, err)
	}
	ids := make([]string, len(tasks))
	for i, task := range tasks {
		ids[i] = strconv.Itoa(task.ID)
	}
	return strings.Join(ids, ",")
}
