package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// hookBlock is a self-contained, marker-delimited block within the repo's git
// pre-commit hook. md manages each block independently so multiple md features
// (auto-delete, auto-save) can coexist in the same hook file.
type hookBlock struct {
	marker  string // unique first line, e.g. "# md auto-delete hook"
	comment string // human-readable description shown beneath the marker
	command string // the md invocation to run, e.g. "md auto-delete"
}

// body renders the block from its marker through the closing `fi`, with no
// trailing newline. Removal matches on this so it stays byte-stable regardless
// of how many blank lines a neighbouring block leaves behind.
func (b hookBlock) body() string {
	return fmt.Sprintf(`%s
# %s
if command -v md >/dev/null 2>&1; then
    GITHOOK=1 %s
fi`, b.marker, b.comment, b.command)
}

// text renders the block plus the trailing blank line that separates it from
// any neighbouring block.
func (b hookBlock) text() string {
	return b.body() + "\n\n"
}

// blankRunRe matches a run of three or more newlines (two or more blank lines).
var blankRunRe = regexp.MustCompile(`\n{3,}`)

// normalizeHook trims surrounding blank lines and collapses internal blank-line
// runs to a single separator, keeping sibling blocks byte-stable as blocks are
// added and removed.
func normalizeHook(s string) string {
	return strings.Trim(blankRunRe.ReplaceAllString(s, "\n\n"), "\n")
}

// preCommitPath returns the absolute path to the repo's pre-commit hook.
// --absolute-git-dir is used (rather than --git-dir) so the path is correct
// regardless of the working directory md was invoked from.
func preCommitPath(g *globals) (string, error) {
	out, err := g.gitCommand("rev-parse", "--absolute-git-dir").Output()
	if err != nil {
		return "", fmt.Errorf("not in a git repository")
	}
	gitDir := strings.TrimSpace(string(out))
	return filepath.Join(gitDir, "hooks", "pre-commit"), nil
}

// install adds the block to the pre-commit hook. If the hook already exists
// without our block we prepend, so md staging runs before any existing hook
// logic. Returns false if the block was already present (no change made).
func (b hookBlock) install(g *globals) (bool, error) {
	path, err := preCommitPath(g)
	if err != nil {
		return false, err
	}
	existing, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			return false, fmt.Errorf("reading existing hook: %w", err)
		}
		if err := os.WriteFile(path, []byte(b.text()), 0755); err != nil {
			return false, fmt.Errorf("creating hook: %w", err)
		}
		return true, nil
	}
	if strings.Contains(string(existing), b.marker) {
		return false, nil
	}
	if err := os.WriteFile(path, []byte(b.text()+string(existing)), 0755); err != nil {
		return false, fmt.Errorf("writing hook: %w", err)
	}
	return true, nil
}

// remove deletes the block from the pre-commit hook, removing the hook file
// entirely if nothing else remains. Returns false if the block was not present.
func (b hookBlock) remove(g *globals) (bool, error) {
	path, err := preCommitPath(g)
	if err != nil {
		return false, err
	}
	content, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("reading hook: %w", err)
	}
	if !strings.Contains(string(content), b.marker) {
		return false, nil
	}
	newContent := normalizeHook(strings.Replace(string(content), b.body(), "", 1))
	if newContent == "" {
		if err := os.Remove(path); err != nil {
			return false, fmt.Errorf("removing hook: %w", err)
		}
		return true, nil
	}
	if err := os.WriteFile(path, []byte(newContent+"\n"), 0755); err != nil {
		return false, fmt.Errorf("writing hook: %w", err)
	}
	return true, nil
}

// installed reports whether the block is present in the pre-commit hook.
func (b hookBlock) installed(g *globals) (bool, error) {
	path, err := preCommitPath(g)
	if err != nil {
		return false, err
	}
	content, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("reading hook: %w", err)
	}
	return strings.Contains(string(content), b.marker), nil
}

// sequencerInProgress reports whether git is mid rebase, merge or cherry-pick.
// The staging hooks skip in that case: their "git add" would race git for
// .git/index.lock during the operation, and staging is redundant while a commit
// is being replayed. This removes the need for a manual core.hooksPath bypass.
func sequencerInProgress(g *globals) bool {
	out, err := g.gitCommand("rev-parse", "--absolute-git-dir").Output()
	if err != nil {
		return false
	}
	gitDir := strings.TrimSpace(string(out))
	for _, marker := range []string{"rebase-merge", "rebase-apply", "MERGE_HEAD", "CHERRY_PICK_HEAD"} {
		if _, err := os.Stat(filepath.Join(gitDir, marker)); err == nil {
			return true
		}
	}
	return false
}
