package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
)

// This file implements task 71's command-visibility feature: which commands
// `md`'s rendered help advertises depends on whether the current repo looks
// like it is in git mode or file mode. Two things make this safe:
//
//   - Detection here (fastGitModeLikely/fastTasksFileExists/detectHelpMode)
//     is a FAST, filesystem-only APPROXIMATION, deliberately kept separate
//     from globals.mode() - the authoritative, subprocess-backed detector
//     that actually picks a command's storage backend. It runs on every
//     invocation, including a plain `md --help`, so it must never shell out
//     to git (see each function's doc comment for exactly what it stats/
//     reads instead). Being an approximation is fine here: getting this
//     wrong only ever changes which commands a --help listing mentions,
//     never which backend a command actually uses.
//   - Hiding a command from help never unregisters it (see filterHelp): the
//     root struct in main.go always declares every command, so opts' own
//     command dispatch always finds auto-save/auto-delete/beads-import
//     regardless of what help shows. This matters concretely: the installed
//     pre-commit hook runs `GITHOOK=1 md auto-save`/`auto-delete`
//     unconditionally (hook.go), and if those ever became unknown commands
//     in a git-mode repo, md would exit non-zero and abort the user's
//     commit.

// --- fast, subprocess-free mode detection ---

// helpMode is detectHelpMode's result: a best-effort guess at which storage
// backend is active, used only to decide which commands to advertise in
// rendered help (see hiddenCommands). It is NOT used to pick a backend -
// that is globals.mode()'s job alone.
type helpMode int

const (
	// helpModeUnknown means neither a git-mode marker nor a tasks file was
	// found (e.g. a brand new directory) - show every command, since
	// nothing here suggests any command is inapplicable.
	helpModeUnknown helpMode = iota
	// helpModeFile means a tasks file exists and no git-mode refs were
	// found.
	helpModeFile
	// helpModeGit means refs/meads/* appears non-empty.
	helpModeGit
)

// detectHelpMode inspects dir (cwd if empty) via stat/read calls only - see
// fastGitModeLikely and fastTasksFileExists - and never invokes git. Git-mode
// refs win over a merely-present tasks file, mirroring globals.mode()'s own
// auto-detect precedence: a stray leftover tasks file in an otherwise
// git-mode repo (e.g. left over from before `md convert --to-git`) must not
// flip this back to "file mode" - see hook_git_test.go's
// TestIntegration_AutoSave_GitMode_Noop, whose file-mode backend is proven
// to behave the same way.
func detectHelpMode(dir string) helpMode {
	if dir == "" {
		dir = "."
	}
	if fastGitModeLikely(dir) {
		return helpModeGit
	}
	if fastTasksFileExists(dir) {
		return helpModeFile
	}
	return helpModeUnknown
}

// fastGitDir resolves dir's ".git" entry to the actual git directory,
// without shelling out to git: handles both the ordinary "`.git` is a
// directory" layout and a linked worktree's "`.git` is a plain file
// containing `gitdir: <path>`" layout (git-worktree(1)). Returns ("", false)
// if there is no resolvable .git entry at all - treated by callers as "not a
// git repo" for help-visibility purposes only; see globals.inGitRepo for the
// authoritative, subprocess-based check every other command uses.
//
// For a linked worktree this is the PER-WORKTREE directory, which holds no
// shared refs - anything looking for one wants fastGitCommonDir on top, which
// is exactly the trap task 75 was.
//
// It also does not walk up: `md --help` from a SUBDIRECTORY of a git-mode
// repo still advertises the file-mode-only commands, because there is no .git
// entry beside it. That is a known, accepted limit of this approximation
// rather than an oversight (TASKS #93).
func fastGitDir(dir string) (string, bool) {
	p := filepath.Join(dir, ".git")
	info, err := os.Stat(p)
	if err != nil {
		return "", false
	}
	if info.IsDir() {
		return p, true
	}
	if info.Mode()&os.ModeType != 0 {
		return "", false // neither a directory nor a regular file (e.g. a symlink to nowhere useful, a socket, ...)
	}
	// Linked worktree: .git is a file containing "gitdir: <path>".
	data, err := os.ReadFile(p)
	if err != nil {
		return "", false
	}
	line := strings.TrimSpace(string(data))
	const prefix = "gitdir:"
	if !strings.HasPrefix(line, prefix) {
		return "", false
	}
	gitDir := strings.TrimSpace(strings.TrimPrefix(line, prefix))
	if gitDir == "" {
		return "", false
	}
	if !filepath.IsAbs(gitDir) {
		gitDir = filepath.Join(dir, gitDir)
	}
	if info, err := os.Stat(gitDir); err != nil || !info.IsDir() {
		return "", false
	}
	return gitDir, true
}

// fastGitCommonDir resolves gitDir to the git directory that holds SHARED
// state - refs, packed-refs, objects - which for a linked worktree is not
// gitDir itself.
//
// A linked worktree's git directory (<gitdir>/worktrees/<name>) holds only its
// own per-worktree state: HEAD, index, refs/bisect, refs/worktree. An ordinary
// ref like refs/meads/* is shared and lives in the common directory, named by
// a "commondir" file inside the per-worktree gitdir - relative to it when not
// absolute (gitrepository-layout(5), NOT git-worktree(1), which describes
// $GIT_COMMON_DIR but never this file). Without that hop fastGitModeLikely
// looked for refs/meads under a directory that has no refs/ at all, and read a
// packed-refs that is not there either, so every linked worktree of a git-mode
// repo reported file mode (task 75).
//
// git lets $GIT_COMMON_DIR outrank the file; this deliberately does not read
// it, for the same reason nothing else here consults the environment - see
// this file's top comment on staying a pure filesystem approximation.
//
// The main worktree has no commondir file; there gitDir is already the common
// directory, which is also the right answer for an unreadable or empty one. A
// bogus one needs no validation either: every use of the result is a ReadDir
// or ReadFile that simply fails, and a failed lookup already means "not git
// mode".
//
// A submodule is deliberately unaffected: <super>/.git/modules/<name> has no
// commondir file, so a submodule of a git-mode superproject keeps reporting
// its own mode rather than inheriting the parent's refs.
func fastGitCommonDir(gitDir string) string {
	data, err := os.ReadFile(filepath.Join(gitDir, "commondir"))
	if err != nil {
		return gitDir
	}
	common := strings.TrimSpace(string(data))
	if common == "" {
		return gitDir
	}
	if !filepath.IsAbs(common) {
		common = filepath.Join(gitDir, common)
	}
	return common
}

// fastGitModeLikely reports whether refs/meads/* appears to exist under
// dir's git directory, checked purely via filesystem stat/read - never a git
// subprocess. Refs can live loose under refs/meads/** (as plain files/dirs)
// or packed into a single packed-refs file (git pack-refs, git gc, or a
// clone/fetch that receives them already packed), so both are checked - a
// git-mode repo that has been gc'd would otherwise wrongly look like file
// mode the moment its loose refs disappear into packed-refs.
//
// Both lookups go through fastGitCommonDir, since refs/meads/* is a shared
// ref and a linked worktree's own git directory does not hold shared refs.
func fastGitModeLikely(dir string) bool {
	gitDir, ok := fastGitDir(dir)
	if !ok {
		return false
	}
	gitDir = fastGitCommonDir(gitDir)
	// Loose refs: refs/meads/ containing anything at all - e.g. a "config"
	// file (refs/meads/config, written by `md init --git` before any task
	// exists) and/or a "tasks" directory (refs/meads/tasks/<id> per task).
	// A single shallow listing is enough: we only need to know the
	// directory is non-empty, never what it actually says inside it.
	if entries, err := os.ReadDir(filepath.Join(gitDir, "refs", "meads")); err == nil && len(entries) > 0 {
		return true
	}
	// Packed refs: "<oid> refs/meads/...\n" lines. A leading space is part
	// of the match so an unrelated ref name that merely ends in "meads"
	// (e.g. a hypothetical "refs/heads/notmeads") can never be mistaken for
	// this namespace.
	data, err := os.ReadFile(filepath.Join(gitDir, "packed-refs"))
	if err != nil {
		return false
	}
	return bytes.Contains(data, []byte(" refs/meads/"))
}

// fastTasksFileExists reports whether dir contains a file-mode tasks file
// (TASKS.md or TASKS.csv) - the same pair bareDefaultTasksFile checks,
// deliberately not the MEADS_TASK_FILE/MD_TASKS-aware defaultTasksFile: this
// is a cheap, best-effort signal for help visibility only, not a substitute
// for globals.mode()'s full precedence.
func fastTasksFileExists(dir string) bool {
	if _, err := os.Stat(filepath.Join(dir, "TASKS.md")); err == nil {
		return true
	}
	if _, err := os.Stat(filepath.Join(dir, "TASKS.csv")); err == nil {
		return true
	}
	return false
}

// --- which commands to hide, given a helpMode ---

// hiddenCommands returns the set of top-level command names to omit from
// rendered help for m. auto-save/auto-delete/beads-import are file-mode
// concepts that either no-op (auto-save/auto-delete: no working-tree tasks
// file to stage/prune) or error (beads-import: errGitModeUnsupported) in git
// mode, so advertising them in a git-mode repo would invite exactly the
// confusion this task exists to prevent. They remain fully registered and
// invokable either way - see this file's top comment - only their HELP
// VISIBILITY changes.
//
// `sync` is hidden symmetrically in file mode because there are no task refs
// or remote operation to perform there. Unknown mode still shows everything:
// a new directory has not chosen a backend yet.
func hiddenCommands(m helpMode) map[string]bool {
	if m == helpModeFile {
		return map[string]bool{"sync": true}
	}
	if m != helpModeGit {
		return nil
	}
	return map[string]bool{
		"auto-save":    true,
		"auto-delete":  true,
		"beads-import": true,
	}
}

// --- filtering rendered help text ---

// isBulletLine reports whether line is one of opts' rendered command rows,
// e.g. "  · auto-save  Auto-save hook" (see the "cmd" template in
// github.com/jpillora/opts's node_help.go).
func isBulletLine(line string) bool {
	return strings.HasPrefix(strings.TrimLeft(line, " "), "· ")
}

// bulletCommandName extracts the command name from a rendered command row -
// the first whitespace-delimited field after the "· " marker, before the
// padding and help text that follow it.
func bulletCommandName(line string) string {
	rest := strings.TrimPrefix(strings.TrimLeft(line, " "), "· ")
	fields := strings.Fields(rest)
	if len(fields) == 0 {
		return ""
	}
	return fields[0]
}

// isCommandGroupHeader reports whether line is one of opts' rendered command
// group headings, e.g. "  Misc commands:" or the unnamed group's bare
// "  Commands:" (see the "cmdgroup" template in node_help.go).
func isCommandGroupHeader(line string) bool {
	t := strings.TrimSpace(line)
	return t == "Commands:" || strings.HasSuffix(t, " commands:")
}

// filterHelp removes rendered command rows for any name in hidden from a
// fully rendered opts help/error text, plus any command-group heading left
// with no rows under it (e.g. if every command in a group were hidden). It
// operates on the FINAL RENDERED STRING rather than opts' own internal
// node/template state, because opts v1.4.0 (see go.mod) has no API for
// hiding a command from a rendered listing (confirmed against its source -
// see cmd/md/main.go's doc comment on why main() calls ParseArgsError
// instead of Parse) or for filtering which commands exist without also
// making them unrecognised, which task 71's CRITICAL constraint forbids for
// auto-save/auto-delete (see this file's top comment).
//
// A blank hidden (nil or empty) returns help unchanged without scanning it,
// so the overwhelmingly common case - file mode, nothing hidden - costs
// nothing beyond the map lookup.
func filterHelp(help string, hidden map[string]bool) string {
	if len(hidden) == 0 {
		return help
	}
	lines := strings.Split(help, "\n")
	keep := make([]bool, len(lines))
	for i := range keep {
		keep[i] = true
	}
	for i, line := range lines {
		if isBulletLine(line) && hidden[bulletCommandName(line)] {
			keep[i] = false
		}
	}
	// A second pass, since a group header can only be judged empty once
	// every row under it has already been marked in the first pass.
	for i, line := range lines {
		if !keep[i] || !isCommandGroupHeader(line) {
			continue
		}
		anyKept := false
		for j := i + 1; j < len(lines); j++ {
			if strings.TrimSpace(lines[j]) == "" {
				break // blank line: end of this group
			}
			if !isBulletLine(lines[j]) {
				break // not a command row: not actually a command-group heading
			}
			if keep[j] {
				anyKept = true
			}
		}
		if !anyKept {
			keep[i] = false
		}
	}
	out := make([]string, 0, len(lines))
	for i, line := range lines {
		if keep[i] {
			out = append(out, line)
		}
	}
	// Removing a whole group (header + every row) leaves its surrounding
	// blank-line separators adjacent to each other; collapse the resulting
	// run exactly like hook.go's normalizeHook does for the same reason
	// (reusing its blankRunRe rather than declaring a second copy).
	return blankRunRe.ReplaceAllString(strings.Join(out, "\n"), "\n\n")
}
