package meads

import (
	"context"
	"errors"
	"hash/fnv"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/go-git/go-billy/v5/memfs"
	"github.com/go-git/go-billy/v5/util"
)

// Tests for the unified Tasks seam (tasks.go): the Backend enum, and the
// FileTasks/GitTasks adapters' newer methods (Backend/Location/Exists/
// Revision/Sync) plus the GitTasks shim semantics (Add discarding Create's
// Task, Update's always-write wrap, Delete discarding SoftDelete's Task,
// GetHistory == LoadAll, GetWithHistory as a direct read). CRUD parity
// between the backends themselves is already covered by gitstore_test.go;
// these tests pin the adapter layer on top.

func TestBackendString(t *testing.T) {
	cases := map[Backend]string{
		BackendMarkdown: "md",
		BackendCSV:      "csv",
		BackendGit:      "git",
		Backend(99):     "Backend(99)",
	}
	for b, want := range cases {
		if got := b.String(); got != want {
			t.Errorf("Backend(%d).String() = %q, want %q", int(b), got, want)
		}
	}
}

// --- FileTasks ---

func TestFileTasks_Backend(t *testing.T) {
	if got := NewFileTasks(NewStore(memfs.New(), "TASKS.md"), nil).Backend(); got != BackendMarkdown {
		t.Errorf("TASKS.md backend = %v, want BackendMarkdown", got)
	}
	if got := NewFileTasks(NewStore(memfs.New(), "TASKS.csv"), nil).Backend(); got != BackendCSV {
		t.Errorf("TASKS.csv backend = %v, want BackendCSV", got)
	}
}

func TestFileTasks_Location(t *testing.T) {
	// memfs roots at "/", so the location is the root-joined path.
	if got := NewFileTasks(NewStore(memfs.New(), "TASKS.md"), nil).Location(); got != "/TASKS.md" {
		t.Errorf("memfs Location() = %q, want /TASKS.md", got)
	}
	// A real file store resolves to the absolute on-disk path.
	abs := filepath.Join(t.TempDir(), "TASKS.md")
	if got := NewFileTasks(NewFileStore(abs), nil).Location(); got != abs {
		t.Errorf("osfs Location() = %q, want %q", got, abs)
	}
}

func TestFileTasks_Exists(t *testing.T) {
	ft := NewFileTasks(newTestStore(t, ""), nil)
	exists, err := ft.Exists()
	if err != nil || exists {
		t.Fatalf("Exists() before any write = %v, %v; want false, nil", exists, err)
	}
	if _, err := ft.Add(Task{Title: "x"}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	exists, err = ft.Exists()
	if err != nil || !exists {
		t.Fatalf("Exists() after Add = %v, %v; want true, nil", exists, err)
	}
}

// TestFileTasks_Revision pins the exact hash shape rais's ProjectMeads.Hash
// computes (fnv64a of the raw file bytes, hex via strconv.FormatUint 16) so
// the two can never drift apart, and that the value tracks writes.
func TestFileTasks_Revision(t *testing.T) {
	fs := memfs.New()
	content := "# Tasks\n\n## 1. a task\n\n* status: open\n"
	if err := util.WriteFile(fs, "TASKS.md", []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	ft := NewFileTasks(NewStore(fs, "TASKS.md"), nil)

	h := fnv.New64a()
	h.Write([]byte(content))
	want := strconv.FormatUint(h.Sum64(), 16)

	rev, err := ft.Revision()
	if err != nil {
		t.Fatalf("Revision: %v", err)
	}
	if rev != want {
		t.Errorf("Revision() = %q, want fnv64a-of-bytes %q", rev, want)
	}

	// Stable across calls with no write in between...
	again, err := ft.Revision()
	if err != nil || again != rev {
		t.Errorf("Revision() across calls = %q, %v; want unchanged %q", again, err, rev)
	}
	// ...and different after a write.
	if _, err := ft.Add(Task{Title: "y"}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	after, err := ft.Revision()
	if err != nil {
		t.Fatalf("Revision after Add: %v", err)
	}
	if after == rev {
		t.Errorf("Revision() after Add = %q, want a different token", after)
	}
}

func TestFileTasks_Revision_MissingFileErrors(t *testing.T) {
	ft := NewFileTasks(NewStore(memfs.New(), "TASKS.md"), nil)
	if _, err := ft.Revision(); err == nil {
		t.Fatal("Revision() with no tasks file should error, got nil")
	}
}

func TestFileTasks_SyncIsNoOp(t *testing.T) {
	ft := NewFileTasks(newTestStore(t, ""), nil)
	report, err := ft.Sync(context.Background())
	if err != nil {
		t.Errorf("FileTasks.Sync = %v, want nil (nothing to publish)", err)
	}
	// Non-nil and empty, so a caller can inspect the report without first
	// asking which backend it holds.
	if report == nil || report.Integrate == nil {
		t.Fatalf("FileTasks.Sync report = %+v, want a non-nil report with a non-nil Integrate", report)
	}
	if !report.Integrate.empty() || report.PushOutput != "" || report.Rejected {
		t.Errorf("FileTasks.Sync report = %+v, want it empty", report)
	}
}

// TestFileTasks_FSLocator proves the FS/Path delegates survive the adapter,
// which is what pkg/webui's fileLocator (fsnotify watcher, startup banner)
// discovers structurally.
func TestFileTasks_FSLocator(t *testing.T) {
	store := NewStore(memfs.New(), "TASKS.md")
	ft := NewFileTasks(store, nil)
	if ft.FS() != store.FS() || ft.Path() != store.Path() {
		t.Errorf("FS/Path = (%v, %q), want the underlying Store's (%v, %q)",
			ft.FS(), ft.Path(), store.FS(), store.Path())
	}
}

// --- GitTasks ---

func TestGitTasks_BackendLocation(t *testing.T) {
	gs, _, _ := newGitStoreRepo(t)
	gt := NewGitTasks(gs)
	if got := gt.Backend(); got != BackendGit {
		t.Errorf("Backend() = %v, want BackendGit", got)
	}
	if got := gt.Location(); got != "refs/meads/tasks/*" {
		t.Errorf("Location() = %q, want refs/meads/tasks/*", got)
	}
}

func TestGitTasks_Exists(t *testing.T) {
	gs, _, _ := newGitStoreRepo(t)
	gt := NewGitTasks(gs)
	// A bare repo has no refs/meads/* at all: not initialised.
	exists, err := gt.Exists()
	if err != nil || exists {
		t.Fatalf("Exists() in a bare repo = %v, %v; want false, nil", exists, err)
	}
	// Any ref under refs/meads/ counts - including the config ref alone,
	// with zero tasks, which is how a fresh `init --git` bootstraps.
	if err := gs.SetConfig(Config{PushInterval: "1h"}); err != nil {
		t.Fatalf("SetConfig: %v", err)
	}
	exists, err = gt.Exists()
	if err != nil || !exists {
		t.Fatalf("Exists() with only a config ref = %v, %v; want true, nil", exists, err)
	}
}

func TestGitTasks_Revision(t *testing.T) {
	gs, _, _ := newGitStoreRepo(t)
	gt := NewGitTasks(gs)

	empty, err := gt.Revision()
	if err != nil {
		t.Fatalf("Revision: %v", err)
	}
	id, err := gt.Add(Task{Title: "x"})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	one, err := gt.Revision()
	if err != nil {
		t.Fatalf("Revision: %v", err)
	}
	if one == empty {
		t.Error("Revision() after first Add should differ from the no-refs token")
	}
	if err := gt.Update(id, func(task *Task) { task.Title = "y" }); err != nil {
		t.Fatalf("Update: %v", err)
	}
	two, err := gt.Revision()
	if err != nil {
		t.Fatalf("Revision: %v", err)
	}
	if two == one {
		t.Error("Revision() after Update should differ (the ref moved)")
	}
	again, err := gt.Revision()
	if err != nil || again != two {
		t.Errorf("Revision() across calls = %q, %v; want unchanged %q", again, err, two)
	}
}

// TestGitTasks_ShimSemantics pins the adapter reconciliations GitTasks
// documents: Add returns just the id, Update's fn-shaped mutate always
// writes, Delete soft-deletes with the Task discarded, GetHistory is
// LoadAll, and GetWithHistory resolves a deleted id directly.
func TestGitTasks_ShimSemantics(t *testing.T) {
	gs, _, _ := newGitStoreRepo(t)
	gt := NewGitTasks(gs)

	id, err := gt.Add(Task{Title: "shim", Status: "open"})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if id != 1 {
		t.Errorf("first Add id = %d, want 1", id)
	}

	if err := gt.Update(id, func(task *Task) { task.Title = "renamed" }); err != nil {
		t.Fatalf("Update: %v", err)
	}
	got, err := gt.Get([]int{id})
	if err != nil || len(got) != 1 || got[0].Title != "renamed" {
		t.Fatalf("Get after Update = %v, %v; want the renamed task", got, err)
	}

	if err := gt.Delete(id); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := gt.Get([]int{id}); err == nil {
		t.Error("Get after Delete should error (soft-deleted)")
	}
	// GetWithHistory resolves the deleted id straight from its kept ref.
	gwh, err := gt.GetWithHistory([]int{id})
	if err != nil || len(gwh) != 1 || !gwh[0].Deleted {
		t.Errorf("GetWithHistory after Delete = %+v, %v; want the one deleted task", gwh, err)
	}
	// GetHistory is LoadAll: every task ever created, including deleted.
	hist, err := gt.GetHistory()
	if err != nil || len(hist) != 1 || hist[0].ID != id {
		t.Errorf("GetHistory = %+v, %v; want exactly task %d (deleted included)", hist, err, id)
	}
	loadAll, err := gs.LoadAll()
	if err != nil || len(loadAll) != len(hist) {
		t.Fatalf("LoadAll = %+v, %v; want same length as GetHistory", loadAll, err)
	}
}

func TestGitTasks_SyncWithoutOriginErrors(t *testing.T) {
	gs, _, _ := newGitStoreRepo(t)
	gt := NewGitTasks(gs)
	if _, err := gt.Add(Task{Title: "x"}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	// No origin remote is configured, so the push must fail - Sync reports
	// it rather than silently pretending the refs were published.
	_, err := gt.Sync(context.Background())
	if err == nil {
		t.Fatal("Sync with no origin remote should error, got nil")
	}
	if !strings.Contains(err.Error(), "refs/meads/") {
		t.Errorf("Sync error = %q, want it to name the refs/meads/* push", err)
	}
}

func TestGitTasks_SyncCancelledContext(t *testing.T) {
	gs, _, _ := newGitStoreRepo(t)
	gt := NewGitTasks(gs)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := gt.Sync(ctx); err == nil {
		t.Fatal("Sync with a cancelled context should error before pushing, got nil")
	}
}

// TestGitTasks_Sync_ReportsReHomeToCaller is task 87's point: a sync that
// RENAMES a local task must say so through the Tasks interface. Before
// this, Sync discarded Pull's IntegrateReport and returned a bare error, so
// cmd/md (which calls Integrate itself) could print the re-home while a
// library caller holding that id in its own durable state - rais keys agent
// badges and .plans/<id>/ directories on it - saw nothing at all.
//
// The partition is the same one TestGitStore_Pull_TwoCloneRoundTrip builds:
// two clones independently land on id 1, so clone2's sync re-homes its own
// version at id 2 and lets origin's keep the id.
func TestGitTasks_Sync_ReportsReHomeToCaller(t *testing.T) {
	bare := t.TempDir()
	runGit(t, bare, "init", "--bare", "-b", "main")

	c1 := newDetectRepo(t)
	gs1 := NewGitStore(&ExecGit{Dir: c1})
	runGit(t, c1, "remote", "add", "origin", bare)
	if _, err := EnsureFetchRefspec(&ExecGit{Dir: c1}); err != nil {
		t.Fatalf("EnsureFetchRefspec (clone1): %v", err)
	}
	if err := gs1.SetConfig(DefaultConfig()); err != nil {
		t.Fatalf("SetConfig (clone1): %v", err)
	}
	if _, err := gs1.Create(Task{Title: "clone1's task", Status: "open"}); err != nil {
		t.Fatalf("Create (clone1): %v", err)
	}
	runGit(t, c1, "push", "origin", RefNamespace+"*:"+RefNamespace+"*")

	c2 := newDetectRepo(t)
	gs2 := NewGitStore(&ExecGit{Dir: c2})
	runGit(t, c2, "remote", "add", "origin", bare)
	if _, err := EnsureFetchRefspec(&ExecGit{Dir: c2}); err != nil {
		t.Fatalf("EnsureFetchRefspec (clone2): %v", err)
	}
	if err := gs2.SetConfig(DefaultConfig()); err != nil {
		t.Fatalf("SetConfig (clone2): %v", err)
	}
	if _, err := gs2.Create(Task{Title: "clone2's task", Status: "open"}); err != nil {
		t.Fatalf("Create (clone2): %v", err)
	}

	report, err := NewGitTasks(gs2).Sync(t.Context())
	if err != nil {
		t.Fatalf("Sync (clone2): %v", err)
	}
	if report == nil || report.Integrate == nil {
		t.Fatalf("Sync report = %+v, want a non-nil report with a non-nil Integrate", report)
	}
	fixes := report.Integrate.Fixes
	if len(fixes) != 1 || fixes[0].OldID != 1 || fixes[0].NewID != 2 || fixes[0].Kind != DoctorFixDuplicate {
		t.Fatalf("Integrate.Fixes = %+v, want exactly one {OldID:1 NewID:2 Kind:duplicate}", fixes)
	}
	// The re-home really happened, so a caller acting on the report (moving
	// its own id-keyed state) is acting on a fact, not a prediction.
	got, err := gs2.Get([]int{2})
	if err != nil || len(got) != 1 || got[0].Title != "clone2's task" {
		t.Errorf("task 2 on clone2 = %v, %v; want clone2's re-homed task", got, err)
	}
	// The pull made the push converge, and its output was captured rather
	// than discarded - that capture is what makes Rejected meaningful.
	if report.Rejected {
		t.Errorf("Rejected = true, want false: the pull reconciled before the push")
	}
	if report.PushOutput == "" {
		t.Error("PushOutput is empty, want the push's porcelain output captured")
	}
}

// TestPushRejected pins the porcelain reasons that mean "origin moved"
// against everything else a push can fail with - the distinction a caller
// needs to tell "try again next sync" from "the remote is broken".
func TestPushRejected(t *testing.T) {
	cases := []struct {
		name   string
		output string
		want   bool
	}{
		{"clean push", "To /tmp/bare\n=\trefs/meads/tasks/1:refs/meads/tasks/1\t[up to date]\nDone\n", false},
		{"fetch first", "To /tmp/bare\n!\trefs/meads/tasks/1:refs/meads/tasks/1\t[rejected] (fetch first)\nDone\n", true},
		{"non-fast-forward", "!\trefs/meads/tasks/1:refs/meads/tasks/1\t[rejected] (non-fast-forward)\n", true},
		{"stale info", "!\trefs/meads/config:refs/meads/config\t[rejected] (stale info)\n", true},
		{"auth failure", "fatal: Authentication failed for 'https://example.com/x.git/'\n", false},
		{"offline", "fatal: unable to access 'https://example.com/x.git/': Could not resolve host\n", false},
		// A host's own free-text refusal is never matched: it varies by
		// host and is not git's porcelain vocabulary.
		{"host free text", "!\trefs/meads/tasks/1:refs/meads/tasks/1\t[remote rejected] (pre-receive hook declined)\n", false},
		{"empty", "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := PushRejected(c.output); got != c.want {
				t.Errorf("PushRejected(%q) = %v, want %v", c.output, got, c.want)
			}
		})
	}
}

// fetchFailingGit fails every `git fetch` and counts every `git push`,
// delegating everything else. It deliberately implements NEITHER ContextGit
// NOR CombinedOutputGit, so runContext/combinedOutputContext take their
// documented fallbacks through Run/Output - which is exactly how the
// overrides below get to see the fetch and the push.
type fetchFailingGit struct {
	Git
	mu        sync.Mutex
	pushCalls int
}

var errSimulatedFetch = errors.New("simulated fetch failure")

func (g *fetchFailingGit) Run(args ...string) error {
	if len(args) > 0 && args[0] == "fetch" {
		return errSimulatedFetch
	}
	return g.Git.Run(args...)
}

func (g *fetchFailingGit) Output(args ...string) (string, error) {
	if len(args) > 0 && args[0] == "fetch" {
		return "", errSimulatedFetch
	}
	if len(args) > 0 && args[0] == "push" {
		g.mu.Lock()
		g.pushCalls++
		g.mu.Unlock()
	}
	return g.Git.Output(args...)
}

func (g *fetchFailingGit) pushes() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.pushCalls
}

// TestGitTasks_Sync_FailedPullStillPushes pins the policy Sync inherited
// from cmd/md's auto-push when the two were merged (task 80): a fetch that
// fails must NOT cost this clone the chance to publish work it has already
// committed. The fetch is the half most likely to fail for a reason the
// push does not share, and a rejected push is now reported and reconciled
// by the next sync rather than being a dead end.
//
// What a failed pull DOES skip is the integration - reconciling against
// remote-tracking refs a failed fetch just left stale could renumber
// against facts that are no longer true - so the report comes back empty.
func TestGitTasks_Sync_FailedPullStillPushes(t *testing.T) {
	bare := t.TempDir()
	runGit(t, bare, "init", "--bare", "-b", "main")

	dir := newDetectRepo(t)
	runGit(t, dir, "remote", "add", "origin", bare)
	spy := &fetchFailingGit{Git: &ExecGit{Dir: dir}}
	gs := NewGitStore(spy)
	if err := gs.SetConfig(DefaultConfig()); err != nil {
		t.Fatalf("SetConfig: %v", err)
	}
	if _, err := gs.Create(Task{Title: "committed locally", Status: "open"}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	report, err := NewGitTasks(gs).Sync(t.Context())
	if !errors.Is(err, errSimulatedFetch) {
		t.Fatalf("Sync err = %v, want it to carry the fetch failure", err)
	}
	if spy.pushes() != 1 {
		t.Errorf("push attempts = %d, want 1: a failed fetch must not skip the push", spy.pushes())
	}
	if report == nil || !report.Integrate.empty() {
		t.Errorf("Integrate = %+v, want empty: integration is skipped against a stale fetch", report)
	}
	// The push really landed, so "publish anyway" is a real outcome and not
	// just an attempted one.
	out, _ := (&ExecGit{Dir: bare}).Output("for-each-ref", "--format=%(refname)", TasksRefPrefix)
	if !strings.Contains(out, TasksRefPrefix+"1") {
		t.Errorf("origin task refs = %q, want the locally committed task pushed", out)
	}
}
