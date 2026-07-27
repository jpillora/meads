package meads

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/go-git/go-billy/v5"
)

// Detect reports which backend dir's tasks live in: BackendGit iff the
// refs/meads/* namespace is non-empty, else BackendCSV iff TASKS.csv exists,
// else BackendMarkdown.
//
// It probes the WHOLE refs/meads/ namespace, not just refs/meads/tasks/* - a
// fresh `init --git` has no tasks (only the config ref), and a tasks-only
// probe would mean git mode could never bootstrap (the first add would write
// TASKS.md into the working tree). Any git failure, including "not a
// repository", folds to a file backend, so detection never errors; the error
// return exists for signature parity with the OpenTasks family.
func Detect(dir string) (Backend, error) {
	refs, err := NewRefStore(&ExecGit{Dir: dir}).ListRefs(RefNamespace)
	if err == nil && len(refs) > 0 {
		return BackendGit, nil
	}
	if fileExists(filepath.Join(dir, "TASKS.csv")) {
		return BackendCSV, nil
	}
	return BackendMarkdown, nil
}

// OpenTasks opens dir's task store - THE entry point for library
// consumers. It always returns a usable store: "nothing initialised yet" is
// Exists() == false, NOT an error (consumers like rais's project scan need
// to tell those apart cheaply).
//
// Unlike Detect (which must stay pure, offline and cheap: rais calls it on
// a ticker), OpenTasks additionally runs the ONE-SHOT clone resolution
// (resolveCloneBackend) when the probe is ambiguous - no tasks file and no
// local refs/meads/* is either an uninitialised repo or a fresh clone of a
// git-mode repo, which only one remote round-trip can tell apart. The
// answer is cached (adopted refs, or the InitCheckRef marker), so the
// steady state stays at a single for-each-ref per call and the ls-remote
// happens at most once per clone, ever.
func OpenTasks(dir string) (Tasks, error) {
	return OpenTasksGit(dir, &ExecGit{Dir: dir})
}

// OpenTasksGit is OpenTasks with an explicit Git implementation for the
// detection/resolution probes, so callers that already hold one (cmd/md's
// globals) route every probe through it - which is also how tests count or
// script git calls. The constructed store still derives its own git from
// dir (see OpenTasksFile).
func OpenTasksGit(dir string, git Git) (Tasks, error) {
	meadsRefs, initChecked := probeInitState(git)
	switch {
	case len(meadsRefs) > 0:
		return OpenTasksBackend(dir, BackendGit)
	case fileExists(filepath.Join(dir, "TASKS.csv")):
		return OpenTasksBackend(dir, BackendCSV)
	case fileExists(filepath.Join(dir, "TASKS.md")):
		// An existing tasks file is unambiguous file mode, never a reason
		// to ask origin anything.
		return OpenTasksBackend(dir, BackendMarkdown)
	case initChecked:
		// "Asked origin already; it has no refs/meads/*" - file mode, no
		// network (see InitCheckRef).
		return OpenTasksBackend(dir, BackendMarkdown)
	default:
		return OpenTasksBackend(dir, resolveCloneBackend(git))
	}
}

// fileExists reports whether path is present (any stat error reads as
// "not", so detection paths never error on it).
func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// OpenTasksBackend opens dir's task store for a forced backend, bypassing
// Detect (the library analogue of --git/--file). Construction cannot fail
// even outside a git repository - the returned store's operations surface
// any git errors instead - so the error return only covers an unknown
// Backend.
func OpenTasksBackend(dir string, b Backend) (Tasks, error) {
	switch b {
	case BackendGit:
		return NewGitTasks(NewGitStore(&ExecGit{Dir: dir})), nil
	case BackendMarkdown:
		return OpenTasksFile(filepath.Join(dir, "TASKS.md"))
	case BackendCSV:
		return OpenTasksFile(filepath.Join(dir, "TASKS.csv"))
	}
	return nil, fmt.Errorf("unknown backend %v", b)
}

// OpenTasksFile opens an explicit tasks file (the library analogue of
// --tasks-file). The file's own directory supplies the git context
// GetWithHistory/GetHistory need for their history walks.
func OpenTasksFile(file string) (Tasks, error) {
	return NewFileTasks(NewFileStore(file), &ExecGit{Dir: filepath.Dir(file)}), nil
}

// OpenTasksFS opens a tasks file on an arbitrary filesystem, so consumers
// whose tests are built on memfs-backed stores keep working now that
// NewStore(memfs.New(), ...) is no longer the Tasks entry point. git is nil:
// the history methods must not be called (see FileTasks' doc comment).
func OpenTasksFS(fs billy.Filesystem, file string) (Tasks, error) {
	return NewFileTasks(NewStore(fs, file), nil), nil
}
