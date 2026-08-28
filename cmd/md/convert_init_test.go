package main

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/jpillora/meads/pkg/meads"
)

// hasFetchRefspec reports whether origin's fetch refspecs include meads'.
func hasFetchRefspec(t *testing.T, dir string) bool {
	t.Helper()
	lines, _ := gitConfigGetAll(t, dir, "remote.origin.fetch")
	for _, line := range lines {
		if line == meads.FetchRefspec {
			return true
		}
	}
	return false
}

func hasMeadsRefs(t *testing.T, h *testHarness) bool {
	t.Helper()
	refs, err := meads.NewRefStore(h.globals.git()).ListRefs(meads.RefNamespace)
	if err != nil {
		t.Fatal(err)
	}
	return len(refs) > 0
}

// gitModeGlobals builds globals forced into git mode over h's repo. The
// harness passes an explicit --tasks-file, which pins mode() to file mode
// regardless of refs; a real git-mode repo has no tasks file to pin it.
func gitModeGlobals(h *testHarness) *globals {
	return &globals{Git: &meads.ExecGit{Dir: h.dir}, Dir: h.dir, TasksFile: "TASKS.md", GitMode: true}
}

// TestConvert_FileToGit_AddsFetchRefspec is task 91: the README offers `md
// init --git` and `md convert TASKS.md --to-git` as two independent ways into
// git mode, but only the first added origin's fetch refspec. A repo migrated
// the documented way was left unable to fetch anyone else's task refs, and
// could not be repaired, since init refuses once refs/meads/ has any ref.
func TestConvert_FileToGit_AddsFetchRefspec(t *testing.T) {
	h := newHarness(t) // origin is already configured (see harness_test.go)
	h.addTask("One")

	if hasFetchRefspec(t, h.dir) {
		t.Fatal("precondition: origin should not have the meads fetch refspec yet")
	}

	if err := (&convertCmd{globals: h.globals, File: h.globals.TasksFile, ToGit: true}).Run(); err != nil {
		t.Fatalf("convert --to-git: %v", err)
	}

	if !hasFetchRefspec(t, h.dir) {
		t.Errorf("origin is missing %q after convert --to-git", meads.FetchRefspec)
	}
}

// TestConvert_FileToGit_MatchesInitGitRefspec is the invariant behind the fix:
// whichever way you enter git mode, the repo can share its task refs.
func TestConvert_FileToGit_MatchesInitGitRefspec(t *testing.T) {
	viaInit := newHarness(t)
	if err := (&initCmd{globals: viaInit.globals, Git: true}).Run(); err != nil {
		t.Fatalf("init --git: %v", err)
	}

	viaConvert := newHarness(t)
	viaConvert.addTask("One")
	if err := (&convertCmd{globals: viaConvert.globals, File: viaConvert.globals.TasksFile, ToGit: true}).Run(); err != nil {
		t.Fatalf("convert --to-git: %v", err)
	}

	for _, h := range []*testHarness{viaInit, viaConvert} {
		if !hasFetchRefspec(t, h.dir) {
			t.Errorf("fetch refspec missing in %s", h.dir)
		}
	}
}

// TestConvert_FileToGit_WritesProtocolVersion pins the compatibility marker
// for the second supported route into git mode. ImportTask writes it before
// the first task ref, so a future md can change ref semantics without older
// binaries silently interpreting the new representation.
func TestConvert_FileToGit_WritesProtocolVersion(t *testing.T) {
	h := newHarness(t)
	h.addTask("One")
	if err := (&convertCmd{globals: h.globals, File: h.globals.TasksFile, ToGit: true}).Run(); err != nil {
		t.Fatalf("convert --to-git: %v", err)
	}
	content, _, err := meads.NewRefStore(h.globals.git()).ReadFileAtRef(meads.ConfigRef, meads.ConfigFileName)
	if err != nil {
		t.Fatalf("reading %s after conversion: %v", meads.ConfigRef, err)
	}
	var config map[string]any
	if err := json.Unmarshal(content, &config); err != nil {
		t.Fatalf("parsing %s: %v", meads.ConfigFileName, err)
	}
	if got := config["git_ref_protocol_version"]; got != float64(meads.GitRefProtocolVersion) {
		t.Errorf("git_ref_protocol_version = %v, want %d", got, meads.GitRefProtocolVersion)
	}
}

// TestConvert_FileToGit_NoTasksLeavesRepoUntouched: a typo'd filename imports
// nothing, and must then leave no trace - not a half-migrated repo whose real
// TASKS.md is suddenly invisible.
func TestConvert_FileToGit_NoTasksLeavesRepoUntouched(t *testing.T) {
	h := newHarness(t)
	h.addTask("Still in the file")

	missing := h.dir + "/TASKS-typo.md"
	out, err := captureStdout(t, (&convertCmd{globals: h.globals, File: missing, ToGit: true}).Run)
	if err != nil {
		t.Fatalf("convert --to-git on an empty source: %v", err)
	}
	if hasMeadsRefs(t, h) {
		t.Error("a zero-task conversion wrote refs under refs/meads/")
	}
	if hasFetchRefspec(t, h.dir) {
		t.Error("a zero-task conversion configured a fetch refspec")
	}
	// It must not claim anything about origin either: the setup step was
	// skipped, and "no 'origin' remote configured" would be a lie here.
	if strings.Contains(out, "origin") {
		t.Errorf("output mentions origin after a skipped setup step: %q", out)
	}
}

// TestConvert_FileToGit_NoOriginIsNotAnError: no origin means there is nothing
// to configure a refspec on, which is a normal local repo, not a failure.
func TestConvert_FileToGit_NoOriginIsNotAnError(t *testing.T) {
	h := newHarness(t)
	h.addTask("One")
	if err := h.globals.git().Run("remote", "remove", "origin"); err != nil {
		t.Fatal(err)
	}

	if err := (&convertCmd{globals: h.globals, File: h.globals.TasksFile, ToGit: true}).Run(); err != nil {
		t.Fatalf("convert --to-git without origin: %v", err)
	}
	if !hasMeadsRefs(t, h) {
		t.Error("tasks should still have been imported without an origin")
	}
}

// TestConvert_FileToGit_TwiceIsStillRefused guards a PRE-EXISTING check (it
// passes with or without task 91's change): completing the setup must not
// weaken convert's own refusal to migrate into a populated namespace.
func TestConvert_FileToGit_TwiceIsStillRefused(t *testing.T) {
	h := newHarness(t)
	h.addTask("One")
	cmd := &convertCmd{globals: h.globals, File: h.globals.TasksFile, ToGit: true}
	if err := cmd.Run(); err != nil {
		t.Fatalf("first convert --to-git: %v", err)
	}
	err := cmd.Run()
	if err == nil {
		t.Fatal("second convert --to-git should refuse, got nil")
	}
	if !strings.Contains(err.Error(), "already has tasks") {
		t.Errorf("second convert --to-git error = %q, want it to mention existing tasks", err)
	}
}

// TestDoctorGit_RepairsMissingFetchRefspec covers the repos an older binary
// already migrated: they have task refs but no fetch refspec, and nothing
// could fix them - `md init --git` refuses on any ref under refs/meads/, and
// convert refuses on any existing task. `md doctor` is the way back.
func TestDoctorGit_RepairsMissingFetchRefspec(t *testing.T) {
	h := newHarness(t)
	h.addTask("One")
	if err := (&convertCmd{globals: h.globals, File: h.globals.TasksFile, ToGit: true}).Run(); err != nil {
		t.Fatalf("convert --to-git: %v", err)
	}
	// Roll the repo back to what the old convert left behind.
	if err := h.globals.git().Run("config", "--unset", "remote.origin.fetch", regexpQuote(meads.FetchRefspec)); err != nil {
		t.Fatalf("removing the fetch refspec: %v", err)
	}
	if hasFetchRefspec(t, h.dir) {
		t.Fatal("precondition: the refspec should be gone")
	}
	// Neither entry point can fix it, which is why doctor has to.
	if err := (&initCmd{globals: h.globals, Git: true}).Run(); err == nil {
		t.Error("precondition: init --git should still refuse on a populated namespace")
	}

	g := gitModeGlobals(h)
	if err := (&doctorCmd{globals: g}).Run(); err != nil {
		t.Fatalf("doctor: %v", err)
	}
	if !hasFetchRefspec(t, h.dir) {
		t.Errorf("doctor did not add %q to origin", meads.FetchRefspec)
	}

	// Idempotent: a second run must find nothing and add nothing.
	if err := (&doctorCmd{globals: g}).Run(); err != nil {
		t.Fatalf("doctor (2nd): %v", err)
	}
	lines, _ := gitConfigGetAll(t, h.dir, "remote.origin.fetch")
	n := 0
	for _, line := range lines {
		if line == meads.FetchRefspec {
			n++
		}
	}
	if n != 1 {
		t.Errorf("meads refspec appears %d times after two doctor runs, want 1 (%v)", n, lines)
	}
}

// TestDoctorGit_DoesNotConvertAFileModeRepo is the guard rail on the repair
// above. `md --git doctor` (or MEADS_GIT=1) in an ordinary file-mode repo must
// change nothing: seeding refs/meads/ there would flip every later command to
// git mode, and the TASKS.md tasks would vanish from `md list` while still
// sitting in the file.
func TestDoctorGit_DoesNotConvertAFileModeRepo(t *testing.T) {
	h := newHarness(t)
	h.addTask("Important file task")
	if hasMeadsRefs(t, h) {
		t.Fatal("precondition: a file-mode repo should have no refs/meads/")
	}

	out, err := captureStdout(t, (&doctorCmd{globals: gitModeGlobals(h)}).Run)
	if err != nil {
		t.Fatalf("doctor: %v", err)
	}
	if hasMeadsRefs(t, h) {
		t.Error("doctor created refs under refs/meads/, converting the repo to git mode")
	}
	if hasFetchRefspec(t, h.dir) {
		t.Error("doctor configured a meads fetch refspec on a file-mode repo")
	}
	if !strings.Contains(out, "no issues found") {
		t.Errorf("doctor output = %q, want it to report no issues found", out)
	}
}

// TestDoctorGit_DoesNotPreemptCloneAdoption: a fresh clone has no local
// refs/meads/* until something adopts origin's. Doctor writing there first
// would short-circuit that adoption permanently, leaving the clone showing an
// empty task list against a populated remote.
func TestDoctorGit_DoesNotPreemptCloneAdoption(t *testing.T) {
	h := newHarness(t)
	if err := (&initCmd{globals: h.globals, Git: true}).Run(); err != nil {
		t.Fatalf("init --git: %v", err)
	}
	if err := (&addCmd{globals: gitModeGlobals(h), Args: []string{"shared task"}}).Run(); err != nil {
		t.Fatalf("add: %v", err)
	}
	if err := h.globals.git().Run("push", "origin", meads.RefNamespace+"*:"+meads.RefNamespace+"*"); err != nil {
		t.Fatalf("push meads refs: %v", err)
	}

	origin, err := h.globals.git().Output("remote", "get-url", "origin")
	if err != nil {
		t.Fatal(err)
	}
	clone := t.TempDir() + "/clone"
	if err := (&meads.ExecGit{Dir: t.TempDir()}).Run("clone", "--quiet", strings.TrimSpace(origin), clone); err != nil {
		t.Fatalf("clone: %v", err)
	}
	cloneG := &globals{Git: &meads.ExecGit{Dir: clone}, Dir: clone, TasksFile: "TASKS.md", GitMode: true}
	if refs, err := meads.NewRefStore(cloneG.git()).ListRefs(meads.RefNamespace); err != nil || len(refs) > 0 {
		t.Fatalf("precondition: a fresh clone should have no local meads refs, got %v (err=%v)", refs, err)
	}

	if err := (&doctorCmd{globals: cloneG}).Run(); err != nil {
		t.Fatalf("doctor in a fresh clone: %v", err)
	}
	refs, err := meads.NewRefStore(cloneG.git()).ListRefs(meads.RefNamespace)
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) > 0 {
		t.Errorf("doctor wrote %v into a fresh clone, pre-empting adoption of origin's refs", refs)
	}
}

// regexpQuote escapes a refspec for `git config --unset`, whose value
// argument is a regular expression, not a literal.
func regexpQuote(s string) string {
	var b strings.Builder
	for _, r := range s {
		if strings.ContainsRune(`\.+*?()|[]{}^$`, r) {
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}

// hookOn reports whether a hook block is installed in h's repo.
func hookOn(t *testing.T, h *testHarness, b hookBlock) bool {
	t.Helper()
	on, err := b.installed(h.globals)
	if err != nil {
		t.Fatal(err)
	}
	return on
}

// TestConvert_FileToGit_RemovesFileModeHooks: auto-save and auto-delete exist
// only to serve a working-tree tasks file. Migrating is the moment they stop
// having a job, so that is where they go - otherwise they linger, spawning an
// `md` process per commit to conclude they have nothing to do.
func TestConvert_FileToGit_RemovesFileModeHooks(t *testing.T) {
	h := newHarness(t)
	h.addTask("One")
	if err := (&autoSaveCmd{globals: h.globals}).Run(); err != nil {
		t.Fatalf("auto-save: %v", err)
	}
	if err := (&autoDeleteCmd{globals: h.globals}).Run(); err != nil {
		t.Fatalf("auto-delete: %v", err)
	}
	if !hookOn(t, h, autoSaveBlock) || !hookOn(t, h, autoDeleteBlock) {
		t.Fatal("precondition: both hooks should be installed")
	}

	if err := (&convertCmd{globals: h.globals, File: h.globals.TasksFile, ToGit: true}).Run(); err != nil {
		t.Fatalf("convert --to-git: %v", err)
	}

	if hookOn(t, h, autoSaveBlock) {
		t.Error("convert --to-git left the auto-save hook installed")
	}
	if hookOn(t, h, autoDeleteBlock) {
		t.Error("convert --to-git left the auto-delete hook installed")
	}
}

// A migration must not disturb hooks it did not install. The pre-commit file
// is shared, so removing meads' blocks has to leave everyone else's alone.
func TestConvert_FileToGit_KeepsForeignHookContent(t *testing.T) {
	h := newHarness(t)
	h.addTask("One")
	if err := (&autoSaveCmd{globals: h.globals}).Run(); err != nil {
		t.Fatalf("auto-save: %v", err)
	}
	path, err := preCommitPath(h.globals)
	if err != nil {
		t.Fatal(err)
	}
	existing, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	const foreign = "# somebody else's hook\necho lint\n"
	if err := os.WriteFile(path, append([]byte(foreign), existing...), 0755); err != nil {
		t.Fatal(err)
	}

	if err := (&convertCmd{globals: h.globals, File: h.globals.TasksFile, ToGit: true}).Run(); err != nil {
		t.Fatalf("convert --to-git: %v", err)
	}

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(after), "echo lint") {
		t.Errorf("convert --to-git removed a foreign hook block:\n%s", after)
	}
	if strings.Contains(string(after), autoSaveBlock.marker) {
		t.Errorf("convert --to-git left meads' own block behind:\n%s", after)
	}
}

// No hooks installed is the common case and must stay silent - not an error,
// and not a "removed" line for something that was never there.
func TestConvert_FileToGit_NoHooksInstalledIsQuiet(t *testing.T) {
	h := newHarness(t)
	h.addTask("One")

	out, err := captureStdout(t, (&convertCmd{globals: h.globals, File: h.globals.TasksFile, ToGit: true}).Run)
	if err != nil {
		t.Fatalf("convert --to-git: %v", err)
	}
	if strings.Contains(out, "removed") {
		t.Errorf("convert --to-git reported removing a hook that was never installed:\n%s", out)
	}
}
