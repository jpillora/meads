package main

import (
	"os"
	"path/filepath"
	"testing"
)

// beadsGeneratedHook is a realistic beads-installed post-checkout hook: the
// "# bd-hooks-version" marker plus plain shell (var assignments, a bd
// invocation) that isEntirelyBeadsHook does NOT recognise as beads. Before the
// marker fast-path, nuke would "clean" this hook and leave it in place, still
// running `bd sync` on every checkout.
const beadsGeneratedHook = `#!/bin/sh
# bd-hooks-version: 0.30.7
#
if [ "$3" != "1" ]; then
    exit 0
fi

GIT_DIR=$(git rev-parse --git-dir 2>/dev/null)
GIT_COMMON_DIR=$(git rev-parse --git-common-dir 2>/dev/null)

if ! output=$(bd sync --import-only --no-git-history 2>&1); then
    echo "Warning: Failed to sync bd changes after checkout" >&2
fi
exit 0
`

func TestIsBeadsGeneratedHook(t *testing.T) {
	if !isBeadsGeneratedHook(beadsGeneratedHook) {
		t.Error("marked bd hook should be detected as beads-generated")
	}
	if isBeadsGeneratedHook("#!/bin/sh\necho hi\n") {
		t.Error("plain hook should not be detected as beads-generated")
	}
}

func TestRemoveBeadsHookRemovesGeneratedHook(t *testing.T) {
	// Premise: the line heuristic alone must NOT treat this hook as entirely
	// beads — otherwise this test wouldn't exercise the version-marker fix.
	if isEntirelyBeadsHook(beadsGeneratedHook) {
		t.Fatal("test premise invalid: heuristic already treats the hook as entirely beads")
	}

	hooksDir := t.TempDir()
	hookPath := filepath.Join(hooksDir, "post-checkout")
	if err := os.WriteFile(hookPath, []byte(beadsGeneratedHook), 0o755); err != nil {
		t.Fatal(err)
	}

	var errs []string
	(&nukeCmd{}).removeBeadsHook(hooksDir, "post-checkout", &errs)

	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if _, err := os.Stat(hookPath); !os.IsNotExist(err) {
		t.Fatalf("beads-generated hook should be removed, but it remains (stat err=%v)", err)
	}
}

func TestRemoveBeadsHookLeavesForeignHook(t *testing.T) {
	const foreign = "#!/bin/sh\n# my project hook\nnpm run lint\n"
	hooksDir := t.TempDir()
	hookPath := filepath.Join(hooksDir, "pre-commit")
	if err := os.WriteFile(hookPath, []byte(foreign), 0o755); err != nil {
		t.Fatal(err)
	}

	var errs []string
	(&nukeCmd{}).removeBeadsHook(hooksDir, "pre-commit", &errs)

	got, err := os.ReadFile(hookPath)
	if err != nil {
		t.Fatalf("foreign hook should be left in place: %v", err)
	}
	if string(got) != foreign {
		t.Fatalf("foreign hook was modified:\n%s", got)
	}
}
