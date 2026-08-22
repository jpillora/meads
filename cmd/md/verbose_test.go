package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jpillora/meads/pkg/meads"
)

func TestVerboseTasksShowsUserFacingActionsAndTimings(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "TASKS.md")
	var diagnostics bytes.Buffer
	g := &globals{
		Store:         meads.NewFileStore(path),
		TasksFile:     path,
		FileMode:      true,
		Verbose:       true,
		VerboseOutput: &diagnostics,
	}

	tasks, err := g.tasks()
	if err != nil {
		t.Fatalf("tasks: %v", err)
	}
	id, err := tasks.Add(meads.Task{Title: "trace me"})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := tasks.Update(id, func(task *meads.Task) { task.Title = "traced" }); err != nil {
		t.Fatalf("Update: %v", err)
	}
	if _, err := tasks.Get([]int{id}); err != nil {
		t.Fatalf("Get: %v", err)
	}

	out := diagnostics.String()
	for _, want := range []string{
		"resolve task store...",
		"using md task store at " + path,
		"resolve task store done in ",
		"add task...",
		"add task done in ",
		"update task 1...",
		"update task 1 done in ",
		"read task 1...",
		"read task 1 done in ",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("verbose output missing %q:\n%s", want, out)
		}
	}
}

func TestVerboseGitShowsFetchAndPushTimings(t *testing.T) {
	h := gitModeHarness(t)
	var diagnostics bytes.Buffer
	h.globals.Verbose = true
	h.globals.VerboseOutput = &diagnostics

	if err := (&addCmd{globals: h.globals, Args: []string{"verbose sync"}}).Run(); err != nil {
		t.Fatalf("add: %v", err)
	}

	out := diagnostics.String()
	for _, want := range []string{
		"add task...",
		"remote sync due:",
		"sync task refs with origin...",
		"git fetch origin...",
		"git fetch origin done in ",
		"git push --porcelain origin refs/meads/*:refs/meads/*...",
		"git push --porcelain origin refs/meads/*:refs/meads/* done in ",
		"sync task refs with origin done in ",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("verbose output missing %q:\n%s", want, out)
		}
	}
}

func TestVerboseGitRedactsURLCredentials(t *testing.T) {
	got := gitAction([]string{"clone", "https://secret-token@github.com/example/repo.git"})
	if strings.Contains(got, "secret-token") {
		t.Fatalf("gitAction leaked credentials: %s", got)
	}
	if want := "https://[redacted]@github.com/example/repo.git"; !strings.Contains(got, want) {
		t.Fatalf("gitAction = %q, want redacted URL %q", got, want)
	}
}

func TestIntegrationVerboseFlagKeepsJSONOnStdout(t *testing.T) {
	bin := buildMD(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "TASKS.md"), []byte("# Tasks\n"), 0644); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(bin, "-V", "list", "--json")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "MEADS_WEBHOOK_URI=")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("md -V list --json: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
	}

	var tasks []meads.Task
	if err := json.Unmarshal(stdout.Bytes(), &tasks); err != nil {
		t.Fatalf("stdout is not clean JSON: %v\n%s", err, stdout.String())
	}
	if strings.Contains(stdout.String(), "verbose") {
		t.Fatalf("verbose diagnostics leaked onto stdout: %s", stdout.String())
	}
	for _, want := range []string{
		"meads: verbose: command...",
		"meads: verbose: read all tasks...",
		"meads: verbose: command done in ",
	} {
		if !strings.Contains(stderr.String(), want) {
			t.Errorf("stderr missing %q:\n%s", want, stderr.String())
		}
	}

	help := exec.Command(bin, "--help")
	help.Dir = dir
	helpOut, _ := help.CombinedOutput()
	if !strings.Contains(string(helpOut), "--verbose, -V") {
		t.Errorf("root help does not advertise the global verbose flag:\n%s", helpOut)
	}
}
