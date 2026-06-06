# Evaluating meads task 30 ("Git mode") with furgit ref plumbing

A proof-of-concept that implements task 30's git-mode **storage model** —
task state living purely in git under `refs/meads/tasks`, out of the working
tree — using [furgit](https://github.com/runxiyu/furgit)
(`lindenii.org/go/furgit`) for **all** object/ref plumbing instead of go-git.

Run it: `go run . demo` (self-contained, builds a temp repo). Or use the CLI:
`init`, `add`, `list`, `get`, `set-status`, `history`, `refdump`
(git dir via `$MEADS_GITDIR`, default `./.git`).

## What the PoC implements (task 30's model, verbatim)

```
mutation:  tasks.json bytes ─▶ blob ─▶ tree ─▶ commit ─▶ CAS-advance refs/meads/tasks
read:      refs/meads/tasks ─▶ commit ─▶ tree ─▶ tasks.json blob
history:   walk the ref commit's first-parent chain (= recovery)
init:      seed {"tasks":[]} as the first ("virtual") commit
```

`gitbackend.go` is the task-30 `backend` interface (`read` / `transact` /
`ensureInit` / `history` / `headContent`) over furgit. `main.go` is a thin CLI
+ a self-verifying demo.

## furgit ↔ go-git mapping (the "ref plumbing")

| task 30 (go-git)                         | furgit                                                                    |
|------------------------------------------|---------------------------------------------------------------------------|
| open repo                                | `repository.Open(*os.Root)`                                               |
| write blob/tree/commit                   | `repo.ObjectStore().WriteBytesContent(type, body)` + `object/{blob,tree,commit}` serializers |
| read typed objects                       | `repo.Fetcher().Exact{Commit,Tree,Blob}` / `PeelToTree`                    |
| read ref                                 | `repo.RefStore().ResolveToDetached(name)`                                 |
| **`CheckAndSetReference(new, old)`**     | `tx := refs.BeginTransaction(); tx.Update(name, new, old); tx.Commit()`   |
| seed ref                                 | `tx.Create(name, id)`                                                     |
| walk parents                             | `ExactCommit(id).Object().Parents`                                        |

The mapping is clean: furgit's narrow-interface architecture (objects are plain
values; object-store and ref-store are separate capability interfaces) fits the
backend abstraction well, and its `Transaction.Update(name, new, old)` is a
faithful expression of compare-and-set.

## Findings

### ✅ The storage model is sound, and furgit writes real git
- `git status` stays **clean** — no tracked task file; tasks never touch the
  working tree.
- furgit's loose objects are **byte-for-byte canonical git**: the demo verifies
  `git cat-file refs/meads/tasks:tasks.json` equals what furgit reads, and
  `git log refs/meads/tasks` shows exactly one commit per mutation.
- **History/recovery** works by walking parents (`[3 3 2 1 0]` task counts,
  newest→oldest). Every prior state is a reachable commit.
- The ref lives outside `refs/heads/*`, so it never pollutes branches, `git
  status`, or a normal `git push`.

Single-process, the furgit-backed git mode does everything task 30 wants.

### ❌ furgit's ref Transaction is **MT-Unsafe** — it cannot provide the
###    "native optimistic locking" task 30 attributes to go-git

furgit's files `Transaction` is explicitly documented `MT-Unsafe`, and the PoC's
**probe A** (16 independent handles ≈ 16 processes racing to add a task, no
external lock) reproduces real damage every run:

- 2–7 of 16 writers fail with `renameat … no such file or directory` or
  `openat refs/meads/tasks.lock: no such file or directory`; and
- occasionally the ref is **torn-written to a 0-byte / broken state**
  (`broken reference: got 0 chars, expected 40`) — i.e. *ref corruption*.

Root cause (read from furgit `ref/store/files`):
1. `cleanupPreparedUpdates` removes the `.lock` file for every target **even
   when this transaction didn't create it** → the loser unlinks the winner's
   lock (lock names aren't owned per-transaction).
2. `tryRemoveEmptyParentPaths` / `removeEmptyDirTree` prune `refs/meads/` when
   it is momentarily empty → ENOENT between a writer's `MkdirAll` and its
   `OpenFile(..., O_EXCL)`.
3. A stray fresh 0-byte `O_EXCL` lock can get `rename`d into the ref, leaving an
   empty (broken) ref.

So furgit's `O_EXCL` lock is **not** a robust cross-writer mutex. go-git's
`CheckAndSetReference`, by contrast, is a process-safe atomic CAS via a proper
lockfile — which is the exact property task 30 is counting on to "replace the
lock-line scheme."

### ✅ The CAS *logic* is correct once the furgit txn is serialized
**Probe B** runs the same 16-way race but serializes only the furgit
transaction (reads/applies still run concurrently, so the retry path is
exercised). Result every run: **all 16 land, ids unique, no lost updates**.
That isolates the problem to furgit's lock *implementation*, not the CAS model.

## Recommendation for task 30

- **Keep go-git for the MVP.** Its `CheckAndSetReference(new, old)` gives the
  process-safe atomic CAS the design depends on, plus API-stability promises.
  furgit itself says *"You should not use furgit"* (research project, breaking
  API "every few days", `MT-Unsafe` transactions, and it requires Go ≥ 1.26).
- If furgit is ever desired, md git-mode would have to **wrap every ref write in
  its own `flock`** (≈ the lock-line/optimistic-lock scheme task 30 set out to
  *remove*), which negates furgit's "native CAS" appeal — or furgit must first
  fix per-transaction lock ownership and parent-dir pruning upstream.
- Everything *else* in furgit (object writing, typed reads, parent walks, git
  byte-compatibility) is solid and could back the read/history paths today; only
  the **locked-write/CAS** path is unsafe under concurrency.

Net: task 30's design is validated, go-git remains the right backend for the
locked-write path, and the concurrency requirement ("run two `md add` in
parallel; no lost update") is the load-bearing test that furgit currently fails.
