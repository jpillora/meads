package main

import (
	"strings"
	"testing"

	"github.com/jpillora/opts"
)

// Tests for optsFailureText (main.go): the three-way split that lets main
// filter hidden commands out of whatever opts would otherwise print and
// os.Exit(1) on internally (see main()'s doc comment on why it calls
// ParseArgsError instead of Parse/ParseArgs). These exercise the REAL
// jpillora/opts library end to end (not a hand-rolled fake of its error
// types, which are unexported anyway - see optsFailureText's doc comment),
// using a small dedicated struct so this test doesn't depend on md's actual
// command set ever staying the same shape.

type failureTextLeafCmd struct {
	Flag bool `help:"a leaf flag"`
}

func (c *failureTextLeafCmd) Run() error { return nil }

type failureTextRoot struct {
	Leaf failureTextLeafCmd `opts:"mode=cmd" help:"a leaf command"`
}

// parseFailureText runs opts against a fresh failureTextRoot with args (as
// the trailing os.Args a real invocation would have, e.g. []string{"--help"}
// - "prog" is prepended automatically, matching os.Args[0]'s role) and
// returns optsFailureText's result along with the raw (p, err) pair for
// tests that need to inspect them directly.
//
// It deliberately passes the package's real `version` var to .Version(),
// exactly like main() does - optsFailureText's --version/-v detection
// compares err.Error() against that specific global (see its doc comment),
// so a test build tagged with a different version string here would wrongly
// exercise the generic-error fallback instead of the version-string case.
func parseFailureText(t *testing.T, args ...string) (text string, p opts.ParsedOpts, err error) {
	t.Helper()
	c := failureTextRoot{}
	p, err = opts.New(&c).Name("md").Version(version).Summary("test summary").ParseArgsError(append([]string{"prog"}, args...))
	if err == nil {
		t.Fatalf("args %v: expected a non-nil error (an exit-worthy condition), got nil", args)
	}
	return optsFailureText(p, err), p, err
}

func TestOptsFailureText(t *testing.T) {
	t.Run("--help at root: verbatim exitError text, not p.Help() recomputed", func(t *testing.T) {
		text, p, err := parseFailureText(t, "--help")
		if !strings.Contains(text, "Usage:") {
			t.Fatalf("text = %q, want it to contain \"Usage:\"", text)
		}
		if text != err.Error() {
			t.Errorf("text = %q, want exactly err.Error() = %q", text, err.Error())
		}
		if text != p.Help() {
			t.Errorf("text differs from p.Help() even though nothing should have changed between the two calls:\ntext=%q\np.Help()=%q", text, p.Help())
		}
	})

	t.Run("-h on a leaf subcommand: the LEAF's help, not root's", func(t *testing.T) {
		text, p, err := parseFailureText(t, "leaf", "-h")
		if !strings.Contains(text, "Usage: md leaf") {
			t.Fatalf("text = %q, want it to contain \"Usage: md leaf\" (the leaf's own usage line)", text)
		}
		if text != err.Error() {
			t.Errorf("text = %q, want exactly err.Error()", text)
		}
		// p (returned by ParseArgsError) is always the ROOT node, so
		// p.Help() would wrongly be root's listing, not the leaf's - proving
		// optsFailureText must use err.Error() here, never p.Help().
		if text == p.Help() {
			t.Error("text unexpectedly equals root's p.Help() - the leaf-specific help was lost")
		}
	})

	t.Run("--version: bare version string, not a help dump", func(t *testing.T) {
		text, _, _ := parseFailureText(t, "--version")
		if text != version {
			t.Errorf("text = %q, want exactly the version string %q", text, version)
		}
	})

	t.Run("-v: same as --version", func(t *testing.T) {
		text, _, _ := parseFailureText(t, "-v")
		if text != version {
			t.Errorf("text = %q, want exactly the version string %q", text, version)
		}
	})

	t.Run("unknown top-level command: root's Help() with the error embedded", func(t *testing.T) {
		text, p, err := parseFailureText(t, "bogus")
		if err.Error() == text {
			t.Errorf("text should NOT be the bare err.Error() (%q) for a generic error - it must be expanded into full help", err.Error())
		}
		if !strings.Contains(text, "Usage:") {
			t.Errorf("text = %q, want it to contain \"Usage:\" (root's rendered help)", text)
		}
		if !strings.Contains(text, err.Error()) {
			t.Errorf("text = %q, want it to embed the original error message %q", text, err.Error())
		}
		if text != p.Help() {
			t.Errorf("text should equal p.Help() (opts' own generic-error fallback) once the error has been embedded")
		}
	})

	t.Run("unknown flag on a leaf subcommand: the LEAF's help, with its own error embedded", func(t *testing.T) {
		text, _, err := parseFailureText(t, "leaf", "--nope")
		if !strings.Contains(text, "Usage: md leaf") {
			t.Fatalf("text = %q, want the leaf's own usage line", text)
		}
		if text != err.Error() {
			t.Errorf("text = %q, want exactly err.Error() (opts already embeds the error into the leaf's own exitError text)", text)
		}
	})
}
