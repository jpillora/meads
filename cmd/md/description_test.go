package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func runMDWithStdin(t *testing.T, bin, dir, stdin string, args ...string) (string, error) {
	t.Helper()
	cmd := exec.Command(bin, args...)
	cmd.Dir = dir
	cmd.Stdin = strings.NewReader(stdin)
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	err := cmd.Run()
	return output.String(), err
}

// This exercises the compiled CLI rather than only calling Run directly:
// --description-file=- must survive option parsing and consume the process's
// stdin exactly as a shell HEREDOC supplies it.
func TestIntegration_DescriptionFromStdin(t *testing.T) {
	bin := buildMD(t)
	h := newHarness(t)
	tasksFlag := "--tasks-file=" + h.globals.TasksFile

	addDescription := "Add notes\n\nUse `code`, ${HOME}, and literal \\n text.\n"
	if out, err := runMDWithStdin(t, bin, h.dir, addDescription,
		tasksFlag, "add", "--title=From stdin", "--description-file=-"); err != nil {
		t.Fatalf("md add from stdin: %v\n%s", err, out)
	}
	if got := h.getTask(1).Description; got != strings.TrimRight(addDescription, "\r\n") {
		t.Fatalf("add description = %q, want literal stdin %q", got, strings.TrimRight(addDescription, "\r\n"))
	}

	updateDescription := "Updated notes\n\n- one\n- `two`\n"
	if out, err := runMDWithStdin(t, bin, h.dir, updateDescription,
		tasksFlag, "update", "1", "--description-file=-"); err != nil {
		t.Fatalf("md update from stdin: %v\n%s", err, out)
	}
	if got := h.getTask(1).Description; got != strings.TrimRight(updateDescription, "\r\n") {
		t.Fatalf("update description = %q, want %q", got, strings.TrimRight(updateDescription, "\r\n"))
	}
}

// Unlike --description, a description read from a file or from stdin is
// already real Markdown: it must not pass through the JSON-escape decoder, or
// a literal '\n' or a Windows path in the source would be silently rewritten.
func TestDescriptionSourceIsLiteral(t *testing.T) {
	const description = "literal \\n and \\t stay escaped\n"
	writeDescription := func(t *testing.T) string {
		t.Helper()
		path := filepath.Join(t.TempDir(), "description.md")
		if err := os.WriteFile(path, []byte(description), 0o644); err != nil {
			t.Fatal(err)
		}
		return path
	}

	t.Run("add file", func(t *testing.T) {
		h := newHarness(t)
		cmd := &addCmd{
			globals:         h.globals,
			Title:           "Literal add from file",
			DescriptionFile: writeDescription(t),
		}
		if err := cmd.Run(); err != nil {
			t.Fatal(err)
		}
		if got := h.getTask(1).Description; got != strings.TrimRight(description, "\r\n") {
			t.Fatalf("description = %q, want %q", got, strings.TrimRight(description, "\r\n"))
		}
	})

	t.Run("add stdin", func(t *testing.T) {
		h := newHarness(t)
		h.globals.Stdin = strings.NewReader(description)
		cmd := &addCmd{
			globals:         h.globals,
			Title:           "Literal add",
			DescriptionFile: "-",
		}
		if err := cmd.Run(); err != nil {
			t.Fatal(err)
		}
		if got := h.getTask(1).Description; got != strings.TrimRight(description, "\r\n") {
			t.Fatalf("description = %q, want %q", got, strings.TrimRight(description, "\r\n"))
		}
	})

	t.Run("update stdin", func(t *testing.T) {
		h := newHarness(t)
		id := h.addTask("Literal update")
		h.globals.Stdin = strings.NewReader(description)
		cmd := &updateCmd{globals: h.globals, ID: "1", DescriptionFile: "-"}
		if err := cmd.Run(); err != nil {
			t.Fatal(err)
		}
		if got := h.getTask(id).Description; got != strings.TrimRight(description, "\r\n") {
			t.Fatalf("description = %q, want %q", got, strings.TrimRight(description, "\r\n"))
		}
	})
}
