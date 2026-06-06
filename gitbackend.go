// Command task30 is a proof-of-concept for meads task 30 ("Git mode"): it keeps
// task state purely in git, as a single tasks.json blob under a dedicated ref
// (refs/meads/tasks), out of the working tree. Each mutation is a new commit
// parented on the prior one, and the ref is advanced via native CAS.
//
// This file implements the "ref plumbing" using furgit
// (lindenii.org/go/furgit), a low-level git plumbing library:
//
//	mutation:  tasks.json bytes -> blob -> tree -> commit -> CAS-advance ref
//	read:      ref -> commit -> tree -> tasks.json blob
//	history:   walk the ref commit's first-parent chain
//
// It is the furgit analogue of the go-git gitBackend sketched in task 30. The
// go-git CheckAndSetReference(new, old) is realised here with a furgit ref
// Transaction: Update(name, newID, oldID) verifies the current value equals
// oldID under a lock file before advancing — optimistic locking with no
// lock-line scheme.
package main

import (
	"errors"
	"fmt"
	"math/rand"
	"os"
	"sync"
	"time"

	"lindenii.org/go/furgit/object/commit"
	objectid "lindenii.org/go/furgit/object/id"
	objectsignature "lindenii.org/go/furgit/object/signature"
	"lindenii.org/go/furgit/object/tree"
	objecttype "lindenii.org/go/furgit/object/type"
	refstore "lindenii.org/go/furgit/ref/store"
	"lindenii.org/go/furgit/repository"
)

// RefName is the dedicated ref that backs all task state. It lives outside
// refs/heads/* and refs/tags/*, so it never shows up in `git status`,
// `git log`, branch listings, or normal pushes.
const RefName = "refs/meads/tasks"

// blobName is the single file stored in the ref's tree.
const blobName = "tasks.json"

// maxRetries bounds the CAS retry loop under contention.
const maxRetries = 256

// GitBackend is the task-30 `backend` for the git-ref store, implemented over
// furgit. It owns an open repository handle; construct one per process (or per
// goroutine) — separate handles coordinate purely through on-disk ref locks,
// which is what makes the CAS safe across processes/clones.
type GitBackend struct {
	repo    *repository.Repository
	root    *os.Root
	algo    objectid.Algorithm
	refName string

	// serialize, when non-nil, guards the furgit ref transaction (begin..commit)
	// for the duration of the CAS. furgit's files Transaction is documented
	// MT-Unsafe and its lock-file cleanup races across concurrent same-ref
	// writers, so a real md git-mode would have to provide this lock itself
	// (e.g. flock) across processes. It deliberately does NOT cover the
	// read/apply step, so the CAS retry path is still exercised.
	serialize *sync.Mutex
}

// SetSerializer installs a shared mutex that serializes the furgit ref
// transaction portion of transact. Pass the same mutex to every backend that
// shares a repository to make concurrent CAS safe in-process.
func (b *GitBackend) SetSerializer(mu *sync.Mutex) { b.serialize = mu }

// OpenGitBackend opens the git directory at gitDir (e.g. "<repo>/.git", or the
// repo root for a bare repo) and wires up a furgit-backed backend.
func OpenGitBackend(gitDir string) (*GitBackend, error) {
	root, err := os.OpenRoot(gitDir)
	if err != nil {
		return nil, fmt.Errorf("open git dir %q: %w", gitDir, err)
	}

	repo, err := repository.Open(root)
	if err != nil {
		_ = root.Close()
		return nil, fmt.Errorf("furgit open repository: %w", err)
	}

	return &GitBackend{
		repo:    repo,
		root:    root,
		algo:    repo.Algorithm(),
		refName: RefName,
	}, nil
}

// Close releases the repository and git-dir handles.
func (b *GitBackend) Close() error {
	err := b.repo.Close()
	if cerr := b.root.Close(); err == nil {
		err = cerr
	}
	return err
}

// --- task-30 backend interface -------------------------------------------------

// read returns the current tasks.json content. A missing ref yields "".
func (b *GitBackend) read() (string, error) {
	head, ok, err := b.resolveHead()
	if err != nil || !ok {
		return "", err
	}
	return b.readAtCommit(head)
}

// headContent returns the committed HEAD content of the ref, if any. It is the
// git-mode analogue of reading the last committed TASKS.md (for committedIDs).
func (b *GitBackend) headContent() (string, bool) {
	content, err := b.read()
	if err != nil || content == "" {
		return "", false
	}
	return content, true
}

// ensureInit seeds the ref with `empty` as the first ("virtual") commit. It is
// an error if the ref already exists.
func (b *GitBackend) ensureInit(empty string) error {
	if _, ok, err := b.resolveHead(); err != nil {
		return err
	} else if ok {
		return fmt.Errorf("%s already initialized", b.refName)
	}

	commitID, err := b.writeSnapshot(empty, nil, "meads: init")
	if err != nil {
		return err
	}

	tx, err := b.repo.RefStore().BeginTransaction()
	if err != nil {
		return err
	}
	if err := tx.Create(b.refName, commitID); err != nil {
		_ = tx.Abort()
		return fmt.Errorf("create %s: %w", b.refName, err)
	}
	return tx.Commit()
}

// transact applies fn to the current content and advances the ref via CAS,
// retrying on contention. fn receives the current tasks.json and returns the
// next content; when changed is false the ref is left untouched.
//
// This is the heart of the ref plumbing: read-ref -> apply -> write objects ->
// CAS-advance, looping if another writer advanced the ref first. It replaces
// the file backend's acquireLock/releaseLock lock-line scheme.
func (b *GitBackend) transact(msg string, fn func(cur string) (next string, changed bool, err error)) error {
	for attempt := 0; ; attempt++ {
		head, ok, err := b.resolveHead()
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("%s not initialized", b.refName)
		}

		cur, err := b.readAtCommit(head)
		if err != nil {
			return err
		}

		next, changed, err := fn(cur)
		if err != nil {
			return err
		}
		if !changed {
			return nil
		}

		newCommit, err := b.writeSnapshot(next, []objectid.ObjectID{head}, msg)
		if err != nil {
			return err
		}

		err = b.casCommit(newCommit, head)
		if err == nil {
			return nil
		}
		if isContention(err) && attempt < maxRetries {
			// Another writer advanced the ref (or holds the lock) between our
			// read and our commit. Re-read and rebuild on the new tip.
			time.Sleep(time.Duration(rand.Intn(500)+100) * time.Microsecond)
			continue
		}
		return fmt.Errorf("CAS commit (attempt %d): %w", attempt, err)
	}
}

// casCommit advances the ref from head to newCommit via a furgit ref
// transaction: Update(name, newCommit, head) verifies the current value equals
// head under a lock before writing — go-git's CheckAndSetReference(new, old)
// expressed in furgit. If a serializer is installed it guards the whole
// transaction (furgit's files Transaction is MT-Unsafe).
func (b *GitBackend) casCommit(newCommit, head objectid.ObjectID) error {
	if b.serialize != nil {
		b.serialize.Lock()
		defer b.serialize.Unlock()
	}

	tx, err := b.repo.RefStore().BeginTransaction()
	if err != nil {
		return err
	}
	if err := tx.Update(b.refName, newCommit, head); err != nil {
		_ = tx.Abort()
		return fmt.Errorf("queue CAS update: %w", err)
	}
	return tx.Commit()
}

// history returns every committed tasks.json, newest first, by walking the
// ref commit's first-parent chain. This is recovery: every prior state is a
// reachable commit.
func (b *GitBackend) history() ([]string, error) {
	head, ok, err := b.resolveHead()
	if err != nil || !ok {
		return nil, err
	}

	var out []string
	id := head
	for {
		sc, err := b.repo.Fetcher().ExactCommit(id)
		if err != nil {
			return nil, fmt.Errorf("read commit %s: %w", id, err)
		}
		c := sc.Object()

		content, err := b.readTreeBlob(c.Tree)
		if err != nil {
			return nil, err
		}
		out = append(out, content)

		if len(c.Parents) == 0 {
			break
		}
		id = c.Parents[0]
	}
	return out, nil
}

// --- furgit plumbing helpers ---------------------------------------------------

// resolveHead resolves the ref to its commit id. ok is false (with nil error)
// when the ref does not yet exist.
func (b *GitBackend) resolveHead() (id objectid.ObjectID, ok bool, err error) {
	d, err := b.repo.RefStore().ResolveToDetached(b.refName)
	if errors.Is(err, refstore.ErrReferenceNotFound) {
		return objectid.ObjectID{}, false, nil
	}
	if err != nil {
		return objectid.ObjectID{}, false, fmt.Errorf("resolve %s: %w", b.refName, err)
	}
	return d.ID, true, nil
}

// readAtCommit returns the tasks.json content stored at the given commit.
func (b *GitBackend) readAtCommit(commitID objectid.ObjectID) (string, error) {
	sc, err := b.repo.Fetcher().ExactCommit(commitID)
	if err != nil {
		return "", fmt.Errorf("read commit %s: %w", commitID, err)
	}
	return b.readTreeBlob(sc.Object().Tree)
}

// readTreeBlob reads the tasks.json blob out of the given tree.
func (b *GitBackend) readTreeBlob(treeID objectid.ObjectID) (string, error) {
	st, err := b.repo.Fetcher().ExactTree(treeID)
	if err != nil {
		return "", fmt.Errorf("read tree %s: %w", treeID, err)
	}
	entry := st.Object().Entry([]byte(blobName))
	if entry == nil {
		return "", nil
	}
	sb, err := b.repo.Fetcher().ExactBlob(entry.ID)
	if err != nil {
		return "", fmt.Errorf("read blob %s: %w", entry.ID, err)
	}
	return string(sb.Object().Data), nil
}

// writeSnapshot writes content as a blob, wraps it in a single-entry tree, and
// commits that tree with the given parents. It returns the new commit id. The
// objects are written as loose git objects, readable by canonical git.
func (b *GitBackend) writeSnapshot(content string, parents []objectid.ObjectID, msg string) (objectid.ObjectID, error) {
	store := b.repo.ObjectStore()

	blobID, err := store.WriteBytesContent(objecttype.TypeBlob, []byte(content))
	if err != nil {
		return objectid.ObjectID{}, fmt.Errorf("write blob: %w", err)
	}

	t := &tree.Tree{}
	if err := t.InsertEntry(tree.TreeEntry{
		Mode: tree.FileModeRegular,
		Name: []byte(blobName),
		ID:   blobID,
	}); err != nil {
		return objectid.ObjectID{}, fmt.Errorf("build tree: %w", err)
	}
	treeBody, err := t.BytesWithoutHeader()
	if err != nil {
		return objectid.ObjectID{}, fmt.Errorf("serialize tree: %w", err)
	}
	treeID, err := store.WriteBytesContent(objecttype.TypeTree, treeBody)
	if err != nil {
		return objectid.ObjectID{}, fmt.Errorf("write tree: %w", err)
	}

	sig := b.signature()
	c := &commit.Commit{
		Tree:      treeID,
		Parents:   parents,
		Author:    sig,
		Committer: sig,
		Message:   []byte(msg + "\n"),
	}
	commitBody, err := c.BytesWithoutHeader()
	if err != nil {
		return objectid.ObjectID{}, fmt.Errorf("serialize commit: %w", err)
	}
	commitID, err := store.WriteBytesContent(objecttype.TypeCommit, commitBody)
	if err != nil {
		return objectid.ObjectID{}, fmt.Errorf("write commit: %w", err)
	}
	return commitID, nil
}

// signature returns the commit author/committer signature.
func (b *GitBackend) signature() objectsignature.Signature {
	now := time.Now()
	_, offSec := now.Zone()
	return objectsignature.Signature{
		Name:          []byte("meads"),
		Email:         []byte("meads@localhost"),
		WhenUnix:      now.Unix(),
		OffsetMinutes: int32(offSec / 60),
	}
}

// isContention reports whether err is a recoverable CAS conflict: either the
// ref moved under us (old value mismatch) or another writer currently holds
// the ref lock file.
func isContention(err error) bool {
	var mismatch *refstore.IncorrectOldValueError
	if errors.As(err, &mismatch) {
		return true
	}
	// furgit locks refs with O_CREATE|O_EXCL; a concurrent holder surfaces as
	// os.ErrExist rather than a value mismatch.
	return errors.Is(err, os.ErrExist)
}

// RefNames lists matching ref names (path.Match globbing; "" matches all). Used
// by the demo to show the ref exists and nothing leaks into refs/heads.
func (b *GitBackend) RefNames(pattern string) ([]string, error) {
	refs, err := b.repo.RefStore().List(pattern)
	if err != nil {
		return nil, err
	}
	names := make([]string, len(refs))
	for i, r := range refs {
		names[i] = r.Name()
	}
	return names, nil
}
