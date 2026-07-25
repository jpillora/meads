package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/jpillora/meads/pkg/meads"
	"github.com/jpillora/opts"
)

var version = "0.0.0-dev"

// bareDefaultTasksFile is defaultTasksFile's fallback with no env vars
// consulted: whichever of TASKS.csv/TASKS.md would be picked with nothing
// configured at all. It also doubles as globals.explicitTasksFile's baseline
// for detecting whether TasksFile was pinned to something else (see there).
func bareDefaultTasksFile() string {
	if _, err := os.Stat("TASKS.csv"); err == nil {
		return "TASKS.csv"
	}
	return "TASKS.md"
}

func defaultTasksFile() string {
	if v := os.Getenv("MEADS_TASK_FILE"); v != "" {
		return v
	}
	if v := os.Getenv("MD_TASKS"); v != "" {
		return v
	}
	return bareDefaultTasksFile()
}

func defaultWebhookURI() string {
	return os.Getenv("MEADS_WEBHOOK_URI")
}

// defaultGitMode reads MEADS_GIT the same way defaultTasksFile reads
// MEADS_TASK_FILE/MD_TASKS: it is computed once in main(), before
// opts.Parse() applies the globals struct's current value as each flag's
// default, so an explicit --git/--file on the command line still wins (see
// globals.mode) while a shell that exports MEADS_GIT=1 gets git mode on
// every invocation without typing --git each time.
func defaultGitMode() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("MEADS_GIT"))) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

type globals struct {
	Store      *meads.Store    `opts:"-"`
	GitStore   *meads.GitStore `opts:"-"`
	Git        meads.Git       `opts:"-"`
	TasksFile  string          `help:"the tasks markdown file to manage (env MEADS_TASK_FILE)"`
	WebhookURI string          `help:"a uri to POST to with {meads:true,action,file,data}; http(s):// or unix:///path/to/sock or unix://[/path/to/sock]/http/path (env MEADS_WEBHOOK_URI)"`
	Dir        string          `opts:"-"`

	// GitMode/FileMode force git-mode (refs/meads/*) or file-mode
	// (TasksFile) storage, overriding auto-detection (see mode). Go field
	// names differ from their CLI flag names (--git/--file) only because
	// "Git" is already taken by the meads.Git field above.
	GitMode  bool `opts:"name=git" help:"Force git mode (refs/meads/*), overriding auto-detection (env MEADS_GIT)"`
	FileMode bool `opts:"name=file" help:"Force file mode (TasksFile), overriding auto-detection"`

	// TaskStoreCache memoizes tasks(): mode() and git-mode construction each
	// cost one git subprocess spawn, and neither answer can change within
	// one md process's lifetime, so recomputing per call (e.g. once for a
	// command's main read/write, once more for warnCycles) would just be a
	// redundant plumbing spawn for the same answer.
	TaskStoreCache taskStore `opts:"-"`
}

// tasksFileAbs resolves TasksFile to an absolute path. It is included in every
// webhook payload so a consumer receiving events from multiple tasks files can
// tell them apart. Falls back to the raw path if resolution fails.
func (g *globals) tasksFileAbs() string {
	path := g.TasksFile
	if !filepath.IsAbs(path) && g.Dir != "" {
		path = filepath.Join(g.Dir, path)
	}
	if abs, err := filepath.Abs(path); err == nil {
		return abs
	}
	return path
}

// gitCommand creates an exec.Command for git with Dir set.
// Used by hook management (enable/disable/checkStatus) which needs exec.Cmd directly.
func (g *globals) gitCommand(args ...string) *exec.Cmd {
	cmd := exec.Command("git", args...)
	if g.Dir != "" {
		cmd.Dir = g.Dir
	}
	return cmd
}

// warnCycles prints a stderr warning for each circular dependency in the tasks
// store (file or git, whichever this invocation resolved to - see
// globals.tasks). A cycle silently deadlocks the tasks it spans — they can
// never become ready — so read commands surface it rather than letting it go
// unnoticed. It writes to stderr to keep stdout (including --json) clean, and
// stays quiet on any error (mode resolution or the read itself), which the
// calling command reports through its own path.
func warnCycles(g *globals) {
	ts, err := g.tasks()
	if err != nil {
		return
	}
	cycles, err := ts.FindCycles()
	if err != nil {
		return
	}
	for _, cycle := range cycles {
		fmt.Fprintf(os.Stderr, "warning: circular dependency: %s (run 'md doctor')\n", meads.FormatCycle(cycle))
	}
}

// store returns the Store, lazily initializing from TasksFile if not set.
func (g *globals) store() *meads.Store {
	if g.Store == nil {
		g.Store = meads.NewFileStore(g.TasksFile)
	}
	return g.Store
}

// gitStore returns the GitStore, lazily initializing if not set. Mirrors store().
func (g *globals) gitStore() *meads.GitStore {
	if g.GitStore == nil {
		g.GitStore = meads.NewGitStore(g.git())
	}
	return g.GitStore
}

// git returns the Git implementation, lazily initializing if not set.
func (g *globals) git() meads.Git {
	if g.Git == nil {
		g.Git = &meads.ExecGit{Dir: g.Dir}
	}
	return g.Git
}

// inGitRepo reports whether Dir (or the process cwd) is inside a git
// repository. Any git failure - including "not a git repository" - reports
// false rather than propagating an error; callers that need a clear message
// for the negative case build their own from this (see initCmd.runGit and
// tasks() below).
func (g *globals) inGitRepo() bool {
	_, err := g.git().Output("rev-parse", "--git-dir")
	return err == nil
}

// explicitTasksFile reports whether TasksFile was pinned to something other
// than the ordinary zero-config default (bareDefaultTasksFile: a bare
// "TASKS.md", or "TASKS.csv" if that's the only one present) - whether by
// the --tasks-file flag or by the MEADS_TASK_FILE/MD_TASKS env vars
// defaultTasksFile folds in before opts.Parse runs. opts has no API to ask
// "was this flag explicitly passed" (unlike, say, Go's flag.Visit), so this
// is the closest cheap proxy - and either source means the same thing here:
// the user pointed md at one specific file, which unambiguously means file
// mode regardless of what refs happen to exist in the current repo.
func (g *globals) explicitTasksFile() bool {
	return g.TasksFile != bareDefaultTasksFile()
}

// gitTaskRefsExist is the one cheap ref lookup mode() uses for
// auto-detection: git mode is active iff the refs/meads/* namespace is
// non-empty. A for-each-ref over an empty/absent namespace still exits 0 with
// no output (see RefStore.ResolveRef's doc comment), and outside a git
// repository entirely it fails - both cases fold into "false" here, so
// detection itself never surfaces an error to the caller, matching the
// requirement that it must not error outside a repo.
//
// It deliberately checks the whole namespace, not just refs/meads/tasks/*.
// A freshly initialised repo has no tasks yet, so a tasks-only check would
// report file mode, the first `md add` would write TASKS.md into the working
// tree, and git mode could never bootstrap. `md init --git` writes the config
// ref precisely so the namespace is non-empty from the start.
func (g *globals) gitTaskRefsExist() bool {
	refs, err := meads.NewRefStore(g.git()).ListRefs(meads.RefNamespace)
	if err != nil {
		return false
	}
	return len(refs) > 0
}

// taskStoreMode identifies which backend an invocation should use.
type taskStoreMode int

const (
	modeFile taskStoreMode = iota
	modeGit
)

// mode resolves which backend this invocation should use, in priority order:
//  1. --file, or an explicit tasks file (explicitTasksFile), forces file mode.
//  2. --git, or MEADS_GIT via defaultGitMode, forces git mode.
//  3. Otherwise auto-detect: git mode iff refs/meads/tasks/* exists.
//
// Outside a git repo, gitTaskRefsExist reports false, so an unforced
// invocation always falls through to file mode without erroring.
func (g *globals) mode() taskStoreMode {
	if g.FileMode || g.explicitTasksFile() {
		return modeFile
	}
	if g.GitMode {
		return modeGit
	}
	if g.gitTaskRefsExist() {
		return modeGit
	}
	return modeFile
}

// modeConflictErr reports whether --git and --file were both forced, which
// mode()'s plain precedence order would otherwise resolve silently (--file
// wins). Shared by tasks() and by commands not wired to the taskStore seam
// (doctor.go, import.go, mcp.go, webui.go all check mode() directly to
// decide whether to refuse git mode) so a self-contradictory flag
// combination is reported consistently regardless of which path a command
// takes, rather than only for the commands that happen to go through tasks().
func (g *globals) modeConflictErr() error {
	if g.GitMode && g.FileMode {
		return fmt.Errorf("cannot use both --git and --file")
	}
	return nil
}

// tasks returns the taskStore seam commands use to work against either
// backend (see cmd/md/taskstore.go), computed from mode() and cached in
// TaskStoreCache: mode() and git-mode construction each cost a git
// subprocess spawn, and the answer cannot change within one md process's
// lifetime, so a command's second call (e.g. warnCycles after the command's
// own read) reuses the first result rather than spawning again.
//
// Construction can fail where mode() alone cannot: forcing git mode (--git
// or MEADS_GIT) outside a git repository has no cheap silent fallback the
// way auto-detection does, so it errors clearly instead of leaving the first
// git-plumbing call downstream to fail with a raw git error.
func (g *globals) tasks() (taskStore, error) {
	if g.TaskStoreCache != nil {
		return g.TaskStoreCache, nil
	}
	if err := g.modeConflictErr(); err != nil {
		return nil, err
	}
	if g.mode() == modeGit {
		if !g.inGitRepo() {
			return nil, fmt.Errorf("--git requires a git repository")
		}
		g.TaskStoreCache = gitTaskStore{gs: g.gitStore()}
		return g.TaskStoreCache, nil
	}
	g.TaskStoreCache = fileTaskStore{store: g.store(), git: g.git()}
	return g.TaskStoreCache, nil
}

type root struct {
	Globals     globals        `opts:"mode=embedded"`
	Add         addCmd         `opts:"mode=cmd,group=Basic" help:"Add a new task"`
	Create      createCmd      `opts:"mode=cmd,group=Basic" help:"Create a new task (alias for add)"`
	Get         getCmd         `opts:"mode=cmd,group=Basic" help:"Get tasks by ID"`
	List        listCmd        `opts:"mode=cmd,group=Basic" help:"List all tasks"`
	Del         delCmd         `opts:"mode=cmd,group=Basic" help:"Delete a task by ID"`
	Update      updateCmd      `opts:"mode=cmd,group=Basic" help:"Update a task by ID"`
	SetStatus   setStatusCmd   `opts:"mode=cmd,name=set-status,group=Basic" help:"Set a task's status"`
	AddDep      addDepCmd      `opts:"mode=cmd,name=add-dep,group=Basic" help:"Add a dependency to a task"`
	RmDep       rmDepCmd       `opts:"mode=cmd,name=rm-dep,group=Basic" help:"Remove a dependency from a task"`
	Ready       readyCmd       `opts:"mode=cmd,group=Basic" help:"List open tasks not blocked by dependencies"`
	Init        initCmd        `opts:"mode=cmd,group=Misc" help:"Initialize a new tasks file, or git mode with --git"`
	Convert     convertCmd     `opts:"mode=cmd,group=Misc" help:"Convert between TASKS.md/TASKS.csv formats, or migrate to/from git mode with --to-git/--from-git"`
	Prime       primeCmd       `opts:"mode=cmd,group=Misc" help:"Print LLM context for using md (describes whichever mode is active)"`
	Mcp         mcpCmd         `opts:"mode=cmd,group=Misc" help:"Start MCP server over stdio (file or git mode)"`
	Webui       webuiCmd       `opts:"mode=cmd,group=Misc" help:"Launch web UI for the current task store (file or git mode)"`
	Doctor      doctorCmd      `opts:"mode=cmd,group=Misc" help:"Detect and fix duplicate task IDs (in git mode, also reports diverged tasks)"`
	AutoDelete  autoDeleteCmd  `opts:"mode=cmd,name=auto-delete,group=Misc" help:"Auto-delete closed tasks via git hook (no-op in git mode: nothing to prune)"`
	AutoSave    autoSaveCmd    `opts:"mode=cmd,name=auto-save,group=Misc" help:"Auto-stage the tasks file in every commit via git hook (no-op in git mode: no tasks file)"`
	BeadsImport beadsImportCmd `opts:"mode=cmd,name=beads-import,group=Beads" help:"Import tasks from beads (file mode only)"`
	BeadsNuke   nukeCmd        `opts:"mode=cmd,name=beads-nuke,group=Beads" help:"Completely remove beads from the current repository"`
}

func main() {
	c := root{}
	c.Globals.TasksFile = defaultTasksFile()
	c.Globals.WebhookURI = defaultWebhookURI()
	c.Globals.GitMode = defaultGitMode()
	g := &c.Globals
	c.Add.globals = g
	c.Create.globals = g

	c.Get.globals = g
	c.List.globals = g
	c.Del.globals = g
	c.Update.globals = g
	c.SetStatus.globals = g
	c.AddDep.globals = g
	c.RmDep.globals = g
	c.Ready.globals = g
	c.Init.globals = g
	c.Convert.globals = g
	c.Prime.globals = g
	c.BeadsImport.globals = g
	c.Mcp.globals = g
	c.Webui.globals = g
	c.Doctor.globals = g
	c.AutoDelete.globals = g
	c.AutoSave.globals = g
	c.BeadsNuke.globals = g

	// Which commands to hide from rendered help - see help_visibility.go.
	// Computed once, up front, from the fast filesystem-only check (never
	// globals.mode(), which is subprocess-backed and reserved for actually
	// picking a backend) so it costs nothing extra even when help is never
	// rendered at all (the ordinary "a real command ran" path below).
	hidden := hiddenCommands(detectHelpMode(""))

	// ParseArgsError, not Parse/ParseArgs: opts' own auto-exiting variants
	// print help text and os.Exit(1) INSIDE the library, before md ever
	// gets a chance to filter hidden commands out of it (confirmed against
	// opts v1.4.0's source - see optsFailureText's doc comment for exactly
	// what each variant prints and why). Using the Error-returning form
	// instead means every exit path is reproduced here, in md, where the
	// rendered text can be filtered first.
	p, err := opts.New(&c).
		Name("md").
		Version(version).
		Summary("Git-native task tracking — a Markdown/CSV file, or git refs in git mode").
		Repo("https://github.com/jpillora/meads").
		ParseArgsError(os.Args)
	if err != nil {
		fmt.Fprint(os.Stderr, filterHelp(optsFailureText(p, err), hidden))
		os.Exit(1)
	}
	if !p.IsRunnable() {
		fmt.Println(filterHelp(p.Help(), hidden))
		return
	}
	p.RunFatal()
}

// optsFailureText reproduces exactly what opts v1.4.0's own auto-exiting
// Parse()/ParseArgs() would print for a parse failure, given the (p, err)
// ParseArgsError returns instead - so main can filter it (see filterHelp)
// before printing it itself. Verified against opts' actual source and
// behaviour (github.com/jpillora/opts@v1.4.0's node_parse.go/node_commands.go):
//
//   - "-h"/"--help" (on any node - root or a subcommand), and any flag-parse
//     error (e.g. an unknown flag), all internally set opts' internalOpts.Help
//     and return their own unexported "exitError" type, whose Error() IS
//     already the exact text to print verbatim: the resolved node's full
//     rendered Help() (with the error embedded too, for a flag-parse
//     error) - always starting with "Usage:".
//   - "--version"/"-v" also returns that same unexported exitError type, but
//     with just the bare version string as its content (never "Usage:").
//   - Any OTHER error (e.g. an unknown top-level command, "unexpected
//     arguments: ...") is a plain error whose message alone is not the full
//     picture: opts' own fallback there is to print root's Help() with the
//     message embedded in its "Error:" section (n.Help(), called on the
//     same node ParseArgsError returns as p - see its ParseArgs).
//
// exitError is unexported, so it cannot be type-switched on from outside
// the opts package. This reproduces the same three-way split by content
// instead: err.Error() is used verbatim when it is either exactly version
// (the --version/-v case) or already looks like a rendered help document
// (contains "Usage:" - the -h/--help and flag-parse-error cases); anything
// else falls back to p.Help(), matching opts' own generic-error fallback
// exactly. See TestOptsFailureText for every case this covers.
func optsFailureText(p opts.ParsedOpts, err error) string {
	text := err.Error()
	if text == version || strings.Contains(text, "Usage:") {
		return text
	}
	return p.Help()
}
