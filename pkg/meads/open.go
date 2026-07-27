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
	if _, err := os.Stat(filepath.Join(dir, "TASKS.csv")); err == nil {
		return BackendCSV, nil
	}
	return BackendMarkdown, nil
}

// OpenTasks opens dir's task store, whichever backend Detect finds - THE
// entry point for library consumers. It always returns a usable store:
// "nothing initialised yet" is Exists() == false, NOT an error (consumers
// like rais's project scan need to tell those apart cheaply).
func OpenTasks(dir string) (Tasks, error) {
	b, err := Detect(dir)
	if err != nil {
		return nil, err
	}
	return OpenTasksBackend(dir, b)
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
