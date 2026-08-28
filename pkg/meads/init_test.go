package meads

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Tests for InitTasks and EnsureFetchRefspec (init.go), the library form of
// `md init`: the file backends' empty-file write, git mode's config-ref
// seeding and refusals, and the fetch-refspec invariants (additive,
// idempotent, lands in refs/meads-remote/*, and never touches
// remote.origin.push). cmd/md/init_git_test.go covers the CLI wrapper and
// the behavioural "plain git push still works" regression guard; these pin
// the library layer itself.

func TestInitTasks_Markdown(t *testing.T) {
	dir := t.TempDir()
	res, err := InitTasks(dir, BackendMarkdown)
	if err != nil {
		t.Fatalf("InitTasks: %v", err)
	}
	content, err := os.ReadFile(filepath.Join(dir, "TASKS.md"))
	if err != nil {
		t.Fatalf("TASKS.md not created: %v", err)
	}
	if len(content) != 0 {
		t.Errorf("TASKS.md content = %q, want empty", content)
	}
	if res.Tasks.Backend() != BackendMarkdown {
		t.Errorf("Tasks.Backend() = %v, want BackendMarkdown", res.Tasks.Backend())
	}
	exists, err := res.Tasks.Exists()
	if err != nil || !exists {
		t.Errorf("Exists() after init = %v, %v; want true, nil", exists, err)
	}
	if _, err := InitTasks(dir, BackendMarkdown); err == nil {
		t.Error("second InitTasks should refuse (file exists), got nil")
	}
}

func TestInitTasks_CSV(t *testing.T) {
	dir := t.TempDir()
	res, err := InitTasks(dir, BackendCSV)
	if err != nil {
		t.Fatalf("InitTasks: %v", err)
	}
	content, err := os.ReadFile(filepath.Join(dir, "TASKS.csv"))
	if err != nil {
		t.Fatalf("TASKS.csv not created: %v", err)
	}
	if string(content) != InitCSV() {
		t.Errorf("TASKS.csv content = %q, want the csv header %q", content, InitCSV())
	}
	if res.Tasks.Backend() != BackendCSV {
		t.Errorf("Tasks.Backend() = %v, want BackendCSV", res.Tasks.Backend())
	}
}

func TestInitTasks_Git(t *testing.T) {
	dir := newDetectRepo(t)
	res, err := InitTasks(dir, BackendGit)
	if err != nil {
		t.Fatalf("InitTasks: %v", err)
	}
	// No origin was configured on this repo, so the refspec step must have
	// skipped cleanly, not failed.
	if res.FetchRefspec != FetchRefspecNoOrigin {
		t.Errorf("FetchRefspec = %v, want FetchRefspecNoOrigin", res.FetchRefspec)
	}
	// The default config ref is written (making the namespace non-empty and
	// the init detectable), and no placeholder task is seeded.
	gt, ok := res.Tasks.(GitTasks)
	if !ok {
		t.Fatalf("InitTasks(BackendGit).Tasks = %T, want GitTasks", res.Tasks)
	}
	cfg, err := gt.GitStore().Config()
	if err != nil {
		t.Fatalf("Config() after init: %v", err)
	}
	if want := DefaultConfig(); cfg != want {
		t.Errorf("Config() after init = %+v, want defaults %+v", cfg, want)
	}
	content, _, err := gt.GitStore().refs.ReadFileAtRef(ConfigRef, ConfigFileName)
	if err != nil {
		t.Fatalf("reading config.json after init: %v", err)
	}
	var stored map[string]any
	if err := json.Unmarshal(content, &stored); err != nil {
		t.Fatalf("parsing config.json after init: %v", err)
	}
	if got := stored[gitRefProtocolVersionKey]; got != float64(GitRefProtocolVersion) {
		t.Errorf("%s after init = %v, want %d", gitRefProtocolVersionKey, got, GitRefProtocolVersion)
	}
	if tasks, err := res.Tasks.Get(nil); err != nil || len(tasks) != 0 {
		t.Errorf("Get(nil) after init = %v, %v; want no tasks", tasks, err)
	}
	// Detect must now agree the repo is git mode.
	if b, _ := Detect(dir); b != BackendGit {
		t.Errorf("Detect after init = %v, want BackendGit", b)
	}
	// A second init refuses, even though zero tasks exist.
	if _, err := InitTasks(dir, BackendGit); err == nil {
		t.Error("second InitTasks should refuse (already initialized), got nil")
	} else if !strings.Contains(err.Error(), "already initialized") {
		t.Errorf("second InitTasks error = %q, want it to mention \"already initialized\"", err.Error())
	}
}

func TestInitTasks_GitOutsideRepoErrors(t *testing.T) {
	if _, err := InitTasks(t.TempDir(), BackendGit); err == nil {
		t.Fatal("InitTasks(BackendGit) outside a git repository should error, got nil")
	} else if !strings.Contains(err.Error(), "not in a git repository") {
		t.Errorf("error = %q, want it to mention \"not in a git repository\"", err.Error())
	}
}

func TestInitTasks_UnknownBackendErrors(t *testing.T) {
	if _, err := InitTasks(t.TempDir(), Backend(99)); err == nil {
		t.Error("InitTasks(unknown) should error, got nil")
	}
}

// TestEnsureFetchRefspec pins the two invariants task 79 calls out: the
// refspec is additive (existing fetch lines survive, and it lands in
// refs/meads-remote/*, never refs/meads/*) and remote.origin.push is never
// touched - plus idempotency via the outcome values.
func TestEnsureFetchRefspec(t *testing.T) {
	// A working repo with a bare origin configured.
	originDir := t.TempDir()
	runGit(t, originDir, "init", "--bare", "-b", "main")
	dir := t.TempDir()
	runGit(t, dir, "init", "-b", "main")
	runGit(t, dir, "config", "user.name", "Test")
	runGit(t, dir, "config", "user.email", "test@test.com")
	runGit(t, dir, "remote", "add", "origin", originDir)
	git := &ExecGit{Dir: dir}

	// Baseline: git's own default fetch refspec must survive untouched.
	before := strings.TrimSpace(runGit(t, dir, "config", "--get-all", "remote.origin.fetch"))
	if before == "" {
		t.Fatal("precondition: expected git's default fetch refspec to be set")
	}

	outcome, err := EnsureFetchRefspec(git)
	if err != nil || outcome != FetchRefspecAdded {
		t.Fatalf("EnsureFetchRefspec = %v, %v; want FetchRefspecAdded, nil", outcome, err)
	}
	after := strings.Split(strings.TrimSpace(runGit(t, dir, "config", "--get-all", "remote.origin.fetch")), "\n")
	if len(after) != 2 {
		t.Fatalf("remote.origin.fetch = %v, want the default plus exactly one more", after)
	}
	foundDefault, foundMeads := false, false
	for _, line := range after {
		if line == before {
			foundDefault = true
		}
		if line == FetchRefspec {
			foundMeads = true
		}
		// The refspec must land in the remote-tracking namespace, never
		// force-update the local refs/meads/* directly.
		if strings.HasSuffix(line, ":"+RefNamespace+"*") {
			t.Errorf("fetch refspec %q lands in the local %s namespace, want %s", line, RefNamespace, RemoteRefNamespace)
		}
	}
	if !foundDefault || !foundMeads {
		t.Errorf("remote.origin.fetch = %v, want both the default and %q", after, FetchRefspec)
	}
	// remote.origin.push must never be configured (an unset key exits 1,
	// so read it through the error-returning Git interface, not runGit).
	if out, _ := git.Output("config", "--get-all", "remote.origin.push"); out != "" {
		t.Errorf("remote.origin.push = %q, want never set", out)
	}

	// Idempotent: a second call changes nothing and says so.
	outcome, err = EnsureFetchRefspec(git)
	if err != nil || outcome != FetchRefspecAlreadyPresent {
		t.Fatalf("EnsureFetchRefspec (2nd) = %v, %v; want FetchRefspecAlreadyPresent, nil", outcome, err)
	}
	again := strings.TrimSpace(runGit(t, dir, "config", "--get-all", "remote.origin.fetch"))
	if again != strings.Join(after, "\n") {
		t.Errorf("remote.origin.fetch changed on a repeat call: %v -> %v", after, again)
	}
}

func TestEnsureFetchRefspec_NoOrigin(t *testing.T) {
	dir := newDetectRepo(t) // no remote configured at all
	outcome, err := EnsureFetchRefspec(&ExecGit{Dir: dir})
	if err != nil || outcome != FetchRefspecNoOrigin {
		t.Errorf("EnsureFetchRefspec with no origin = %v, %v; want FetchRefspecNoOrigin, nil", outcome, err)
	}
}
