package meads

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

// Tests for git mode phase 6's local push-cadence state (TASKS #63):
// GitStore.ShouldPush/MarkPushed. Unlike gitstore_test.go/gitmutate_test.go,
// most of these don't need a real git repository at all - commonDir is just
// a plain directory ShouldPush/MarkPushed read and write files under - so
// t.TempDir() alone is enough for most cases. The one exception
// (TestGitStore_MarkPushed_NeverCreatesARef) deliberately uses a real repo
// to prove the state genuinely never touches refs/meads/*.

// --- ShouldPush: no record yet ---

func TestGitStore_ShouldPush_NoRecordIsDue(t *testing.T) {
	gs := NewGitStore(nil) // ShouldPush/MarkPushed touch only commonDir, never g.git/g.refs
	commonDir := t.TempDir()

	should, err := gs.ShouldPush(commonDir, time.Hour)
	if err != nil {
		t.Fatalf("ShouldPush with no prior record: %v", err)
	}
	if !should {
		t.Error("ShouldPush with no prior record = false, want true (never pushed -> due)")
	}
}

// --- MarkPushed then ShouldPush: interval respected ---

func TestGitStore_ShouldPush_RecentlyMarkedIsNotDue(t *testing.T) {
	gs := NewGitStore(nil)
	commonDir := t.TempDir()

	if err := gs.MarkPushed(commonDir); err != nil {
		t.Fatalf("MarkPushed: %v", err)
	}
	should, err := gs.ShouldPush(commonDir, time.Hour)
	if err != nil {
		t.Fatalf("ShouldPush: %v", err)
	}
	if should {
		t.Error("ShouldPush immediately after MarkPushed with a 1h interval = true, want false")
	}
}

func TestGitStore_ShouldPush_ElapsedIntervalIsDue(t *testing.T) {
	gs := NewGitStore(nil)
	commonDir := t.TempDir()

	if err := gs.MarkPushed(commonDir); err != nil {
		t.Fatalf("MarkPushed: %v", err)
	}
	// A near-zero interval must already have "elapsed" by the time
	// ShouldPush runs.
	should, err := gs.ShouldPush(commonDir, time.Nanosecond)
	if err != nil {
		t.Fatalf("ShouldPush: %v", err)
	}
	if !should {
		t.Error("ShouldPush with an effectively-zero interval = false, want true")
	}
}

// backdateLastPush writes commonDir's last-push file directly (bypassing
// MarkPushed, which always stamps "now") so a test can simulate "the last
// push attempt was long ago" deterministically, without sleeping.
func backdateLastPush(t *testing.T, commonDir string, when time.Time) {
	t.Helper()
	dir := filepath.Join(commonDir, PushStateDir)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("creating %s: %v", dir, err)
	}
	path := filepath.Join(dir, lastPushFile)
	content := strconv.FormatInt(when.UnixNano(), 10)
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}
}

func TestGitStore_ShouldPush_BackdatedRecordIsDue(t *testing.T) {
	gs := NewGitStore(nil)
	commonDir := t.TempDir()
	backdateLastPush(t, commonDir, time.Now().Add(-2*time.Hour))

	should, err := gs.ShouldPush(commonDir, time.Hour)
	if err != nil {
		t.Fatalf("ShouldPush: %v", err)
	}
	if !should {
		t.Error("ShouldPush with a 2h-old record and a 1h interval = false, want true")
	}
}

func TestGitStore_ShouldPush_JustUnderIntervalIsNotDue(t *testing.T) {
	gs := NewGitStore(nil)
	commonDir := t.TempDir()
	backdateLastPush(t, commonDir, time.Now().Add(-30*time.Minute))

	should, err := gs.ShouldPush(commonDir, time.Hour)
	if err != nil {
		t.Fatalf("ShouldPush: %v", err)
	}
	if should {
		t.Error("ShouldPush with a 30m-old record and a 1h interval = true, want false")
	}
}

// --- corrupt/malformed record: fail open ---

func TestGitStore_ShouldPush_CorruptRecordFailsOpen(t *testing.T) {
	gs := NewGitStore(nil)
	commonDir := t.TempDir()
	dir := filepath.Join(commonDir, PushStateDir)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("creating %s: %v", dir, err)
	}
	if err := os.WriteFile(filepath.Join(dir, lastPushFile), []byte("not-a-timestamp"), 0644); err != nil {
		t.Fatalf("writing corrupt record: %v", err)
	}

	should, err := gs.ShouldPush(commonDir, time.Hour)
	if err != nil {
		t.Fatalf("ShouldPush on a corrupt record should fail OPEN (no error), got: %v", err)
	}
	if !should {
		t.Error("ShouldPush on a corrupt record = false, want true (fail open, not permanently wedged)")
	}
}

// --- MarkPushed: creates the directory and a parseable file ---

func TestGitStore_MarkPushed_CreatesStateDirAndParseableFile(t *testing.T) {
	gs := NewGitStore(nil)
	commonDir := t.TempDir()

	before := time.Now()
	if err := gs.MarkPushed(commonDir); err != nil {
		t.Fatalf("MarkPushed: %v", err)
	}
	after := time.Now()

	path := filepath.Join(commonDir, PushStateDir, lastPushFile)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s after MarkPushed: %v", path, err)
	}
	ns, err := strconv.ParseInt(string(data), 10, 64)
	if err != nil {
		t.Fatalf("last-push content %q is not a parseable integer: %v", data, err)
	}
	got := time.Unix(0, ns)
	if got.Before(before) || got.After(after) {
		t.Errorf("MarkPushed timestamp = %v, want between %v and %v", got, before, after)
	}
}

func TestGitStore_MarkPushed_Idempotent_LaterCallWins(t *testing.T) {
	gs := NewGitStore(nil)
	commonDir := t.TempDir()

	backdateLastPush(t, commonDir, time.Now().Add(-24*time.Hour))
	if err := gs.MarkPushed(commonDir); err != nil {
		t.Fatalf("MarkPushed: %v", err)
	}
	should, err := gs.ShouldPush(commonDir, time.Hour)
	if err != nil {
		t.Fatalf("ShouldPush: %v", err)
	}
	if should {
		t.Error("a fresh MarkPushed should overwrite an old backdated record, but ShouldPush still reports due")
	}
}

// --- last-push state is local filesystem state, never a ref ---

// TestGitStore_MarkPushed_NeverCreatesARef proves MarkPushed touches only
// the filesystem under the real git common dir, never refs/meads/* (or any
// ref at all) - the whole point of storing push cadence outside the ref
// store (see ShouldPush's doc comment): it is per-clone state that must
// never be pushed, fetched, or shared via the ref namespace every clone
// already synchronizes through.
func TestGitStore_MarkPushed_NeverCreatesARef(t *testing.T) {
	gs, rs, dir := newGitStoreRepo(t)
	commonDir := runGit(t, dir, "rev-parse", "--git-common-dir")
	if !filepath.IsAbs(commonDir) {
		commonDir = filepath.Join(dir, commonDir)
	}

	refsBefore, err := rs.ListRefs("refs/")
	if err != nil {
		t.Fatalf("ListRefs before MarkPushed: %v", err)
	}

	if err := gs.MarkPushed(commonDir); err != nil {
		t.Fatalf("MarkPushed: %v", err)
	}

	// The state file must exist as a plain file on disk...
	path := filepath.Join(commonDir, PushStateDir, lastPushFile)
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("last-push file missing at %s: %v", path, err)
	}

	// ...and the set of refs in the repository must be EXACTLY unchanged -
	// not just "no new refs/meads/* entry", but no new ref anywhere at all.
	refsAfter, err := rs.ListRefs("refs/")
	if err != nil {
		t.Fatalf("ListRefs after MarkPushed: %v", err)
	}
	if len(refsAfter) != len(refsBefore) {
		t.Fatalf("ref count changed from %d to %d after MarkPushed, want unchanged: before=%v after=%v", len(refsBefore), len(refsAfter), refsBefore, refsAfter)
	}
	for name := range refsAfter {
		if _, ok := refsBefore[name]; !ok {
			t.Errorf("MarkPushed created a new ref %s, want none", name)
		}
	}
}
