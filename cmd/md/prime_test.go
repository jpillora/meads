package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jpillora/meads/pkg/prime"
)

func TestPrimeCmd(t *testing.T) {
	t.Run("prints CLI context to stdout", func(t *testing.T) {
		out := capturePrimeStdout(t, func() error { return (&primeCmd{}).Run() })
		if !strings.Contains(out, "Essential Commands") {
			t.Fatalf("stdout missing CLI context:\n%s", out)
		}
		if strings.Contains(out, prime.BlockStart) {
			t.Fatal("plain stdout should not include block markers")
		}
	})

	t.Run("prints MCP context with --mcp", func(t *testing.T) {
		out := capturePrimeStdout(t, func() error { return (&primeCmd{MCP: true}).Run() })
		if !strings.Contains(out, "MCP Server") {
			t.Fatalf("stdout missing MCP context:\n%s", out)
		}
	})

	t.Run("--write creates a file with the CLI block", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "CLAUDE.md")
		out := capturePrimeStdout(t, func() error { return (&primeCmd{Write: path}).Run() })
		if !strings.Contains(out, "created") {
			t.Fatalf("expected a created message, got: %q", out)
		}
		got := readPrimeFile(t, path)
		if !strings.Contains(got, prime.BlockStart) || !strings.Contains(got, "Essential Commands") {
			t.Fatalf("file missing CLI block:\n%s", got)
		}
	})

	t.Run("--mcp --write selects MCP content", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "AGENTS.md")
		if err := (&primeCmd{MCP: true, Write: path}).Run(); err != nil {
			t.Fatal(err)
		}
		got := readPrimeFile(t, path)
		if !strings.Contains(got, "MCP Server") {
			t.Fatalf("file missing MCP content:\n%s", got)
		}
		if strings.Contains(got, "Essential Commands") {
			t.Fatal("file should not contain CLI-only content")
		}
	})

	t.Run("--write returns the WriteFile error", func(t *testing.T) {
		// A directory path makes the underlying read fail.
		if err := (&primeCmd{Write: t.TempDir()}).Run(); err == nil {
			t.Fatal("want error writing to a directory path, got nil")
		}
	})
}

func capturePrimeStdout(t *testing.T, fn func() error) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	runErr := fn()
	w.Close()
	os.Stdout = old
	var buf bytes.Buffer
	if _, err := io.Copy(&buf, r); err != nil {
		t.Fatal(err)
	}
	if runErr != nil {
		t.Fatal(runErr)
	}
	return buf.String()
}

func readPrimeFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
