package main

import (
	"strconv"
	"strings"
	"testing"

	"github.com/jpillora/meads/pkg/meads"
)

// Tag support driven through the real commands, in both storage modes: the
// tag rule is enforced at these boundaries (nothing below them rejects a
// value - see meads.Tags), and `md list`/`md ready` filter on tags in
// either mode. Storage round-trips live in e2e/tags_test.go.

func TestTags_AddAndUpdate_FileMode(t *testing.T) {
	h := newHarness(t)
	g := h.globals

	// --tags normalizes: trimmed, lowercased, de-duplicated.
	if err := (&addCmd{globals: g, Args: []string{"Tagged task"}, Tags: " API , web-ui, api "}).Run(); err != nil {
		t.Fatalf("add --tags: %v", err)
	}
	if got := h.getTask(1).Tags.String(); got != "api,web-ui" {
		t.Fatalf("tags after add = %q, want api,web-ui", got)
	}
	// The file itself holds the CSV metadata value.
	if content := h.tasksFileContent(); !strings.Contains(content, "* tags: api,web-ui\n") {
		t.Fatalf("TASKS.md missing the tags line:\n%s", content)
	}

	// --add-tags keeps what is there; --rm-tags takes one away.
	if err := (&updateCmd{globals: g, ID: "1", AddTags: "docs,api"}).Run(); err != nil {
		t.Fatalf("update --add-tags: %v", err)
	}
	if got := h.getTask(1).Tags.String(); got != "api,web-ui,docs" {
		t.Fatalf("tags after --add-tags = %q, want api,web-ui,docs", got)
	}
	if err := (&updateCmd{globals: g, ID: "1", RmTags: "web-ui,never-set"}).Run(); err != nil {
		t.Fatalf("update --rm-tags: %v", err)
	}
	if got := h.getTask(1).Tags.String(); got != "api,docs" {
		t.Fatalf("tags after --rm-tags = %q, want api,docs", got)
	}

	// --tags replaces the whole set.
	if err := (&updateCmd{globals: g, ID: "1", Tags: tagsFlag{set: true, value: "solo"}}).Run(); err != nil {
		t.Fatalf("update --tags: %v", err)
	}
	if got := h.getTask(1).Tags.String(); got != "solo" {
		t.Fatalf("tags after --tags = %q, want solo", got)
	}

	// An empty --tags= clears them, and the file loses the line entirely.
	if err := (&updateCmd{globals: g, ID: "1", Tags: tagsFlag{set: true}}).Run(); err != nil {
		t.Fatalf("update --tags=: %v", err)
	}
	if got := h.getTask(1).Tags; len(got) != 0 {
		t.Fatalf("tags after --tags= = %v, want none", got)
	}
	if content := h.tasksFileContent(); strings.Contains(content, "tags") {
		t.Fatalf("TASKS.md still mentions tags after clearing:\n%s", content)
	}
}

func TestTags_InvalidRejected(t *testing.T) {
	h := newHarness(t)
	g := h.globals
	h.addTask("Existing")

	cases := []struct {
		name string
		run  func() error
	}{
		{"add --tags", (&addCmd{globals: g, Args: []string{"Bad"}, Tags: "web ui"}).Run},
		{"update --tags", (&updateCmd{globals: g, ID: "1", Tags: tagsFlag{set: true, value: "web/ui"}}).Run},
		{"update --add-tags", (&updateCmd{globals: g, ID: "1", AddTags: "web_ui"}).Run},
		{"update --rm-tags", (&updateCmd{globals: g, ID: "1", RmTags: "web ui"}).Run},
	}
	for _, c := range cases {
		err := c.run()
		if err == nil {
			t.Errorf("%s: expected an error for an invalid tag", c.name)
			continue
		}
		if !strings.Contains(err.Error(), "lowercase letters, numbers and dashes") {
			t.Errorf("%s: error = %v, want the tag rule spelled out", c.name, err)
		}
	}
	// The rejected add never created a task.
	if n := len(h.getTasks()); n != 1 {
		t.Errorf("task count = %d, want 1 (the rejected add must not persist)", n)
	}
	// --tags replaces, so mixing it with the incremental flags is refused
	// rather than silently resolved.
	err := (&updateCmd{globals: g, ID: "1", Tags: tagsFlag{set: true, value: "api"}, AddTags: "docs"}).Run()
	if err == nil || !strings.Contains(err.Error(), "cannot use --tags with") {
		t.Errorf("--tags with --add-tags: err = %v, want a conflict error", err)
	}
}

func TestTags_ListAndReadyFilters_FileMode(t *testing.T) {
	h := newHarness(t)
	g := h.globals

	if err := (&addCmd{globals: g, Args: []string{"Both"}, Tags: "api,backend"}).Run(); err != nil {
		t.Fatalf("add: %v", err)
	}
	if err := (&addCmd{globals: g, Args: []string{"One"}, Tags: "api"}).Run(); err != nil {
		t.Fatalf("add: %v", err)
	}
	if err := (&addCmd{globals: g, Args: []string{"None"}}).Run(); err != nil {
		t.Fatalf("add: %v", err)
	}

	list := &listCmd{globals: g}
	list.Tag = "api"
	if got := titles(list.filterTasks(h.getTasks())); got != "Both,One" {
		t.Errorf("list --tag=api = %q, want Both,One", got)
	}
	// A comma-separated filter requires ALL of the tags.
	list.Tag = "api,backend"
	if got := titles(list.filterTasks(h.getTasks())); got != "Both" {
		t.Errorf("list --tag=api,backend = %q, want Both", got)
	}
	list.Tag = "nonexistent"
	if got := titles(list.filterTasks(h.getTasks())); got != "" {
		t.Errorf("list --tag=nonexistent = %q, want nothing", got)
	}
	// Combined with another filter, both still apply.
	list.Tag = "api"
	list.Status = "closed"
	if got := titles(list.filterTasks(h.getTasks())); got != "" {
		t.Errorf("list --tag=api --status=closed = %q, want nothing", got)
	}

	out, err := captureStdout(t, (&readyCmd{globals: g, Tag: "api,backend"}).Run)
	if err != nil {
		t.Fatalf("ready --tag: %v", err)
	}
	if !strings.Contains(out, "Both") || strings.Contains(out, "One") || strings.Contains(out, "None") {
		t.Errorf("ready --tag=api,backend printed:\n%s\nwant only the Both task", out)
	}
}

// TestTags_GitMode drives the same surface against GitStore: tags live in
// the task's JSON blob there, with no tasks file involved at all.
func TestTags_GitMode(t *testing.T) {
	h := gitModeHarness(t)
	g := h.globals
	gs := meads.NewGitStore(g.git())

	if err := (&addCmd{globals: g, Args: []string{"Both"}, Tags: "API,backend"}).Run(); err != nil {
		t.Fatalf("add --tags: %v", err)
	}
	if err := (&addCmd{globals: g, Args: []string{"One"}, Tags: "api"}).Run(); err != nil {
		t.Fatalf("add --tags: %v", err)
	}
	all, err := gs.Get(nil)
	if err != nil || len(all) != 2 {
		t.Fatalf("after two adds: tasks=%v err=%v, want 2 tasks", all, err)
	}
	id := all[0].ID
	if got := all[0].Tags.String(); got != "api,backend" {
		t.Fatalf("tags in git mode = %q, want api,backend", got)
	}

	// Incremental edits survive the ref's read-modify-write cycle.
	if err := (&updateCmd{globals: g, ID: strconv.Itoa(id), AddTags: "docs"}).Run(); err != nil {
		t.Fatalf("update --add-tags: %v", err)
	}
	if err := (&updateCmd{globals: g, ID: strconv.Itoa(id), RmTags: "backend"}).Run(); err != nil {
		t.Fatalf("update --rm-tags: %v", err)
	}
	reread, err := gs.Get([]int{id})
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got := reread[0].Tags.String(); got != "api,docs" {
		t.Fatalf("tags after add/rm in git mode = %q, want api,docs", got)
	}

	// And the filters work off the same field in git mode.
	tasks, err := g.tasks()
	if err != nil {
		t.Fatalf("tasks: %v", err)
	}
	loaded, err := tasks.Get(nil)
	if err != nil {
		t.Fatalf("get all: %v", err)
	}
	list := &listCmd{globals: g}
	list.Tag = "docs"
	if got := titles(list.filterTasks(loaded)); got != "Both" {
		t.Errorf("list --tag=docs (git mode) = %q, want Both", got)
	}
	out, err := captureStdout(t, (&readyCmd{globals: g, Tag: "api"}).Run)
	if err != nil {
		t.Fatalf("ready --tag: %v", err)
	}
	if !strings.Contains(out, "Both") || !strings.Contains(out, "One") {
		t.Errorf("ready --tag=api (git mode) printed:\n%s\nwant both tagged tasks", out)
	}
	out, err = captureStdout(t, (&readyCmd{globals: g, Tag: "docs"}).Run)
	if err != nil {
		t.Fatalf("ready --tag: %v", err)
	}
	if !strings.Contains(out, "Both") || strings.Contains(out, "One") {
		t.Errorf("ready --tag=docs (git mode) printed:\n%s\nwant only the Both task", out)
	}
}

// titles joins task titles for compact comparisons.
func titles(tasks []meads.Task) string {
	out := make([]string, len(tasks))
	for i, t := range tasks {
		out[i] = t.Title
	}
	return strings.Join(out, ",")
}
