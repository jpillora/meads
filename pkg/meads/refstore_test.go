package meads

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// Tests for RefStore, the git-ref storage layer behind git mode (TASKS #30).
// RefStore does git plumbing (hash-object/mktree/commit-tree/update-ref
// style operations) to store JSON blobs in refs without ever touching the
// working tree or index, so these tests run against real temporary git
// repositories via ExecGit rather than a fake - the guarantee under test is
// about actual git state on disk.

// --- helpers ---

// newRefStoreRepo creates a temporary git repository with one committed
// file and returns a RefStore rooted at it plus the repo directory.
func newRefStoreRepo(t *testing.T) (*RefStore, string) {
	t.Helper()
	dir := t.TempDir()
	runGit(t, dir, "init", "-b", "main")
	runGit(t, dir, "config", "user.name", "Test")
	runGit(t, dir, "config", "user.email", "test@test.com")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("hello\n"), 0644); err != nil {
		t.Fatalf("writing seed file: %v", err)
	}
	runGit(t, dir, "add", "README.md")
	runGit(t, dir, "commit", "-m", "initial")
	return NewRefStore(&ExecGit{Dir: dir}), dir
}

// runGit runs a git command in dir and fails the test on error, returning
// trimmed combined output.
func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s failed: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// makeCommit writes content as tasks.json through a full
// blob->tree->commit chain and returns the resulting commit OID. It does
// not move any ref.
func makeCommit(t *testing.T, rs *RefStore, content string, parents []OID) OID {
	t.Helper()
	blob, err := rs.WriteBlob([]byte(content))
	if err != nil {
		t.Fatalf("WriteBlob: %v", err)
	}
	tree, err := rs.WriteTree([]TreeEntry{{Mode: "100644", Type: "blob", OID: blob, Name: "tasks.json"}})
	if err != nil {
		t.Fatalf("WriteTree: %v", err)
	}
	commit, err := rs.WriteCommit(tree, parents, "update tasks")
	if err != nil {
		t.Fatalf("WriteCommit: %v", err)
	}
	return commit
}

// listWorkingTreeFiles returns the sorted, repo-relative paths of every
// file in dir, excluding anything under .git.
func listWorkingTreeFiles(t *testing.T, dir string) []string {
	t.Helper()
	var files []string
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, relErr := filepath.Rel(dir, path)
		if relErr != nil {
			return relErr
		}
		if rel == "." {
			return nil
		}
		if info.IsDir() {
			if rel == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		files = append(files, rel)
		return nil
	})
	if err != nil {
		t.Fatalf("walking %s: %v", dir, err)
	}
	slices.Sort(files)
	return files
}

// --- 1. worktree and index are untouched ---

func TestRefStore_WorktreeAndIndexUntouched(t *testing.T) {
	rs, dir := newRefStoreRepo(t)

	// Establish the baseline. Note that git status/diff-style commands
	// refresh the index's stat cache and can rewrite .git/index themselves
	// (git's "racy git" protection kicks in when tracked-file mtimes are
	// within the same filesystem mtime tick as the index, which is
	// routinely true in a fast test) - so run the baseline status/rev-parse
	// checks, THEN take the "before" mtime snapshot, and do not invoke any
	// further git porcelain command until immediately after the "after"
	// snapshot below. Otherwise the act of checking would itself move the
	// mtime and produce a false failure unrelated to RefStore.
	statusBefore := runGit(t, dir, "status", "--porcelain")
	if statusBefore != "" {
		t.Fatalf("repo not clean before test: %q", statusBefore)
	}
	filesBefore := listWorkingTreeFiles(t, dir)
	headBefore := runGit(t, dir, "rev-parse", "HEAD")

	indexPath := filepath.Join(dir, ".git", "index")
	infoBefore, err := os.Stat(indexPath)
	if err != nil {
		t.Fatalf("stat %s: %v", indexPath, err)
	}

	// Give the filesystem a chance to observe a changed mtime, so a false
	// negative from clock-granularity coincidence is unlikely.
	time.Sleep(20 * time.Millisecond)

	blob, err := rs.WriteBlob([]byte(`{"tasks":[]}` + "\n"))
	if err != nil {
		t.Fatalf("WriteBlob: %v", err)
	}
	tree, err := rs.WriteTree([]TreeEntry{{Mode: "100644", Type: "blob", OID: blob, Name: "tasks.json"}})
	if err != nil {
		t.Fatalf("WriteTree: %v", err)
	}
	commit, err := rs.WriteCommit(tree, nil, "meads: init")
	if err != nil {
		t.Fatalf("WriteCommit: %v", err)
	}
	if err := rs.CompareAndSwap("refs/meads/tasks", commit, ZeroOID); err != nil {
		t.Fatalf("CompareAndSwap: %v", err)
	}

	// Snapshot the index mtime immediately - before running any further git
	// commands (see note above about status/diff rewriting the index).
	infoAfter, err := os.Stat(indexPath)
	if err != nil {
		t.Fatalf("stat %s: %v", indexPath, err)
	}
	if !infoBefore.ModTime().Equal(infoAfter.ModTime()) {
		t.Errorf(".git/index mtime changed: before=%v after=%v", infoBefore.ModTime(), infoAfter.ModTime())
	}

	// Now it's safe to run further git commands for the remaining checks.
	if status := runGit(t, dir, "status", "--porcelain"); status != "" {
		t.Errorf("git status --porcelain not empty after ref-only writes: %q", status)
	}
	filesAfter := listWorkingTreeFiles(t, dir)
	if !slices.Equal(filesBefore, filesAfter) {
		t.Errorf("working tree file list changed: before=%v after=%v", filesBefore, filesAfter)
	}
	if headAfter := runGit(t, dir, "rev-parse", "HEAD"); headAfter != headBefore {
		t.Errorf("HEAD moved: before=%s after=%s", headBefore, headAfter)
	}
}

// --- 2. blob round-trip is byte-exact ---

func TestWriteBlob_ReadBlob_RoundTrip(t *testing.T) {
	rs, _ := newRefStoreRepo(t)

	tests := []struct {
		name string
		data []byte
	}{
		{"empty", []byte{}},
		{"plain text", []byte("hello world")},
		{"trailing newline", []byte("hello\n")},
		{"trailing whitespace and blank lines", []byte("hello   \n\n  \t \n")},
		{"multiple newlines mid-content", []byte("line1\nline2\n\nline3\n")},
		{"indented json with trailing newline", []byte("{\n  \"a\": 1,\n  \"b\": \"two\"\n}\n")},
		{"non-ascii utf8", []byte("héllo wörld 日本語 🎉\n")},
		{"no trailing newline", []byte("no newline at end of file")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			oid, err := rs.WriteBlob(tt.data)
			if err != nil {
				t.Fatalf("WriteBlob: %v", err)
			}
			got, err := rs.ReadBlob(oid)
			if err != nil {
				t.Fatalf("ReadBlob: %v", err)
			}
			if !bytes.Equal(got, tt.data) {
				t.Errorf("round trip mismatch:\n got  %q\n want %q", got, tt.data)
			}
		})
	}
}

// --- 3 & 4. CAS: correct prev succeeds, stale prev is rejected ---

func TestCompareAndSwap_CorrectPrev_MovesRef(t *testing.T) {
	rs, _ := newRefStoreRepo(t)
	const ref = "refs/meads/tasks"

	oid1 := makeCommit(t, rs, `{"n":1}`, nil)
	if err := rs.CompareAndSwap(ref, oid1, ZeroOID); err != nil {
		t.Fatalf("create: %v", err)
	}

	oid2 := makeCommit(t, rs, `{"n":2}`, []OID{oid1})
	if err := rs.CompareAndSwap(ref, oid2, oid1); err != nil {
		t.Fatalf("CompareAndSwap with correct prev: %v", err)
	}

	got, err := rs.ResolveRef(ref)
	if err != nil {
		t.Fatalf("ResolveRef: %v", err)
	}
	if got != oid2 {
		t.Errorf("ResolveRef(%s) = %s, want %s", ref, got, oid2)
	}
}

func TestCompareAndSwap_StalePrev_Rejected(t *testing.T) {
	rs, _ := newRefStoreRepo(t)
	const ref = "refs/meads/tasks"

	oid1 := makeCommit(t, rs, `{"n":1}`, nil)
	if err := rs.CompareAndSwap(ref, oid1, ZeroOID); err != nil {
		t.Fatalf("create: %v", err)
	}

	// A real, valid commit OID that simply isn't the ref's current value -
	// simulating a caller racing against a concurrent update.
	staleGuess := makeCommit(t, rs, `{"n":"someone else's write"}`, nil)
	next := makeCommit(t, rs, `{"n":2}`, []OID{oid1})

	err := rs.CompareAndSwap(ref, next, staleGuess)
	if err == nil {
		t.Fatal("expected error for stale prev, got nil")
	}
	if !errors.Is(err, ErrCASConflict) {
		t.Errorf("err = %v, want errors.Is(err, ErrCASConflict)", err)
	}

	got, err := rs.ResolveRef(ref)
	if err != nil {
		t.Fatalf("ResolveRef: %v", err)
	}
	if got != oid1 {
		t.Errorf("ref moved despite rejected CAS: got %s, want unchanged %s", got, oid1)
	}
}

// --- 5. create-only semantics via ZeroOID prev ---

func TestCompareAndSwap_CreateOnly(t *testing.T) {
	rs, _ := newRefStoreRepo(t)
	const ref = "refs/meads/create-only"

	oid1 := makeCommit(t, rs, `{"n":1}`, nil)
	if err := rs.CompareAndSwap(ref, oid1, ZeroOID); err != nil {
		t.Fatalf("first create should succeed: %v", err)
	}

	oid2 := makeCommit(t, rs, `{"n":2}`, nil)
	err := rs.CompareAndSwap(ref, oid2, ZeroOID)
	if err == nil {
		t.Fatal("expected error creating over an existing ref, got nil")
	}
	if !errors.Is(err, ErrCASConflict) {
		t.Errorf("err = %v, want errors.Is(err, ErrCASConflict)", err)
	}

	got, err := rs.ResolveRef(ref)
	if err != nil {
		t.Fatalf("ResolveRef: %v", err)
	}
	if got != oid1 {
		t.Errorf("ref changed despite rejected create: got %s, want %s", got, oid1)
	}
}

// --- 6. ResolveRef on a missing ref ---

func TestResolveRef_MissingRef(t *testing.T) {
	rs, _ := newRefStoreRepo(t)
	_, err := rs.ResolveRef("refs/meads/does-not-exist")
	if err == nil {
		t.Fatal("expected error for missing ref, got nil")
	}
	if !errors.Is(err, ErrRefNotFound) {
		t.Errorf("err = %v, want errors.Is(err, ErrRefNotFound)", err)
	}
}

// --- 7. CompareAndDelete ---

func TestCompareAndDelete(t *testing.T) {
	rs, _ := newRefStoreRepo(t)
	const ref = "refs/meads/deletable"

	oid1 := makeCommit(t, rs, `{"n":1}`, nil)
	if err := rs.CompareAndSwap(ref, oid1, ZeroOID); err != nil {
		t.Fatalf("create: %v", err)
	}

	staleGuess := makeCommit(t, rs, `{"n":"wrong"}`, nil)
	err := rs.CompareAndDelete(ref, staleGuess)
	if err == nil {
		t.Fatal("expected error deleting with wrong prev, got nil")
	}
	if !errors.Is(err, ErrCASConflict) {
		t.Errorf("err = %v, want errors.Is(err, ErrCASConflict)", err)
	}
	if got, rerr := rs.ResolveRef(ref); rerr != nil || got != oid1 {
		t.Fatalf("ref should survive a rejected delete: got=%v err=%v, want %s", got, rerr, oid1)
	}

	if err := rs.CompareAndDelete(ref, oid1); err != nil {
		t.Fatalf("CompareAndDelete with correct prev: %v", err)
	}
	if _, err := rs.ResolveRef(ref); !errors.Is(err, ErrRefNotFound) {
		t.Errorf("ResolveRef after delete: err = %v, want errors.Is(err, ErrRefNotFound)", err)
	}
}

// --- 8. AtomicUpdate is all-or-nothing ---

func TestAtomicUpdate_HappyPath_BothMove(t *testing.T) {
	rs, _ := newRefStoreRepo(t)
	const refA = "refs/meads/a"
	const refB = "refs/meads/b"

	aOID1 := makeCommit(t, rs, `{"ref":"a","n":1}`, nil)
	bOID1 := makeCommit(t, rs, `{"ref":"b","n":1}`, nil)
	if err := rs.CompareAndSwap(refA, aOID1, ZeroOID); err != nil {
		t.Fatalf("seed refA: %v", err)
	}
	if err := rs.CompareAndSwap(refB, bOID1, ZeroOID); err != nil {
		t.Fatalf("seed refB: %v", err)
	}

	aOID2 := makeCommit(t, rs, `{"ref":"a","n":2}`, []OID{aOID1})
	bOID2 := makeCommit(t, rs, `{"ref":"b","n":2}`, []OID{bOID1})

	err := rs.AtomicUpdate([]RefUpdate{
		{Name: refA, Next: aOID2, Prev: aOID1},
		{Name: refB, Next: bOID2, Prev: bOID1},
	})
	if err != nil {
		t.Fatalf("AtomicUpdate: %v", err)
	}

	if got, rerr := rs.ResolveRef(refA); rerr != nil || got != aOID2 {
		t.Errorf("refA = %v (err=%v), want %s", got, rerr, aOID2)
	}
	if got, rerr := rs.ResolveRef(refB); rerr != nil || got != bOID2 {
		t.Errorf("refB = %v (err=%v), want %s", got, rerr, bOID2)
	}
}

func TestAtomicUpdate_OneStalePrev_NothingMoves(t *testing.T) {
	rs, _ := newRefStoreRepo(t)
	const refA = "refs/meads/a"
	const refB = "refs/meads/b"

	aOID1 := makeCommit(t, rs, `{"ref":"a","n":1}`, nil)
	bOID1 := makeCommit(t, rs, `{"ref":"b","n":1}`, nil)
	if err := rs.CompareAndSwap(refA, aOID1, ZeroOID); err != nil {
		t.Fatalf("seed refA: %v", err)
	}
	if err := rs.CompareAndSwap(refB, bOID1, ZeroOID); err != nil {
		t.Fatalf("seed refB: %v", err)
	}

	aOID2 := makeCommit(t, rs, `{"ref":"a","n":2}`, []OID{aOID1})
	bOID2 := makeCommit(t, rs, `{"ref":"b","n":2}`, []OID{bOID1})
	staleGuess := makeCommit(t, rs, `{"ref":"b","n":"wrong"}`, nil)

	err := rs.AtomicUpdate([]RefUpdate{
		{Name: refA, Next: aOID2, Prev: aOID1},      // correct prev
		{Name: refB, Next: bOID2, Prev: staleGuess}, // stale prev
	})
	if err == nil {
		t.Fatal("expected error from a batch containing a stale prev, got nil")
	}
	if !errors.Is(err, ErrCASConflict) {
		t.Errorf("err = %v, want errors.Is(err, ErrCASConflict)", err)
	}

	if got, rerr := rs.ResolveRef(refA); rerr != nil || got != aOID1 {
		t.Errorf("refA moved despite failed atomic batch: got=%v err=%v, want unchanged %s", got, rerr, aOID1)
	}
	if got, rerr := rs.ResolveRef(refB); rerr != nil || got != bOID1 {
		t.Errorf("refB moved despite failed atomic batch: got=%v err=%v, want unchanged %s", got, rerr, bOID1)
	}
}

// --- 9. AtomicUpdate can create and delete in the same batch ---

func TestAtomicUpdate_CreateAndDelete(t *testing.T) {
	rs, _ := newRefStoreRepo(t)
	const refNew = "refs/meads/new-ref"
	const refOld = "refs/meads/old-ref"

	oidOld := makeCommit(t, rs, `{"n":"old"}`, nil)
	if err := rs.CompareAndSwap(refOld, oidOld, ZeroOID); err != nil {
		t.Fatalf("seed refOld: %v", err)
	}
	oidNew := makeCommit(t, rs, `{"n":"new"}`, nil)

	err := rs.AtomicUpdate([]RefUpdate{
		{Name: refNew, Next: oidNew, Prev: ZeroOID}, // create
		{Name: refOld, Next: ZeroOID, Prev: oidOld}, // delete
	})
	if err != nil {
		t.Fatalf("AtomicUpdate create+delete: %v", err)
	}

	if got, rerr := rs.ResolveRef(refNew); rerr != nil || got != oidNew {
		t.Errorf("refNew = %v (err=%v), want %s", got, rerr, oidNew)
	}
	if _, err := rs.ResolveRef(refOld); !errors.Is(err, ErrRefNotFound) {
		t.Errorf("refOld should be deleted, ResolveRef err = %v, want ErrRefNotFound", err)
	}
}

// --- 10. CommitFile + ReadFileAtRef round-trip ---

func TestCommitFile_ReadFileAtRef_RoundTrip(t *testing.T) {
	rs, _ := newRefStoreRepo(t)
	const ref = "refs/meads/tasks"
	content := []byte("{\n  \"tasks\": [{\"id\": 1, \"title\": \"héllo\\nworld\"}]\n}\n")

	commitOID, err := rs.CommitFile(ref, "tasks.json", content, ZeroOID, "seed tasks")
	if err != nil {
		t.Fatalf("CommitFile: %v", err)
	}

	got, gotOID, err := rs.ReadFileAtRef(ref, "tasks.json")
	if err != nil {
		t.Fatalf("ReadFileAtRef: %v", err)
	}
	if !bytes.Equal(got, content) {
		t.Errorf("content mismatch:\n got  %q\n want %q", got, content)
	}
	if gotOID != commitOID {
		t.Errorf("ReadFileAtRef OID = %s, want CommitFile's returned OID %s", gotOID, commitOID)
	}

	resolved, err := rs.ResolveRef(ref)
	if err != nil {
		t.Fatalf("ResolveRef: %v", err)
	}
	if resolved != commitOID {
		t.Errorf("ResolveRef(%s) = %s, want %s", ref, resolved, commitOID)
	}
	if gotOID != resolved {
		t.Errorf("ReadFileAtRef OID = %s, want ResolveRef result %s", gotOID, resolved)
	}
}

// --- 11. History ---

func TestHistory_NewestFirst(t *testing.T) {
	rs, _ := newRefStoreRepo(t)
	const ref = "refs/meads/tasks"

	oid1, err := rs.CommitFile(ref, "tasks.json", []byte(`{"n":1}`), ZeroOID, "commit 1")
	if err != nil {
		t.Fatalf("CommitFile 1: %v", err)
	}
	oid2, err := rs.CommitFile(ref, "tasks.json", []byte(`{"n":2}`), oid1, "commit 2")
	if err != nil {
		t.Fatalf("CommitFile 2: %v", err)
	}
	oid3, err := rs.CommitFile(ref, "tasks.json", []byte(`{"n":3}`), oid2, "commit 3")
	if err != nil {
		t.Fatalf("CommitFile 3: %v", err)
	}

	hist, err := rs.History(ref)
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	if len(hist) != 3 {
		t.Fatalf("len(History) = %d, want 3 (%v)", len(hist), hist)
	}

	resolved, err := rs.ResolveRef(ref)
	if err != nil {
		t.Fatalf("ResolveRef: %v", err)
	}
	if hist[0] != resolved {
		t.Errorf("History[0] = %s, want newest entry to equal ResolveRef %s", hist[0], resolved)
	}

	want := []OID{oid3, oid2, oid1}
	if !slices.Equal(hist, want) {
		t.Errorf("History = %v, want newest-first %v", hist, want)
	}
}

// --- 12. ListRefs with a prefix ---

func TestListRefs_Prefix(t *testing.T) {
	rs, _ := newRefStoreRepo(t)

	tasksOID := makeCommit(t, rs, `{"kind":"tasks"}`, nil)
	logOID := makeCommit(t, rs, `{"kind":"log"}`, nil)
	otherOID := makeCommit(t, rs, `{"kind":"other"}`, nil)

	for name, oid := range map[string]OID{
		"refs/meads/tasks": tasksOID,
		"refs/meads/log":   logOID,
		"refs/other/thing": otherOID,
	} {
		if err := rs.CompareAndSwap(name, oid, ZeroOID); err != nil {
			t.Fatalf("seeding %s: %v", name, err)
		}
	}

	got, err := rs.ListRefs("refs/meads/")
	if err != nil {
		t.Fatalf("ListRefs: %v", err)
	}
	want := map[string]OID{
		"refs/meads/tasks": tasksOID,
		"refs/meads/log":   logOID,
	}
	if len(got) != len(want) {
		t.Fatalf("ListRefs returned %d refs, want %d (got %v)", len(got), len(want), got)
	}
	for name, oid := range want {
		gotOID, ok := got[name]
		if !ok {
			t.Errorf("ListRefs missing %s", name)
			continue
		}
		if gotOID != oid {
			t.Errorf("ListRefs[%s] = %s, want %s", name, gotOID, oid)
		}
	}
	if _, ok := got["refs/other/thing"]; ok {
		t.Errorf("ListRefs(%q) should not include refs/other/thing", "refs/meads/")
	}
}

// A literal for-each-ref pattern also matches deeper refs up to a slash, so
// resolving "refs/meads/tasks/42" while only "refs/meads/tasks/42/sub" exists
// must report not-found rather than silently returning the nested ref's OID.
func TestResolveRef_NestedRefDoesNotShadowExact(t *testing.T) {
	rs, _ := newRefStoreRepo(t)
	blob, err := rs.WriteBlob([]byte("x"))
	if err != nil {
		t.Fatalf("WriteBlob: %v", err)
	}
	if err := rs.CompareAndSwap("refs/meads/tasks/42/sub", blob, ZeroOID); err != nil {
		t.Fatalf("creating nested ref: %v", err)
	}
	if oid, err := rs.ResolveRef("refs/meads/tasks/42"); !errors.Is(err, ErrRefNotFound) {
		t.Fatalf("ResolveRef of absent parent = (%q, %v), want ErrRefNotFound", oid, err)
	}
	// the nested ref itself still resolves exactly
	if oid, err := rs.ResolveRef("refs/meads/tasks/42/sub"); err != nil || oid != blob {
		t.Fatalf("ResolveRef nested = (%q, %v), want (%q, nil)", oid, err, blob)
	}
}
