package prime

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const testContent = "# Prime\n\nhello world"

func TestWriteFile(t *testing.T) {
	t.Run("creates missing file", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "CLAUDE.md")
		action, err := WriteFile(path, testContent)
		if err != nil {
			t.Fatal(err)
		}
		if action != "created" {
			t.Fatalf("action = %q, want created", action)
		}
		got := readFile(t, path)
		if !strings.Contains(got, BlockStart) || !strings.Contains(got, BlockEnd) {
			t.Fatalf("missing markers:\n%s", got)
		}
		if !strings.Contains(got, "hello world") {
			t.Fatalf("missing content:\n%s", got)
		}
	})

	t.Run("appends to existing file without a block", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "AGENTS.md")
		writeFile(t, path, "# My Project\n\nExisting notes.\n")
		action, err := WriteFile(path, testContent)
		if err != nil {
			t.Fatal(err)
		}
		if action != "appended" {
			t.Fatalf("action = %q, want appended", action)
		}
		got := readFile(t, path)
		if !strings.HasPrefix(got, "# My Project\n\nExisting notes.\n") {
			t.Fatalf("clobbered existing content:\n%s", got)
		}
		if strings.Count(got, BlockStart) != 1 {
			t.Fatalf("want exactly one block, got %d:\n%s", strings.Count(got, BlockStart), got)
		}
	})

	t.Run("replaces existing block, preserving surrounding text", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "CLAUDE.md")
		before := "# Top\n\n"
		after := "\n## Footer\n\nkeep me\n"
		writeFile(t, path, before+BlockStart+"\nOLD BODY\n"+BlockEnd+after)
		action, err := WriteFile(path, testContent)
		if err != nil {
			t.Fatal(err)
		}
		if action != "updated" {
			t.Fatalf("action = %q, want updated", action)
		}
		got := readFile(t, path)
		if strings.Contains(got, "OLD BODY") {
			t.Fatalf("old body not replaced:\n%s", got)
		}
		if !strings.HasPrefix(got, before) {
			t.Fatalf("dropped leading text:\n%s", got)
		}
		if !strings.HasSuffix(got, after) {
			t.Fatalf("dropped trailing text:\n%s", got)
		}
		if !strings.Contains(got, "hello world") {
			t.Fatalf("missing new content:\n%s", got)
		}
		if strings.Count(got, BlockStart) != 1 {
			t.Fatalf("want exactly one block, got %d:\n%s", strings.Count(got, BlockStart), got)
		}
	})

	t.Run("idempotent rewrite reports unchanged", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "CLAUDE.md")
		if _, err := WriteFile(path, testContent); err != nil {
			t.Fatal(err)
		}
		first := readFile(t, path)
		action, err := WriteFile(path, testContent)
		if err != nil {
			t.Fatal(err)
		}
		if action != "unchanged" {
			t.Fatalf("action = %q, want unchanged", action)
		}
		if readFile(t, path) != first {
			t.Fatal("file changed on idempotent rewrite")
		}
	})

	t.Run("creates from a whitespace-only file", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "CLAUDE.md")
		writeFile(t, path, "   \n\n")
		action, err := WriteFile(path, testContent)
		if err != nil {
			t.Fatal(err)
		}
		if action != "created" {
			t.Fatalf("action = %q, want created", action)
		}
		got := readFile(t, path)
		if !strings.HasPrefix(got, BlockStart) || strings.Count(got, BlockStart) != 1 {
			t.Fatalf("want a single leading block, got:\n%q", got)
		}
	})

	t.Run("append inserts a blank-line separator when no trailing newline", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "AGENTS.md")
		writeFile(t, path, "no trailing newline")
		action, err := WriteFile(path, testContent)
		if err != nil {
			t.Fatal(err)
		}
		if action != "appended" {
			t.Fatalf("action = %q, want appended", action)
		}
		if got := readFile(t, path); !strings.HasPrefix(got, "no trailing newline\n\n"+BlockStart) {
			t.Fatalf("want blank-line separator before block, got:\n%q", got)
		}
	})

	t.Run("errors on a start marker without an end", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "CLAUDE.md")
		writeFile(t, path, "# Top\n\n"+BlockStart+"\ndangling body\n")
		if _, err := WriteFile(path, testContent); err == nil {
			t.Fatal("want error for dangling start marker, got nil")
		}
	})

	t.Run("errors when the path is a directory", func(t *testing.T) {
		if _, err := WriteFile(t.TempDir(), testContent); err == nil {
			t.Fatal("want error writing to a directory path, got nil")
		}
	})

	t.Run("errors creating into a missing parent directory", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "nope", "CLAUDE.md")
		if _, err := WriteFile(path, testContent); err == nil {
			t.Fatal("want error creating into missing dir, got nil")
		}
	})
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func writeFile(t *testing.T, path, s string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
}
