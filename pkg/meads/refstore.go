package meads

import (
	"bytes"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// ZeroOID is git's all-zero object id. As a Prev value it means "the ref
// must not currently exist"; as a Next value it means "delete the ref".
const ZeroOID = "0000000000000000000000000000000000000000"

// OID is a git object id (a hex SHA).
type OID string

// ErrRefNotFound is returned when a ref does not exist.
var ErrRefNotFound = errors.New("ref not found")

// ErrCASConflict is returned when a ref's current value does not match the
// expected previous value supplied to a compare-and-swap operation.
var ErrCASConflict = errors.New("ref compare-and-swap conflict")

// ErrUnrelatedHistories is returned by MergeBase when a and b share no
// common ancestor at all - two independently created root histories, e.g.
// two clones' Create calls that happened to land on the same task id while
// partitioned (see GitStore.Doctor). "git merge-base a b" exits 1 with no
// output for exactly this case, which is documented, stable behaviour
// (confirmed experimentally too) - distinct from any other git failure
// (an unknown/malformed oid, a corrupt object), which exits with a
// different, non-1 status and a stderr message; see MergeBase's exit-code
// check for how the two are told apart. Never returned for a valid oid
// pair that simply happens to be identical or fast-forward-related - those
// have a real merge base (themselves, in the identical case).
var ErrUnrelatedHistories = errors.New("no common ancestor")

// commitIdentity pins a deterministic author/committer so WriteCommit works
// even in a repo with no user.name/user.email configured. Passed as -c
// overrides (rather than GIT_AUTHOR_*/GIT_COMMITTER_* env vars, which the
// Git interface has no per-call way to set) so it only affects this one
// invocation; these are plumbing commits and never meant to carry a human
// identity.
var commitIdentity = []string{"-c", "user.name=meads", "-c", "user.email=meads@localhost"}

// TreeEntry is one line of a tree object, as read/written by git mktree.
type TreeEntry struct {
	Mode string // e.g. "100644"
	Type string // "blob" or "tree"
	OID  OID
	Name string
}

// RefUpdate is one entry of an AtomicUpdate transaction.
type RefUpdate struct {
	Name string
	Next OID // ZeroOID means delete
	Prev OID // required; ZeroOID means "must not exist"
}

// RefStore stores data directly in git objects and refs via plumbing
// commands. It never touches the working tree or the index.
type RefStore struct {
	git Git
}

// NewRefStore creates a RefStore backed by git.
func NewRefStore(git Git) *RefStore {
	return &RefStore{git: git}
}

// WriteBlob writes data as a blob object and returns its OID.
func (r *RefStore) WriteBlob(data []byte) (OID, error) {
	out, err := r.git.OutputWithInput(string(data), "hash-object", "-w", "--stdin")
	if err != nil {
		return "", fmt.Errorf("hash-object: %w", err)
	}
	return OID(out), nil
}

// WriteTree writes entries as a single tree object and returns its OID.
func (r *RefStore) WriteTree(entries []TreeEntry) (OID, error) {
	var b strings.Builder
	for _, e := range entries {
		// mktree's stdin format is "<mode> SP <type> SP <oid> TAB <name>";
		// the tab is required, not cosmetic.
		fmt.Fprintf(&b, "%s %s %s\t%s\n", e.Mode, e.Type, e.OID, e.Name)
	}
	out, err := r.git.OutputWithInput(b.String(), "mktree")
	if err != nil {
		return "", fmt.Errorf("mktree: %w", err)
	}
	return OID(out), nil
}

// WriteCommit writes a commit object over tree with the given parents (none
// for a root commit) and returns its OID.
func (r *RefStore) WriteCommit(tree OID, parents []OID, message string) (OID, error) {
	args := append([]string{}, commitIdentity...)
	args = append(args, "commit-tree", string(tree))
	for _, p := range parents {
		args = append(args, "-p", string(p))
	}
	args = append(args, "-m", message)
	out, err := r.git.OutputWithInput("", args...)
	if err != nil {
		return "", fmt.Errorf("commit-tree: %w", err)
	}
	return OID(out), nil
}

// ReadBlob returns the exact byte content of the blob at oid. Content is
// binary-safe (e.g. JSON payloads must round-trip exactly), so this uses
// OutputRaw rather than the trimming Output/OutputWithInput methods.
func (r *RefStore) ReadBlob(oid OID) ([]byte, error) {
	out, err := r.git.OutputRaw("cat-file", "blob", string(oid))
	if err != nil {
		return nil, fmt.Errorf("cat-file blob %s: %w", oid, err)
	}
	return out, nil
}

// ResolveRef returns the OID name currently points to. name must be a
// fully-qualified ref (e.g. "refs/meads/tasks"). Returns ErrRefNotFound if
// the ref does not exist.
// It uses for-each-ref rather than show-ref because for-each-ref exits 0 when
// nothing matches. That separates "ref is absent" (exit 0, no output) from "git
// itself failed" (non-zero exit), which show-ref conflates into exit 1 — and
// conflictError must not mistake a broken repo for a CAS conflict.
func (r *RefStore) ResolveRef(name string) (OID, error) {
	out, err := r.git.Output("for-each-ref", "--format=%(refname) %(objectname)", name)
	if err != nil {
		return "", fmt.Errorf("for-each-ref %s: %w", name, err)
	}
	// A literal pattern also matches deeper refs up to a slash, so require an
	// exact refname match rather than trusting the first line.
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[0] == name {
			return OID(fields[1]), nil
		}
	}
	return "", fmt.Errorf("%s: %w", name, ErrRefNotFound)
}

// ListRefs returns every ref under prefix as a map of full refname to OID.
func (r *RefStore) ListRefs(prefix string) (map[string]OID, error) {
	out, err := r.git.Output("for-each-ref", "--format=%(refname) %(objectname)", prefix)
	if err != nil {
		return nil, fmt.Errorf("for-each-ref %s: %w", prefix, err)
	}
	refs := make(map[string]OID)
	for _, line := range strings.Split(out, "\n") {
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != 2 {
			return nil, fmt.Errorf("for-each-ref %s: unexpected line %q", prefix, line)
		}
		refs[fields[0]] = OID(fields[1])
	}
	return refs, nil
}

// CompareAndSwap sets ref name to next iff its current value equals prev.
// Pass ZeroOID as prev to require the ref not already exist; pass ZeroOID as
// next to delete it. There is deliberately no variant that skips prev: git
// silently disables the compare-and-swap (and clobbers whatever is there) if
// the old value is omitted, so this package never omits it.
//
// git update-ref takes the new value BEFORE the old value:
// "git update-ref <ref> <new> <old>". That is the opposite of the git push
// wire protocol (smart HTTP/SSH), which sends
// "<old-value> SP <new-value> SP <ref-name>" — don't transpose the two.
//
// A non-zero exit is only reported as ErrCASConflict once the ref has been
// re-read and its value confirmed to actually differ from prev; git's error
// text is never parsed, since its wording isn't stable across git
// versions/servers.
func (r *RefStore) CompareAndSwap(name string, next, prev OID) error {
	_, err := r.git.OutputWithInput("", "update-ref", name, string(next), string(prev))
	if err != nil {
		return r.conflictError(name, prev, fmt.Errorf("update-ref %s: %w", name, err))
	}
	return nil
}

// CompareAndDelete deletes ref name iff its current value equals prev.
func (r *RefStore) CompareAndDelete(name string, prev OID) error {
	return r.CompareAndSwap(name, ZeroOID, prev)
}

// conflictError re-reads name after a failed update and decides whether the
// failure was a CAS conflict: if the ref's actual current value differs from
// prev (including exists-when-ZeroOID-expected, or missing-when-a-real-oid-
// expected), it wraps ErrCASConflict; otherwise it returns updateErr
// unchanged, since the update failed for some other reason.
func (r *RefStore) conflictError(name string, prev OID, updateErr error) error {
	cur, err := r.ResolveRef(name)
	if err != nil {
		if !errors.Is(err, ErrRefNotFound) {
			return updateErr // current state unknown; surface the original failure
		}
		cur = ZeroOID
	}
	if cur != prev {
		return fmt.Errorf("%s: %w: expected %s, found %s", name, ErrCASConflict, prev, cur)
	}
	return updateErr
}

// AtomicUpdate applies every update or none. It runs a single
// "git update-ref --stdin" transaction (start/prepare/commit): if any
// update's prev check fails, prepare fails and no ref in the batch moves.
func (r *RefStore) AtomicUpdate(updates []RefUpdate) error {
	var b strings.Builder
	b.WriteString("start\n")
	for _, u := range updates {
		if u.Next == ZeroOID {
			fmt.Fprintf(&b, "delete %s %s\n", u.Name, u.Prev)
		} else {
			fmt.Fprintf(&b, "update %s %s %s\n", u.Name, u.Next, u.Prev)
		}
	}
	b.WriteString("prepare\ncommit\n")
	_, err := r.git.OutputWithInput(b.String(), "update-ref", "--stdin")
	if err != nil {
		updateErr := fmt.Errorf("update-ref --stdin: %w", err)
		// The transaction is all-or-nothing, so every ref is still at its
		// pre-transaction value; check each against its expected prev to
		// report which one caused the conflict.
		for _, u := range updates {
			if casErr := r.conflictError(u.Name, u.Prev, updateErr); errors.Is(casErr, ErrCASConflict) {
				return casErr
			}
		}
		return updateErr
	}
	return nil
}

// ReadFileAtRef returns the content of path as of ref's current commit,
// along with ref's current OID. Returns ErrRefNotFound if ref does not
// exist.
func (r *RefStore) ReadFileAtRef(ref, path string) (content []byte, refOID OID, err error) {
	refOID, err = r.ResolveRef(ref)
	if err != nil {
		return nil, "", err
	}
	content, err = r.git.OutputRaw("cat-file", "blob", ref+":"+path)
	if err != nil {
		return nil, "", fmt.Errorf("reading %s at %s: %w", path, ref, err)
	}
	return content, refOID, nil
}

// ReadFilesAtCommits reads path out of every commit in commits, using ONE
// `git cat-file --batch` process for the whole set instead of one (or, via
// ReadFileAtRef, two) per commit. Results come back in the same order as
// commits.
//
// This is what keeps a git-mode read O(1) in git processes rather than
// O(tasks): every task is its own ref, so the naive shape spawns a pair of
// plumbing processes per task and a store of any size spends nearly all its
// wall clock in fork/exec rather than in git (measured at 100 tasks: 403
// processes, 0.72s, with sys time 3x user). git already has the batch
// plumbing; this just uses it.
//
// Addressing is by commit oid rather than refname - "<oid>:<path>" - because
// callers reach here from ListRefs, which already resolved every ref to its
// oid. Re-deriving a refname would both re-resolve refs git has already told
// us about and re-introduce the per-task for-each-ref this exists to remove.
//
// A commit whose path is absent is an error, not a hole: every task ref is
// created with its task.json in the same commit (CommitFile), so a missing
// one means a corrupt store rather than a legitimately empty slot.
func (r *RefStore) ReadFilesAtCommits(commits []OID, path string) ([][]byte, error) {
	paths := make([]string, len(commits))
	for i := range paths {
		paths[i] = path
	}
	return r.ReadFilesAtCommitsPaths(commits, paths)
}

// ReadFilesAtCommitsPaths is ReadFilesAtCommits with one path per commit.
// It lets callers batch heterogeneous objects (notably config.json plus every
// task.json) through the same single cat-file process. commits and paths must
// have equal lengths; results preserve their order.
func (r *RefStore) ReadFilesAtCommitsPaths(commits []OID, paths []string) ([][]byte, error) {
	if len(commits) != len(paths) {
		return nil, fmt.Errorf("cat-file --batch: %d commits but %d paths", len(commits), len(paths))
	}
	if len(commits) == 0 {
		return nil, nil
	}
	var stdin strings.Builder
	for i, oid := range commits {
		stdin.WriteString(string(oid))
		stdin.WriteString(":")
		stdin.WriteString(paths[i])
		stdin.WriteString("\n")
	}
	out, err := r.git.OutputRawWithInput(stdin.String(), "cat-file", "--batch")
	if err != nil {
		return nil, fmt.Errorf("cat-file --batch over %d commits: %w", len(commits), err)
	}
	pathLabel := paths[0]
	for _, path := range paths[1:] {
		if path != pathLabel {
			pathLabel = "requested paths"
			break
		}
	}
	return parseCatFileBatch(out, len(commits), pathLabel)
}

// parseCatFileBatch splits `git cat-file --batch` output into its want
// payloads. The format is one frame per input line, IN INPUT ORDER, each
//
//	<oid> SP <type> SP <size> LF <contents> LF
//
// or, when the object could not be resolved, a lone
//
//	<input-spec> SP missing LF
//
// Frames are matched to inputs by position, not by the oid in the header:
// that oid is the BLOB's, while the input was addressed by commit, so the
// two never match and only order relates them. <size> is authoritative -
// contents are binary-safe and may contain LF - so the payload is taken by
// length and the delimiter after it merely skipped.
func parseCatFileBatch(out []byte, want int, path string) ([][]byte, error) {
	results := make([][]byte, 0, want)
	rest := out
	for len(results) < want {
		nl := bytes.IndexByte(rest, '\n')
		if nl < 0 {
			return nil, fmt.Errorf("cat-file --batch: output ended after %d of %d objects", len(results), want)
		}
		header := string(rest[:nl])
		rest = rest[nl+1:]
		fields := strings.Fields(header)
		if len(fields) != 3 {
			// "<spec> missing", "<spec> ambiguous", or something unforeseen.
			return nil, fmt.Errorf("cat-file --batch: reading %s: %s", path, header)
		}
		size, err := strconv.Atoi(fields[2])
		if err != nil {
			return nil, fmt.Errorf("cat-file --batch: unparsable size in %q: %w", header, err)
		}
		if size > len(rest) {
			return nil, fmt.Errorf("cat-file --batch: %q claims %d bytes, only %d remain", header, size, len(rest))
		}
		results = append(results, rest[:size])
		rest = rest[size:]
		if len(rest) > 0 && rest[0] == '\n' {
			rest = rest[1:] // the LF git appends after each payload
		}
	}
	return results, nil
}

// CommitFile writes content as path's sole file at ref's tip: it blobs the
// content, builds a single-entry tree, commits it (parented on prev unless
// prev is ZeroOID, meaning ref must not yet exist), and CAS-updates ref to
// the new commit. Returns the new commit OID.
func (r *RefStore) CommitFile(ref, path string, content []byte, prev OID, message string) (OID, error) {
	blob, err := r.WriteBlob(content)
	if err != nil {
		return "", err
	}
	tree, err := r.WriteTree([]TreeEntry{{Mode: "100644", Type: "blob", OID: blob, Name: path}})
	if err != nil {
		return "", err
	}
	var parents []OID
	if prev != ZeroOID {
		parents = []OID{prev}
	}
	commit, err := r.WriteCommit(tree, parents, message)
	if err != nil {
		return "", err
	}
	if err := r.CompareAndSwap(ref, commit, prev); err != nil {
		return "", err
	}
	return commit, nil
}

// History returns every commit reachable from ref, newest first.
func (r *RefStore) History(ref string) ([]OID, error) {
	out, err := r.git.Output("rev-list", ref)
	if err != nil {
		return nil, fmt.Errorf("rev-list %s: %w", ref, err)
	}
	if out == "" {
		return nil, nil
	}
	lines := strings.Split(out, "\n")
	hist := make([]OID, len(lines))
	for i, l := range lines {
		hist[i] = OID(l)
	}
	return hist, nil
}

// MergeBase returns the best common ancestor commit of a and b, or
// ErrUnrelatedHistories if they share none - see that error's doc comment.
// Used by GitStore.Doctor to tell a true cross-clone duplicate id (no
// common ancestor: two independently created tasks) apart from an
// edit/edit conflict on one task (a common ancestor exists: GitStore.
// Diverged's job instead), and by Diverged itself to find the pre-
// divergence state and to recognise a plain fast-forward (base equals one
// of the two inputs) as "not diverged".
//
// "git merge-base a b" exits 0 with the ancestor's oid on stdout when one
// exists, and 1 with empty stdout when it does not (see ErrUnrelatedHistories);
// any other exit (e.g. an unresolvable oid) is a genuine plumbing failure,
// told apart here by exit code alone, NEVER by matching stderr text - same
// discipline as CompareAndSwap's conflictError.
func (r *RefStore) MergeBase(a, b OID) (OID, error) {
	out, err := r.git.OutputWithInput("", "merge-base", string(a), string(b))
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
			return "", ErrUnrelatedHistories
		}
		return "", fmt.Errorf("merge-base %s %s: %w", a, b, err)
	}
	return OID(out), nil
}
