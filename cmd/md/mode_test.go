package main

import (
	"sync"
	"testing"

	"github.com/jpillora/meads/pkg/meads"
)

// Tests for git mode phase 5 (TASKS #62): globals.mode()/tasks() detection
// and its overrides. Run against real temporary git repos (via the existing
// harness, see harness_test.go) rather than a fake, since the guarantee
// under test - a real for-each-ref lookup actually seeing (or not seeing)
// task refs - is precisely what a fake would rubber-stamp without exercising.

// --- defaultGitMode: MEADS_GIT env parsing ---

func TestDefaultGitMode_EnvVar(t *testing.T) {
	tests := []struct {
		val  string
		want bool
	}{
		{"", false},
		{"0", false},
		{"false", false},
		{"garbage", false},
		{"1", true},
		{"true", true},
		{"TRUE", true},
		{"yes", true},
		{"on", true},
	}
	for _, tt := range tests {
		t.Run(tt.val, func(t *testing.T) {
			t.Setenv("MEADS_GIT", tt.val)
			if got := defaultGitMode(); got != tt.want {
				t.Errorf("defaultGitMode() with MEADS_GIT=%q = %v, want %v", tt.val, got, tt.want)
			}
		})
	}
}

// --- explicitTasksFile / bareDefaultTasksFile ---

func TestExplicitTasksFile(t *testing.T) {
	t.Chdir(t.TempDir()) // a dir with no TASKS.csv, so the bare default is "TASKS.md"

	g := &globals{TasksFile: "TASKS.md"}
	if g.explicitTasksFile() {
		t.Error("TasksFile == the bare default should not count as explicit")
	}

	g = &globals{TasksFile: "custom-tasks.md"}
	if !g.explicitTasksFile() {
		t.Error("a TasksFile other than the bare default should count as explicit")
	}

	g = &globals{TasksFile: "/abs/path/TASKS.md"}
	if !g.explicitTasksFile() {
		t.Error("an absolute TasksFile path should count as explicit (this is why harness-based tests never accidentally exercise git-mode detection)")
	}
}

// --- mode(): auto-detection ---

// modeHarness returns a harness whose globals is set up to actually exercise
// mode()'s auto-detection branch, rather than harness_test.go's usual
// absolute-path TasksFile (which always counts as "explicit" and pins every
// ordinary harness test to file mode regardless of what refs exist - see
// TestExplicitTasksFile above). It chdirs the test process into the repo
// (auto-restored by t.Chdir) and resets TasksFile to the bare relative
// default so explicitTasksFile() is false.
func modeHarness(t *testing.T) *testHarness {
	t.Helper()
	h := newHarness(t)
	t.Chdir(h.dir)
	h.globals.TasksFile = "TASKS.md"
	return h
}

func TestMode_AutoDetect_FileWhenNoTaskRefs(t *testing.T) {
	h := modeHarness(t)
	if got := h.globals.mode(); got != modeFile {
		t.Errorf("mode() with no refs/meads/tasks/* = %v, want modeFile", got)
	}
}

func TestMode_AutoDetect_GitWhenTaskRefsExist(t *testing.T) {
	h := modeHarness(t)
	gs := meads.NewGitStore(h.globals.git())
	if _, err := gs.Create(meads.Task{Title: "a git task", Status: "open"}); err != nil {
		t.Fatalf("seeding a task ref: %v", err)
	}
	if got := h.globals.mode(); got != modeGit {
		t.Errorf("mode() with a refs/meads/tasks/* ref present = %v, want modeGit", got)
	}
}

func TestMode_FileFlagOverridesDetection(t *testing.T) {
	h := modeHarness(t)
	gs := meads.NewGitStore(h.globals.git())
	if _, err := gs.Create(meads.Task{Title: "a git task", Status: "open"}); err != nil {
		t.Fatalf("seeding a task ref: %v", err)
	}
	h.globals.FileMode = true
	if got := h.globals.mode(); got != modeFile {
		t.Errorf("mode() with --file and task refs present = %v, want modeFile (explicit flag must win)", got)
	}
}

func TestMode_GitFlagOverridesDetection(t *testing.T) {
	h := modeHarness(t) // zero task refs: auto-detection alone would pick modeFile
	h.globals.GitMode = true
	if got := h.globals.mode(); got != modeGit {
		t.Errorf("mode() with --git and no task refs = %v, want modeGit (explicit flag must win)", got)
	}
}

func TestMode_ExplicitTasksFileForcesFileMode(t *testing.T) {
	h := modeHarness(t)
	gs := meads.NewGitStore(h.globals.git())
	if _, err := gs.Create(meads.Task{Title: "a git task", Status: "open"}); err != nil {
		t.Fatalf("seeding a task ref: %v", err)
	}
	// No --file, no --git: just an explicit tasks file path, which alone
	// must be enough to force file mode even though a task ref exists.
	h.globals.TasksFile = "some-other-file.md"
	if got := h.globals.mode(); got != modeFile {
		t.Errorf("mode() with an explicit TasksFile and task refs present = %v, want modeFile", got)
	}
}

func TestMode_MEADSGITEnvVar_EndToEnd(t *testing.T) {
	h := modeHarness(t) // zero task refs
	t.Setenv("MEADS_GIT", "1")
	// Mirrors main()'s wiring: c.Globals.GitMode = defaultGitMode(), computed
	// before any explicit --git/--file flag would be applied by opts.Parse.
	h.globals.GitMode = defaultGitMode()
	if got := h.globals.mode(); got != modeGit {
		t.Errorf("mode() with MEADS_GIT=1 and no task refs = %v, want modeGit", got)
	}
}

// --- mode()/tasks() outside a git repository ---

func TestMode_OutsideGitRepo_NoError(t *testing.T) {
	dir := t.TempDir() // not a git repo
	g := &globals{
		Git:       &meads.ExecGit{Dir: dir},
		Dir:       dir,
		TasksFile: "TASKS.md",
	}
	if got := g.mode(); got != modeFile {
		t.Errorf("mode() outside a git repo = %v, want modeFile", got)
	}
	if g.gitTaskRefsExist() {
		t.Error("gitTaskRefsExist() outside a git repo should be false")
	}
	if g.inGitRepo() {
		t.Error("inGitRepo() outside a git repo should be false")
	}
	ts, err := g.tasks()
	if err != nil {
		t.Fatalf("tasks() outside a git repo (unforced) should not error, got: %v", err)
	}
	if _, ok := ts.(meads.FileTasks); !ok {
		t.Errorf("tasks() outside a git repo = %T, want meads.FileTasks", ts)
	}
}

func TestTasks_ForcedGitOutsideRepo_ErrorsClearly(t *testing.T) {
	dir := t.TempDir() // not a git repo
	g := &globals{
		Git:       &meads.ExecGit{Dir: dir},
		Dir:       dir,
		TasksFile: "TASKS.md",
		GitMode:   true,
	}
	_, err := g.tasks()
	if err == nil {
		t.Fatal("tasks() with --git forced outside a git repo should error, got nil")
	}
	if got := err.Error(); got != "--git requires a git repository" {
		t.Errorf("tasks() error = %q, want a clear \"--git requires a git repository\" message", got)
	}
}

func TestTasks_ConflictingFlagsErrors(t *testing.T) {
	h := modeHarness(t)
	h.globals.GitMode = true
	h.globals.FileMode = true
	if _, err := h.globals.tasks(); err == nil {
		t.Fatal("tasks() with both --git and --file set should error, got nil")
	}
}

// --- tasks() caching ---

// countingGit wraps meads.Git and counts Output calls, so a test can prove a
// second tasks() call within one command does not re-run the detection
// ref-lookup (mirrors pkg/meads/gitconfig_test.go's countingGit).
type countingGit struct {
	meads.Git
	mu     sync.Mutex
	output int
}

func (c *countingGit) Output(args ...string) (string, error) {
	c.mu.Lock()
	c.output++
	c.mu.Unlock()
	return c.Git.Output(args...)
}

func (c *countingGit) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.output
}

func TestTasks_CachesResult(t *testing.T) {
	h := modeHarness(t)
	cg := &countingGit{Git: h.globals.git()}
	h.globals.Git = cg

	ts1, err := h.globals.tasks()
	if err != nil {
		t.Fatalf("tasks(): %v", err)
	}
	afterFirst := cg.count()
	if afterFirst == 0 {
		t.Fatal("first tasks() call never invoked git Output - this test isn't exercising a real detection call")
	}

	ts2, err := h.globals.tasks()
	if err != nil {
		t.Fatalf("tasks() (second call): %v", err)
	}
	if cg.count() != afterFirst {
		t.Errorf("second tasks() call spawned more git Output calls: %d -> %d, want unchanged (cached)", afterFirst, cg.count())
	}
	if ts1 != ts2 {
		t.Errorf("tasks() returned different values across calls: %v vs %v, want the identical cached value", ts1, ts2)
	}
}
