# TASKS

a [meads](https://github.com/jpillora/meads) (`md`) managed task log

* created: 2026-02-14T11:42:09Z
* updated: 2026-07-27T10:25:05Z
* next-id: 13

## 20. VS Code extension end-to-end manual test

* status: draft
* priority: P2
* type: feature
* depends-on: 
* created: 2026-05-21T07:29:29Z
* updated: 2026-07-25T08:13:16Z

VS Code extension end-to-end manual test
Package the extension locally (vsce package in vscode/), install the .vsix in **desktop VS Code** (NOT 'code serve-web' / vscode.dev / Codespaces — those are blocked by mixed-content; see #26), open a TASKS.md, confirm webui renders inside the webview, confirm changes round-trip, confirm bind-vscode JSON-RPC works (vscode.openFile, vscode.showQuickPick, vscode.copyToClipboard, vscode.openExternal, vscode.showMessage), confirm subprocess cleanup on tab close. Note: bearer token currently rides in the iframe query string — fine inside a sandboxed webview but worth a glance during review.

## 21. meads.minMdVersion setting is dead code

* status: open
* priority: P3
* type: bug
* depends-on: 20
* created: 2026-05-21T07:29:29Z
* updated: 2026-05-21T07:29:34Z

The setting is declared in vscode/package.json and getVersion() exists in vscode/src/mdBinary.ts, but nothing ever calls getVersion or compares against minMdVersion. Either wire it up (warn on mismatch in resolveMd) or remove both the setting and getVersion().

## 22. Cut first VS Code extension release (v1)

* status: open
* priority: P2
* type: task
* depends-on: 20,21
* created: 2026-05-21T07:29:29Z
* updated: 2026-05-21T07:53:42Z

After the e2e test passes, push a v* tag — the release_vscode job in .github/workflows/ci.yml will package meads-vX.Y.Z.vsix and attach it to the GitHub release.

## 23. Restore VS Code extension section to README

* status: open
* priority: P3
* type: task
* depends-on: 22
* created: 2026-05-21T07:29:29Z
* updated: 2026-05-21T07:29:34Z

Once the .vsix is attached to a real release, re-add the 'Web UI + VS Code extension' section to README.md with install instructions (download .vsix from Releases, install via 'Extensions: Install from VSIX...').

## 26. extension doesn't work in code serve

* status: draft
* priority: P3
* type: bug
* created: 2026-05-21T16:36:32Z
* updated: 2026-06-19T11:59:05Z

extension doesn't work in code serve-web / vscode.dev / Codespaces
The webview iframe loads md webui over HTTP, but VS Code's web client hosts webviews on an HTTPS origin (*.vscode-cdn.net). Chrome blocks the iframe as mixed-content before VS Code's portMapping/asExternalUri proxy can intercept (verified empirically with code serve-web 1.103.2 + agent-browser: no request reaches md webui's HTTP handler). Fix options: (a) serve md webui over HTTPS with a self-signed cert; (b) refactor the webview to load static assets via webview.asWebviewUri and proxy all API/SSE/WS traffic through the existing /bind-vscode channel; (c) document desktop-only and stop pretending. Track this before promoting the extension beyond desktop.

## 30. Git mode plan v1

* status: draft
* priority: P2
* type: idea
* created: 2026-06-06T01:24:43Z
* updated: 2026-07-25T05:17:57Z

Git mode plan v1

### Git mode for meads

#### Context

Today every task lives in a working‑tree `TASKS.md`/`.csv`, mutated under an append‑"lock line" optimistic lock. Task 30 ("Git mode") wants task state to live **purely in git** — as JSON in a dedicated ref, *out of the working tree* — so there is no tracked file to diff/stage, history is the ref's commit log, and `md init` seeds an empty tasks JSON as the first ("virtual") commit. Goal: same `md` UX and speed, but the backing store is a git ref instead of a file.

#### Why these choices (measured this session, not assumed)

- **Remote ref write ≈ 2.5 s** (`ls-remote` to `origin` over SSH; dominated by handshake/RTT, payload‑independent) vs **local git op ≈ 3 ms**. ⇒ tasks must be written to a **local** ref; the remote is sync‑only, never on the command hot path.
- **go-git in‑process = 0.70 ms/write** for the full `blob→tree→commit→CAS ref`path vs **git‑CLI 4‑spawn = 31 ms/write** → **44× faster** (real on‑disk repo, arm64). ⇒ use **go-git** for all local read/write/history; reserve the network for the user's own `git push`/`fetch`.

#### Storage model

- Ref `refs/meads/tasks` → commit → tree → single blob `tasks.json`(`{"meta":{…},"tasks":[…]}`).
- Each mutation = a new commit parented on the prior; the ref is advanced via **CAS** (`CheckAndSetReference(new, old)`) — native optimistic locking that replaces the lock‑line scheme.
- History/recovery = walk the ref's commit parents, reading `tasks.json` at each.

#### Design — centralize behind ONE backend (per "centralise the heavy lifting")

All mutation/format/query logic stays shared; only *read*, *locked‑write*, and *history* differ per backend.

New `pkg/meads/backend.go`:

```go
type backend interface {
    read() (string, error)                 // current tasks.json (missing => "")
    transact(func(cur string) (next string, changed bool, err error)) error // lock/CAS + retry
    ensureInit(empty string) error
    history() ([]string, error)            // serialized content, newest→oldest
    headContent() (string, bool)           // committed HEAD content (for committedIDs)
}
```

- `fileBackend` — wraps today's code unchanged: `util.ReadFile`+`stripLockLines`, `acquireLock`/`releaseLock` (`lock.go`), CLI‑`Git` history (`git log --all -- file`, `git show hash:file`).
- `gitBackend` — go-git `storage/filesystem` storer over `osfs` (already vendored via go-billy) + `plumbing`/`object`/`filemode`: `read` = blob at the ref; `transact`= read ref commit → apply → write blob/tree/commit → `CheckAndSetReference` with a short retry loop on contention; `history` = walk parents.

`Store` keeps its `Format` and gains a `be backend`. Refactor `mutate.go`(`Add/AddMany/Delete/DeleteMany/Update/Doctor/AutoClean`) from `acquireLock()/releaseLock()` to `be.transact(…)`, and `query.go`(`Get/Ready/GetHistory/GetWithHistory/committedIDs`) to read via `be.read()/history()/headContent()`. One transaction shape; both backends reuse it.

#### New JSON format

`pkg/meads/json.go`: `jsonFormat` implementing `Format` — `Parse`=`json.Unmarshal`→`File`, `Format`=indented `json.Marshal`, `HasPreamble()=true`, `EmptyFile()=` `{"tasks":[]}`. Reuses existing `File`/`Task` JSON tags + `Task.MarshalJSON`. Self‑consistent round‑trip: all `md` logic reads struct fields (the `t.Meta["status"]` etc. are only *written* by setters), and project `File.Meta` (`created`/`updated`/`max-id`) round‑trips as‑is.

#### Wiring / detection (auto‑detect + override)

- `globals.store()`: `--file` (or no git repo) → fileBackend; `--git`/`MEADS_GIT=1` → gitBackend; else **auto** = gitBackend iff `refs/meads/tasks` exists (one cheap ref lookup). Git mode pins `format=json`, in‑tree name `tasks.json`.
- Add `--git`/`--file` globals (+ `MEADS_GIT`) in `cmd/md/main.go`.

#### `md init --git`

- Open repo (go-git); error if `refs/meads/tasks` already exists.
- Seed empty `{"tasks":[]}` blob→tree→commit (`meads: init`)→`SetReference` — the "first virtual commit."
- Configure a **fetch** refspec on `origin`: `+refs/meads/*:refs/meads/*` (additive, safe).
- Publish on push **without hijacking** `push.default`: install a **pre‑push hook**(reuse the hook plumbing in `cmd/md/auto_delete.go`) that runs `git push origin refs/meads/tasks`. ⚠️ Deliberately do NOT set `remote.origin.push`— configuring any push refspec replaces git's matching/simple default and would break normal branch pushes. Also print the manual push command.

#### Scope — MVP behind the backend (reading the unselected scope + "centralise" as MVP‑first)

**In:** backend abstraction, `gitBackend` (go-git), `jsonFormat`, detection, `md init --git`, full CRUD, history/recovery, webhook (unchanged — it lives at the command layer).

**Deferred as new** `md` **tasks** (file‑assuming integrations + the genuinely hard part):

- **Multi‑clone sync/merge** — a single shared mutable ref *diverges* across clones, so fetch must MERGE (union by ID + `doctor`‑style dedup/tombstones), not force‑overwrite. This is the real distributed‑hard problem, analogous to today's `md doctor` for file merges. MVP keeps fetch non‑force so divergence fails loudly rather than losing tasks.
- **webui watch** (`pkg/webui/watch.go`) — fsnotify watches a path; git mode must watch/poll the ref (`.git/refs/meads/tasks` / `packed-refs`).
- **auto‑delete hook** (`cmd/md/auto_delete.go`) — stages a working‑tree file on pre‑commit; in git mode there is no staged file (each change is already a ref commit) → rework or make it a no‑op.
- **convert/migrate** `TASKS.md ↔ git`; note md↔json conversion must sync struct fields→`Meta` so the markdown formatter still emits per‑task `* status:` lines.

#### Files

- **New:** `pkg/meads/backend.go`, `pkg/meads/git_backend.go`, `pkg/meads/json.go`.
- **Refactor:** `store.go` (hold `backend`), `mutate.go` + `query.go` + `lock.go`(move file logic into `fileBackend`), `git.go` (keep `Git` for any CLI remote bits).
- **CLI:** `cmd/md/main.go` (globals + detection in `store()`), `cmd/md/init.go` (`--git`).
- **Deps:** add `github.com/go-git/go-git/v5`.

#### Verification

- Build: `go install ./cmd/md` (per project rule, from `cmd/md/`).
- Tests: parametrize the existing `pkg/meads` + `e2e` suites over both backends — `e2e`already uses `memfs`; add a git‑backed `Store` fixture in a temp repo. Assert CRUD/ready/deps/doctor/tombstone parity between file and git modes.
- Manual smoke (temp repo): `git init` → `md init --git` → confirm `git status` is clean and `git for-each-ref refs/meads` shows the ref → `md add/update/del/get/ready`→ `git log refs/meads/tasks` shows one commit per change → `md get <deleted-id>`recovers from history.
- Concurrency: run two `md add` in parallel; confirm CAS retry yields both (no lost update).
- Speed: confirm git‑mode `md add` stays single‑digit‑ms (the 0.70 ms write path).

## 56. Git mode plan v2

* status: draft
* priority: P2
* type: idea
* created: 2026-07-25T05:19:23Z
* updated: 2026-07-25T06:47:58Z

Git mode plan v2

---

A global task space, saved inside git, orthogonal to files, as git refs

Designed to operate globally at the repo level, spanning all active branches (main + work trees); treat it like a localhost API, stored separately from the repository

It assumes exactly one trunk branch, defaulting to HEAD branch (default branch)

Design:

- One repo ref for the lock `/refs/meads/lock` **all changes** must set `lock` to
  -  `{"locked":true, "type":"local"}` 


- One repo ref for config `/refs/meads/config` is a `{...}` JSON config object
- One repo ref for metadata `/refs/meads/metadata` is a `{...}` JSON config object
  - Latest task
  - Last update
- One repo (global) ref per task `/refs/meads/tasks/42`
- Auto-push, configurable interval
- Per-repo config
- Agents in worktrees can mark `/refs/meads/tasks/42/metadata` as `{inprogress:true}`

## 57. Git mode v3

* status: draft
* priority: P2
* type: idea
* created: 2026-07-25T07:28:16Z
* updated: 2026-07-25T11:18:38Z

Git mode v3

Supersedes tasks 30 (v1) and 56 (v2). Design by OJ; requirements by JP.

### Requirements (JP)

- A global task list per repo, saved as `refs/meads/*`
- Two agents, each in their own worktree, must share the same task list
- `md update` (and all mutation commands) are guaranteed atomic
  - 2 agents racing to claim task `42` as `status=inprogress` cannot both claim it
- `md list` (and `get`, any read command) are fast
- A global set of config vars, maybe `refs/meads/config`, where you can:
  - optionally set `remoteLocking` to `true`; when set, "acquire lock" must attain a remote lock
  - optionally set `pushInterval` to `<duration>`, deciding how often to push to the
    remote; default `1m` (every change looks at the last updated timestamp, and if
    time since > `1m` then push)
- All mutations save a task version; versions must not slow down `md` operations.
  Looking at old versions can be slow; "current state" must be fast
- Agents communicate progress by updating their task ref:
  - `status` → `inprogress` to signal claiming
  - `agentId` → `<claude-session-id>` for traceability
  - `description` after planning mode completes
  - `filesInScope` → list of files the agent MAY write

### Verified primitives

Measured this session against real remotes (GitHub, Gitea 1.25.5). See
`doc/GIT_REF_TESTS.md`; re-probe any new host with `doc/git-ref-probe.sh`.

- `refs/meads/*` push accepted by both hosts; advertised in `ls-remote`; **not**
  fetched by a default clone — needs an explicit refspec.
- **Server-enforced CAS.** Proven with a raw `git-receive-pack` POST carrying a
  falsified old-oid; both hosts rejected it and left the ref unchanged.
  `--force-with-lease` alone does NOT prove this — its check is client-side.
- **Atomic multi-ref push** (`atomic` capability) on both hosts: a mixed batch
  (one valid CAS, one stale) rejected *both* and rolled back the valid update.
  Same all-or-nothing locally via `git update-ref --stdin` (start/prepare/commit).
- **create-if-absent** = CAS against the 40-zero oid. **CAS delete** =
  `update-ref -d <ref> <old>`.
- Argument order differs by layer — local `update-ref <ref> <NEW> <OLD>`, wire
  `<OLD> <NEW> <ref>`. **Omitting OLD silently disables CAS.**
- Commits/trees build via `hash-object` → `mktree` → `commit-tree` with **zero
  worktree or index involvement**; verified across two linked worktrees sharing
  one ref store, both with clean `git status` throughout.
- Rejection text is not portable: GitHub says `cannot lock ref: is at X but
  expected Y`, Gitea says `failed to update ref`. **Never branch on message text.**

Read performance (git CLI, this session):

| total refs | `md list` equivalent (500 tasks) | single ref lookup |
| --- | --- | --- |
| 10,500 | 22–25 ms | 2 ms |
| 100,500 | 22 ms | 2 ms |

Flat, because reads prefix-scan `refs/meads/tasks/` only.

Next-ID computation from ref names alone (no blob reads): **15 ms at 500 tasks,
39 ms at 5,000, 389 ms at 50,000** — linear, ~8 µs/ref, and that includes CLI
spawn plus an `awk` pipe.

Carried forward from task 30: go-git in-process ≈0.70 ms/write vs git-CLI
4-spawn ≈31 ms/write (44×); a remote ref op ≈2.5 s over SSH; a local git op ≈3 ms.

### Storage model

```
refs/meads/tasks/<id>  → commit → tree → task.json     one ref per task, forever
refs/meads/config      → commit → tree → config.json   user settings
refs/meads/lock        → blob                          only when remoteLocking=true
```

There is **no `meta` ref and no stored next-id counter** — see below.

Local-only, never a ref and never pushed: last-push timestamp at
`$GIT_COMMON_DIR/meads/last-push`. Push cadence is per-clone state; putting it in
a shared ref would make every clone fight over it.

`task.json`:

```json
{
  "id": 42,
  "title": "Fix login",
  "description": "...",
  "status": "inprogress",
  "priority": "P2",
  "type": "bug",
  "deleted": false,
  "dependsOn": [5],
  "tags": [],
  "agentId": "<claude-session-id>",
  "filesInScope": ["pkg/meads/lock.go"],
  "created": "...",
  "updated": "..."
}
```

No `version` field — the version *is* the commit depth, and duplicating it in the
payload creates a second source of truth that can drift.

#### Why commits, not blobs

This is what satisfies "all mutations save a version" with the required
performance asymmetry:

- current state = ref tip, one lookup — **fast**, as required
- old versions = walk parents — **slow**, explicitly allowed
- version N = `refs/meads/tasks/42~N`
- timestamps come from commit metadata
- old versions stay reachable → GC-safe with no extra refs
- keeps a `git log`-based history path

#### Why not `refs/meads/task-versions/<id>/<n>`

Rejected on measurement. 500 tasks × 200 versions = 100,500 refs →
`git pack-refs` ran **over 5 minutes** (killed with 68,263 loose refs still
unlinked) and produced a **7.1 MB** `packed-refs`, which is advertised on every
fetch/push by every agent. Ref count would grow with *mutations*, not tasks.
The commit chain gives identical time-travel at one ref per task.

Cost accepted: version N is a walk (`~N`) rather than an O(1) named ref.

#### Why no index/cache ref for `md list`

22 ms for 500 tasks, flat to 100k refs, and go-git should beat that. An index ref
would be a global contention point on *every* mutation — worse concurrency to fix
a problem the measurements say we don't have. Revisit only if numbers change.

### Soft delete, and no next-id counter

**Tasks are never hard-deleted in git mode.** `md del` sets a boolean `deleted`
field and CASes the task ref like any other update. The ref persists forever.

Rationale (JP): the current codebase allows hard deletes *precisely because* tasks
are rows in a committed file, so git history recovers them. Refs break that
assumption — moving or removing a task ref orphans its chain, so the file-mode
justification for hard delete does not carry over.

This ports the semantics the CSV backend already has ("soft-delete and computed
next-id" — README), reusing the existing `Task.Deleted` boolean and
`filterDeleted()` (`tombstone.go:115`).

**Use the existing boolean, not a new `status` value.** Status is
draft/open/inprogress/closed; folding deletion into it loses what the task's
status *was* when deleted and forces changes to status validation.

**Next ID is computed on demand**: `max(id from ref names) + 1`. Measured at
15 ms / 500 tasks using ref names only. Because refs persist, the max is always
correct, so git mode does **not** need the `f.Meta["max-id"]` high-water-mark
machinery (`tombstone.go:54-98`) that the file format requires — that exists only
because the file compacts tombstones away, retaining a single tombstone row for
the highest deleted task.

Consequence, which is the point: task 14 stays present as a deleted ref forever,
so a *new* task 14 can never be allocated. No ID reuse, no ambiguity in commit
messages or history.

Cost: `md list` reads every task ref and filters, so its cost tracks
tasks-ever-created rather than live tasks. 22 ms at 500; extrapolating linearly,
~90 ms at 2,000 and ~220 ms at 5,000. If that ever bites, the escape hatch is a
`refs/meads/deleted/` namespace so `md list` prefix-scans only live tasks while
next-ID scans both — **not** worth building now.

### Concurrency

Correctness rests on per-ref CAS, not on a lock.

- **Single-task mutation** (including delete) — CAS on that task's ref. Changes to
  different tasks never contend.
- **Task creation** — a single create-if-absent CAS on `refs/meads/tasks/N`, where
  N is the computed next ID. **No atomic batch, no shared counter ref.** The
  create-only CAS *is* the uniqueness guarantee: if two agents both compute 58,
  exactly one wins; the loser recomputes, gets 59, retries. This removes the
  global contention point a `meta.nextId` ref would have introduced on every
  create.
- **Bulk ops** (`doctor` renumber) — a single atomic batch over every affected
  ref, each with its expected old-oid.
- **Soft delete** also needs an atomic batch. CORRECTION (found in phase 3): an
  earlier draft said `doctor` was the only user of the batch primitive. That was
  wrong. `Store.Delete` also strips the deleted id from every other task's
  `DependsOn`, so git mode must update the deleted task's ref and every
  dependent's ref together or not at all. Doing it non-atomically could leave
  the store half-updated. The symptom of skipping the cleanup is not a `Ready`
  blockage — `Ready` already treats a dependency on a deleted task as satisfied,
  because `filterDeleted` runs before blocking is computed — it is that
  `validateDeps` builds its known-id set from non-deleted tasks, so a dangling
  `DependsOn` makes every future `Update` on the dependent fail permanently.

**The retry trap.** A CAS retry must re-evaluate the *decision*, not replay the
*write*. Naive "CAS failed → re-read oid → push again" succeeds on attempt 2 and
silently stomps the winner's claim:

```
loop:
  oid, task = read(refs/meads/tasks/42)
  if task.status != open: return ErrAlreadyClaimed   ← re-checked every iteration
  cas(oid → newCommit)
  if ok: return
```

Make old-oid a **required** parameter throughout the internal API — never expose
an unconditional update, and this bug class disappears at the type level.

### Locking (two-stage, opt-in)

Per-ref CAS already prevents lost updates, so the lock exists only for
multi-step operations that can't be one batch (format migration, long
maintenance). Default off — which also preserves offline operation.

1. **Local** — `flock` on `$GIT_COMMON_DIR/meads.lock`. Chosen over the current
   lock-line scheme because the kernel releases it on process death (crash,
   `kill -9`, OOM), so it cannot leak; and living in the common git dir means it
   is automatically shared by all worktrees. Needs build-tagged impls (unix
   `flock`, Windows `LockFileEx`). Guard against self-deadlock with an in-process
   mutex + refcount — two `flock` calls on separate fds in one process block.
2. **Read config** — cheap; see caching below.
3. **Remote** — only if `remoteLocking=true`. Acquire = CAS `refs/meads/lock`
   from the 40-zero oid (create-if-absent). Release = CAS delete with your own
   oid, which also makes it impossible to release someone else's lock. Lease with
   `{holder, expires}`; steal an expired lock via CAS so exactly one stealer wins.

Ordering: acquire local → remote; release remote **after** the data write, since
the remote lock brackets the whole critical section. If remote acquisition fails,
release local before returning. If `remoteLocking=true` and the network is
unavailable, **fail closed** — silently proceeding is the one outcome that loses
mutual exclusion invisibly.

Detect "lock is held" by re-reading the ref, never by matching rejection text.

### Config and caching

`config.json`: `{"remoteLocking": false, "pushInterval": "1m"}` — both optional,
defaults as shown.

Reading config is one ref lookup + one blob read (~2 ms local). Cache the parsed
value keyed by the ref's oid: the oid changes iff config changed, so a cheap
lookup validates the cache without re-parsing.

`remoteLocking` must live in this shared ref rather than local git config — a
lock protocol only works if every participant follows it, and a per-clone setting
lets one misconfigured agent void mutual exclusion for everyone with no error
anywhere.

### Auto-push

- After each mutation, compare now against `$GIT_COMMON_DIR/meads/last-push`; if
  elapsed > `pushInterval`, trigger a push.
- **Push synchronously, with a bounded timeout.** A remote ref op measured
  ≈2.5 s. REVISED (JP's call, phase 6): blocking one command per push interval
  is acceptable, and far simpler than a detached child process — it removes the
  setsid/detach platform split and, more importantly, reports divergence in the
  command that caused it rather than one command later. The timeout is the part
  that must not be dropped: an unreachable remote can stall for far longer than
  a normal push. On timeout, warn and mark pushed; never fail the mutation.
- Push `refs/meads/*:refs/meads/*`. Set a **fetch** refspec
  `+refs/meads/*:refs/meads/*` on origin at init (additive, safe).
- **Do NOT set `remote.origin.push`** — configuring any push refspec replaces
  git's matching/simple default and breaks ordinary branch pushes. Use an
  explicit refspec at push time (or a pre-push hook, reusing the hook plumbing in
  `cmd/md/auto_delete.go`).

### Worktree sharing

Free. `refs/` lives in the common git dir, so linked worktrees share one ref
store and one object store — verified: agent 1 created a task in worktree A and
agent 2 read it in worktree B with no fetch and no checkout, both worktrees clean
throughout. Across clones, sharing is push/fetch of the same refs.

### filesInScope

Advisory only — a loose declaration so agents can see what others are working on.
Not arbitrated, not a lock, no index ref. It rides along on the normal per-task
CAS. A stale entry is merely misleading, not blocking. A read command unions
`filesInScope` across `inprogress` tasks to show current activity; `md ready` can
optionally deprioritise tasks whose files are already claimed.

### Risks / open questions

- **Offline divergence (hardest).** Two clones mutate task 42 while offline; both
  build commit chains from a common parent, and the second push is rejected
  non-fast-forward. Needs a reconcile: fetch, then merge two task JSON states.
  MVP should fail loudly rather than lose data. Phase separately.
- **Duplicate IDs offline.** Two clones both create task 58 — create-if-absent
  can't coordinate across a partition (a shared counter wouldn't have helped
  either). Same class as today's `md doctor`; renumbering must extend to git mode.
- **`md ready` guarantees contention** — every agent asks and gets the same top
  task. CAS handles it correctly, but shuffling among equal-priority tasks avoids
  the wasted round-trip.
- **Untested hosts** — GitLab, Bitbucket, Gerrit (Gerrit reserves `refs/for/*`,
  `refs/changes/*` and is the likely failure). Run `doc/git-ref-probe.sh`.
- **GC of custom-namespace refs** — untested whether any host expires them.
- `reftable` backend (git 2.45+, we're on 2.43.0) would change ref-scale
  behaviour; not needed given one-ref-per-task, but worth revisiting.

### Suggested phases

1. Ref storage layer — commit/tree/blob plumbing, CAS wrappers with mandatory
   old-oid, atomic batch helper (needed only for `doctor`).
2. Read path — `list`/`get`/`ready` over `refs/meads/tasks/*`, filtering
   `deleted`; history walk; computed next-id.
3. Write path — create (single create-if-absent CAS), update, soft delete;
   retry with precondition re-check.
4. Config ref + oid-keyed cache.
5. `md init --git`, fetch refspec, detection/auto-select of git mode.
   Detection keys on the whole `refs/meads/` namespace, NOT `refs/meads/tasks/*`.
   CORRECTION (found in phase 5): a tasks-only check cannot bootstrap — a freshly
   initialised repo has no tasks, so the first `md add` resolves to file mode and
   writes `TASKS.md` into the working tree. `init --git` writes the config ref so
   the namespace is non-empty from the start. Every unit test passed with this
   bug present; only an end-to-end run caught it.
6. Auto-push (async, `pushInterval`).
7. Two-stage lock, `remoteLocking` opt-in.
8. Reconcile/merge for divergence + `doctor` for git mode.
9. Integrations: webui watch (poll refs, not fsnotify on a file); `md auto-save`
   and `md auto-delete` become **no-ops in git mode** — there is no working-tree
   file to stage, and nothing to prune since refs are never removed.

## 68. pkg/meads -race fails on TestConcurrentWriters_OneWins (pre-existing)

* status: open
* priority: P2
* type: bug
* created: 2026-07-25T09:32:33Z

`go test ./pkg/meads/ -race` fails on `TestConcurrentWriters_OneWins`
(pkg/meads/lock_test.go:132) with "race detected during execution of test".

### Pre-existing, not caused by git mode

Verified by checking out commit 69581ab (the state before any git-mode work) into
a scratch worktree under /tmp — none of refstore.go / gitstore.go / gitmutate.go
present — and running the same test under `-race`. It fails identically there.

Also reported as functionally flaky without `-race`: "expected exactly 1 winner,
got 2" under `-count=5`.

### What to determine

The test drives concurrent `acquireLock` calls against a shared in-memory billy
filesystem. The key question is whether this is:

1. **A test artifact.** Real concurrency in meads is across *processes*, which do
   not share memory — they contend through the actual file via O_APPEND. An
   in-process test sharing one memfs may simply be racing on memfs internals that
   are not safe for concurrent use, in which case the lock design is fine and the
   test needs a process-level or lock-protected fixture.
2. **A real defect.** The README advertises "Concurrent-write safe via optimistic
   locking — multiple processes or AI agents can write simultaneously". If the
   "got 2 winners" outcome can occur across processes on a real filesystem, the
   optimistic lock has a genuine hole and that claim is wrong.

Distinguishing these matters: (1) is a test fix, (2) is a correctness bug in a
headline feature.

Suggested: write a process-level reproduction (spawn N `md` processes against one
real TASKS.md in a temp dir under /tmp and count winners) to settle which it is
before touching the lock code.

### Note

Git mode does not inherit this. Its concurrency is server/filesystem-enforced
compare-and-swap on refs, covered by TestGitStore_Claim_ConcurrentRaceExactlyOneWinner
and friends, which pass cleanly under `-race`.

### Acceptance

- `go test ./pkg/meads/ -race` passes
- The question above is answered explicitly, and if it is (2), the README claim is
  corrected or the lock is fixed

## 72. Task edits made in the same commit that closes them are lost

* status: open
* priority: P2
* type: bug
* created: 2026-07-25T22:33:45Z

Editing a task and closing it before the next commit silently discards the edit.

### What happens

`AutoClean` (pkg/meads/mutate.go) prunes a closed task from TASKS.md if its ID
is in `committedIDs`, i.e. present in `HEAD:TASKS.md`. It does not compare the
working-tree row against the committed one. So for a task that already exists at
HEAD:

1. `md update <id> --description-file=...` writes the new text to TASKS.md
2. `md set-status <id> closed`
3. `git commit` — the pre-commit hook prunes the row and stages the removal

The commit therefore contains TASKS.md *without* the task. The newest committed
version of that task is the one from the previous commit, carrying the **old**
description and `status: open`. `md get <id>` recovers that stale version, so
the task looks recoverable while the edit is gone.

### Observed

Hit while closing #67 on 2026-07-26. A ~60-line root-cause write-up was added to
the task description, the task was closed, and both landed in one commit:

```
$ git show HEAD~1:TASKS.md | grep -c "RESOLVED 2026-07-26"   # 0
$ git show HEAD:TASKS.md   | grep -c "RESOLVED 2026-07-26"   # 0
```

The write-up was preserved out-of-band in doc/INDEX_LOCK.md and the commit
message, but only because the loss was noticed. This is the common shape of a
"do the task, document it, close it" workflow, so it is likely to have eaten
earlier notes too.

### Why it matters

The whole premise of auto-delete is that pruning is safe because git history
retains the task. That holds only for the *last committed* version. Any edit
made after that commit and before the closing one is lost with no warning —
including exactly the final write-up a closing commit is most likely to add.

### Possible approaches

- Prune only when the working-tree row is byte-identical to the committed one;
  otherwise let the edited row ride along in this commit and prune it in the
  next. Costs one extra commit per closed-and-edited task, and is simple.
- Or have the hook stage the edited row first, then prune, so a single commit
  carries both. Needs two index writes and is harder to make atomic.
- Either way `md get <id>` should be able to tell that the recovered version is
  older than the row that was pruned, rather than presenting it as the truth.

### Acceptance

- Editing a task's description and closing it in the same commit preserves the
  edit somewhere in git history
- A regression test covers edit-then-close-in-one-commit

## 73. Git mode LoadAll spawns 4 git processes per task

* status: open
* priority: P1
* type: bug
* created: 2026-07-25T22:44:04Z
* updated: 2026-07-25T23:18:15Z

`GitStore.LoadAll` (pkg/meads/gitstore.go) reads each task with its own
`ReadFileAtRef`, which is `for-each-ref` + `cat-file blob` — two processes per
task, on top of the `ListRefs` that already enumerated them. Every `md list`,
`md ready` and `md get` in git mode pays it.

### Measured

Scratch git-mode repo, 100 tasks, git wrapped in a counting shim:

```
$ time md list
real  0m1.314s
user  0m0.357s
sys   0m0.965s        <- sys > 2x user: this is fork/exec, not work
git invocations: 404
    203 for-each-ref
    200 cat-file
      1 rev-parse
```

~4 git processes and ~13ms per task, scaling linearly.

### The fix costs one pipe

git already has the batch plumbing. `cat-file --batch` accepts `<ref>:<path>`
on stdin and streams every object back over a single process:

```
$ time (git for-each-ref --format='%(refname):task.json' refs/meads/tasks/ \
        | git cat-file --batch | wc -c)
12984
real  0m0.013s
```

**1.314s -> 0.013s. 404 processes -> 2.** Same 100 tasks, same content, no new
dependency.

### Approach

- Add a batch read to RefStore: feed `<ref>:<path>` lines to
  `git cat-file --batch` and parse the `<oid> <type> <size>
<payload>
`
  frames. Needs a streaming call on the Git interface — the current
  `OutputWithInput` buffers, which is fine at this size but worth noting.
- `LoadAll` then becomes one `ListRefs` + one batch read.
- `ReadFileAtRef` stays for single-task paths; the CAS/write path is untouched.

Note `LoadAll` currently also discards the per-ref OID it resolves; the batch
frames carry the oid, so nothing is lost.

### Acceptance

- `md list` over N tasks issues O(1) git processes, not O(N)
- A test asserts the invocation count does not scale with task count
- Existing git-mode behaviour and CAS semantics unchanged

### Also: every command reads the whole store TWICE

The invocation trace for `md list` in a 2-task git-mode repo shows the entire
read sequence repeated verbatim:

```
for-each-ref refs/meads/            <- mode detection
rev-parse --git-dir                 <- inGitRepo
for-each-ref refs/meads/tasks/      <- LoadAll #1
for-each-ref refs/meads/tasks/1
cat-file blob refs/meads/tasks/1:task.json
for-each-ref refs/meads/tasks/2
cat-file blob refs/meads/tasks/2:task.json
for-each-ref refs/meads/tasks/      <- LoadAll #2, identical
for-each-ref refs/meads/tasks/1
cat-file blob refs/meads/tasks/1:task.json
for-each-ref refs/meads/tasks/2
cat-file blob refs/meads/tasks/2:task.json
```

`globals.tasks()` caches the *store* in `TaskStoreCache`, but nothing caches
the *reads*, so `warnCycles` re-runs the command's own read from scratch. That
is where the 404 comes from: `2 x (1 + 100x2) + 2 = 404`. So the double read is
a clean 2x on every command, independent of the batching fix, and the two
compose: batching takes 404 -> 4, plus read caching takes it to 2.

Worth fixing in the same pass, since both are "LoadAll is called more than it
needs to be" and a shared per-process read cache is the natural place.

## 74. TASKS.md left dirty by one trailing newline after every description-file write

* status: open
* priority: P3
* type: bug
* created: 2026-07-25T23:19:06Z

After any commit whose TASKS.md was last written by `md update
--description-file` / `md add --description-file`, TASKS.md stays modified in
the working tree by exactly one trailing blank line. Committing clears it until
the next such write.

### Mechanism

1. `--description-file` keeps the source file's trailing newline, so the
   formatted TASKS.md ends `...

`. Verified:
   ```
   $ md update 1 --description-file=d.txt; tail -c 12 TASKS.md | xxd
   650a 6c69 6e65 2074 776f 0a0a          e.line two..
   ```
2. This repo's pre-commit hook runs **auto-save first**, which stages that
   `

` version verbatim.
3. auto-delete then runs `AutoClean`, whose `acquireLock` normalizes:
   `clean := strings.TrimRight(stripLockLines(content), "
") + "
"`
   (pkg/meads/lock.go:61). Even when nothing is pruned, the
   `len(result.Removed) == 0` branch calls `releaseLock(content)` with that
   already-trimmed content, rewriting the file.
4. auto-delete only re-stages when it actually removed something, so the
   normalization never reaches the index.

Net: index has `

`, working tree has `
`.

Note the block order matters — a repo that installed auto-delete first
normalizes before auto-save stages and never drifts. So this reproduces only
where auto-save's block precedes auto-delete's, which is install-order
dependent.

### Why it is worth fixing

Cosmetic on its own, but it trains "TASKS.md is always dirty, just check it
out" — and per #72 a stray `git checkout -- TASKS.md` discards real task edits.
A file the tool itself never leaves clean is a bad default.

### Options

- Stop `--description-file` preserving the trailing newline (trim it on read),
  so the two representations agree. Narrowest fix.
- Or have auto-delete stage whenever it rewrote the file, not only when it
  pruned. Also fixes any other normalization drift.
- Or normalize on write in `Format`, so no lock cycle is needed to converge.

### Acceptance

- `md add`/`md update --description-file` then commit leaves a clean tree
- A test asserts TASKS.md is byte-identical to the index after the hook runs

## 75. Help visibility misdetects git mode inside a linked worktree

* status: open
* priority: P3
* type: bug
* created: 2026-07-25T23:37:59Z

`fastGitModeLikely` (cmd/md/help_visibility.go) reports file mode in a linked
worktree of a git-mode repo, so `md --help` advertises the file-mode-only
commands (auto-save/auto-delete/beads-import) there.

### Reproduce

```
$ git init -q -b main . && md init --git && md add "task: one"
$ git worktree add /tmp/wt -b side
$ md --help | grep -c auto-save          # main checkout
0                                        # correct: hidden, git mode
$ cd /tmp/wt && md list
1. [P2] one                              # authoritative check: git mode
$ md --help | grep -c auto-save
1                                        # WRONG: shown, i.e. detected file mode
```

### Cause

`fastGitDir` correctly resolves a worktree's `.git` *file* (`gitdir: <path>`)
to the per-worktree git directory. But shared refs do not live there:

```
$ ls /repo/.git/worktrees/wt/refs
ls: cannot access ...: No such file or directory
$ cat /repo/.git/worktrees/wt/commondir
../..
```

`refs/meads/*` is an ordinary shared ref, so it lives in the **common** dir.
`fastGitModeLikely` joins `refs/meads` onto the per-worktree gitdir and finds
nothing, then reads a `packed-refs` that also is not there, and returns false.
The missing step is resolving `<gitdir>/commondir` (git-worktree(1)) before
looking for refs.

### Scope of the damage

Cosmetic, and only because the probe is confined to help visibility - its own
doc comment says so, deferring to `globals.inGitRepo` as "the authoritative,
subprocess-based check every other command uses." `md list` in the same
worktree is correct, because mode() goes through git.

That containment is the point: this is the concrete argument against promoting
the disk probe to decide `globals.mode()`. The same false negative that costs a
line of help text today would, as a mode decision, make `md add` write TASKS.md
into the working tree of a git-mode worktree.

### Fix

Resolve `commondir` in `fastGitDir` (or a sibling), relative to the gitdir when
not absolute, and look for refs there. Everything else stays as is.

### Acceptance

- `md --help` in a linked worktree of a git-mode repo hides the file-mode
  commands, matching the main checkout
- A test covers the worktree layout

## 76. Cloning a git-mode repo silently falls back to file mode

* status: open
* priority: P0
* type: bug
* depends-on: 79
* created: 2026-07-26T06:31:55Z
* updated: 2026-07-27T09:50:00Z

A teammate who clones a git-mode repo gets no task refs, so meads auto-detects
file mode and `md add` starts a second, divergent task store in TASKS.md.

Git does not fetch custom ref namespaces by default, and the
`+refs/meads/*:refs/meads-remote/*` refspec is added by `md init --git` to the
*local* repo it ran in. A fresh clone has neither the refs nor the refspec, and
`md init --git` deliberately seeds no worktree artifact - so there is no offline
evidence of git mode for detection to find.

#### Reproduced end to end

```
### source repo, git mode, one task, pushed
$ md list
1. [P2] lives in git mode
$ git --git-dir=bare.git for-each-ref refs/meads/ --format='%(refname)'
refs/meads/config
refs/meads/tasks/1

### teammate clones
$ git clone bare.git clone && cd clone
$ git for-each-ref refs/meads/ | wc -l
0
$ md list
no tasks found (run 'md add' to create one)     <- repo HAS a task
$ md add "task: teammate adds one"
added task 1                                     <- id 1 collides with the server's id 1
$ ls
TASKS.md  f.txt                                  <- file mode, in a git-mode repo
```

Trying to bootstrap by hand does not recover it either:

```
$ md init --git
initialized git mode (refs/meads/*)              <- succeeds: local namespace IS empty
added fetch refspec +refs/meads/*:refs/meads-remote/* to origin
$ md list
no tasks found                                   <- still nothing; refs never fetched
$ git push origin 'refs/meads/*:refs/meads/*'
error: failed to push some refs
hint: Updates were rejected because a pushed branch tip is behind its remote
```

The push rejection is the design working - server-side CAS refusing to discard
the real config ref in favour of this clone's unrelated fresh root commit. But
the user is left with no in-tool path forward.

`md doctor` does not help: its cross-clone duplicate case requires an id present
in BOTH local and remote-tracking refs. Here local is empty, so it sees nothing
to reconcile.

#### Impact

Git mode is effectively single-clone-only today. Worse than not working, the
failure is silent: the teammate sees an empty task list and a plausible-looking
TASKS.md rather than an error.

### Design: one-shot remote resolution, cached in a local marker ref

**The bar is `git clone` then `md list` - no bootstrap command.**

"No tasks file AND no local `refs/meads/*`" is ambiguous: it means either a
genuinely uninitialised repo, or a clone of a git-mode repo. Nothing offline can
tell them apart, so exactly one remote round-trip must decide it - and the
answer must then be cached so it never repeats.

#### The marker ref

`refs/meads-init-check` records "I have asked origin, and it has no
`refs/meads/*`". It is a ref pointing **directly at a blob**, no commit or tree
- the same shape `refs/meads/lock` already uses (`gitlock.go:49`), for the same
reason: the value has no useful history. Blob content should carry the origin
URL that was checked, so a repo whose remote is later repointed can be spotted.

It lives **outside** `refs/meads/`, and that is load-bearing, not cosmetic:

- `Detect` probes the whole `refs/meads/` namespace, so a marker inside it would
  flip the repo into git mode - the exact bug this is fixing
- the push refspec `refs/meads/*:refs/meads/*` does not match
  `refs/meads-init-check` (`-` is not `/`), so it can never be pushed
- likewise the fetch refspec `+refs/meads/*:refs/meads-remote/*` never touches it

It is per-clone local state, like `<git-common-dir>/meads/last-push`.

#### Resolution flow

Runs only when there is **no tasks file** and **no local `refs/meads/*`**:

1. `refs/meads-init-check` exists → resolved, file mode, no network. Done.
2. no `origin` remote → nothing to ask; write the marker, file mode. Done.
3. `git ls-remote origin 'refs/meads/*'`:
   - **has refs** → this is a clone of a git-mode repo. **Adopt**:
     `git fetch origin '+refs/meads/*:refs/meads/*'` (safe by construction - the
     local namespace is empty, so there is nothing to lose), then add the normal
     `+refs/meads/*:refs/meads-remote/*` fetch refspec. Repo is now in git mode;
     print a one-line stderr notice ("adopted N task refs from origin"). Do NOT
     write the marker - local refs now exist, which is itself the terminal state.
   - **no refs** → write `refs/meads-init-check`. File mode. Safe to create
     `TASKS.md` from here on.

After step 3 the repo is in a terminal state either way, so the `ls-remote`
happens **at most once per clone, ever**.

#### Where it runs

In the `OpenTasks`/`InitTasks` path (task 79) - **not** in `Detect`. `Detect`
must stay pure, offline and cheap: rais calls it on a 1-minute ticker across
every project it knows about.

Probe both namespaces in one process:
`git for-each-ref refs/meads/ refs/meads-init-check`. Steady state therefore
stays exactly one `for-each-ref`, same as today.

Marker writes are best-effort: a failure to write it must not fail the command,
only cost a repeated `ls-remote` next time.

#### Tradeoff to accept knowingly

Every ordinary git repo with an origin and no tasks file pays one `ls-remote`
and gets one local ref written, the first time meads looks at it - including
repos that will never use meads. That is the price of transparent clone
detection; there is no offline signal. It is one-shot and the ref is a single
blob, but bulk scanners (rais scans every project) will see a slower first pass
per repo.

### Acceptance

- `git clone <git-mode repo> && md list` shows the repo's real tasks, with **no
  bootstrap command**
- `md add` in a fresh clone of a git-mode repo never creates TASKS.md, and its
  new task pushes cleanly (no id collision with the origin's ids)
- a second `md list` in that clone issues no `ls-remote` (local refs now exist)
- `md add` in a fresh clone of a repo that has never used meads creates TASKS.md
  as today, writes `refs/meads-init-check`, and issues no further `ls-remote`
- a repo with no origin behaves exactly as today, with no network call
- `refs/meads-init-check` is never pushed and never fetched
- tests cover: clone → list → add → push round trip; the no-origin path; the
  marker short-circuit (assert zero `ls-remote` on the second call, e.g. via a
  counting git shim)

### Verified workaround (works today)

Fetching straight into the local namespace bootstraps a fresh clone. Safe
precisely because the local namespace is empty, so there is nothing to lose:

```
git clone <repo> && cd <repo>
git fetch origin '+refs/meads/*:refs/meads/*'
md list      # now shows the repo's real tasks
```

Confirmed 2026-07-26 against a fresh clone of github.com/jpillora/accord after
migrating it to git mode: `md list` showed "no tasks found" before, and all 97
active tasks after. This is exactly what the adopt branch above automates.

Do NOT bootstrap with `md init --git` in a clone - it succeeds, writes an
unrelated fresh config ref, and the next push is rejected non-fast-forward (see
above). Once this task lands, `init --git` in that situation must adopt instead.

### Also seen while migrating accord

Pushing the whole namespace in one go stalls. `git push origin
'refs/meads/*:refs/meads/*'` with 113 refs made no progress in over 3 minutes
and landed zero refs; a single-ref push completed instantly, and pushing in
batches of 20 completed all 113 in well under a minute.

Worth checking whether cmd/md/push.go's auto-push (which uses exactly that
wildcard refspec, with a pushTimeout) hits the same wall once a repo has many
tasks - if so it would time out every interval and never converge, silently.
Probably deserves its own task once confirmed. Note the adopt branch above
fetches with the same wildcard shape, so it may need the same batching.

### Marker-ref isolation: verified empirically 2026-07-26

All three isolation claims for `refs/meads-init-check` hold. Probed with a
blob-valued marker alongside real `refs/meads/config` + `refs/meads/tasks/1`:

| claim | result |
|---|---|
| `for-each-ref refs/meads/` does not see the marker | **0 matches** - lists only config + tasks/1 |
| push refspec `refs/meads/*:refs/meads/*` does not push it | **0 on origin** after push |
| fetch refspec `+refs/meads/*:refs/meads-remote/*` does not touch it | untouched, still `blob` |

The combined single-process probe also works as designed:
`git for-each-ref refs/meads/ refs/meads-init-check` accepts both patterns in
one call and returns all three refs, so steady state stays at one `for-each-ref`.

Git's pattern matching is what makes this safe: a wildcard-free pattern matches
only at `/` boundaries, so `refs/meads/` can never match `refs/meads-init-check`
(`-` is not `/`). The design's choice to put the marker outside the namespace
rather than inside it is sound, and does not depend on ordering or filtering.

## 79. Move the init --git body into pkg/meads.InitTasks

* status: open
* priority: P0
* type: feature
* depends-on: 
* created: 2026-07-26T12:14:55Z
* updated: 2026-07-27T09:50:00Z

Absorb `cmd/md/init.go:78` `runGit` and `:113` `ensureFetchRefspec` (moving `meadsFetchRefspec`, `init.go:61`) into `InitTasks(dir, BackendGit)`; markdown/csv get the empty-file write.

Keep both invariants:

* the fetch refspec is **additive** (`git config --add`, never a plain set) and lands in `refs/meads-remote/*`, **never** `refs/meads/*` — a force refspec onto the local namespace silently discards not-yet-pushed local commits
* `remote.origin.push` is **never** touched — setting any push refspec replaces git's default matching/simple push behaviour and breaks ordinary branch pushes for the user (`TestIntegration_InitGit_DoesNotBreakNormalPush`)

Return values instead of `fmt.Print` so a server caller (rais) has no stdout side effects; `runGit` becomes a thin wrapper that prints.

Also keep the refusal: error if `refs/meads/` already has any ref ("git mode is already initialized"), and if not inside a git repository.

## 80. Move the push body into GitTasks.Sync

* status: open
* priority: P1
* type: feature
* depends-on: 
* created: 2026-07-26T12:14:55Z
* updated: 2026-07-26T12:15:02Z

Absorb `cmd/md/push.go`'s push into `GitTasks.Sync(ctx)`:

* `git push --porcelain origin refs/meads/*:refs/meads/*` (`pushRefspec`, `push.go:24`) — explicit refspec at push time, never a configured `remote.origin.push`
* the rejection classification in `divergenceMessage` (`push.go:188`), which matches only git's own porcelain reasons (`non-fast-forward`, `fetch first`, `stale info`) and never host free-text
* **never** force-push

`cmd/md/push.go:autoPush` keeps the cadence — `GitStore.ShouldPush`/`MarkPushed`, `gitCommonDir`, `pushTimeout` — and delegates the push itself. File backends no-op, so a caller can invoke `Sync` unconditionally without a backend check.

### Why

rais pushes on every task write. Reimplementing the refspec and the porcelain rejection parsing on the rais side would be a second, drifting copy of the one piece of this that talks to a remote.

## 81. Rewire cmd/md onto meads.Tasks and delete cmd/md/taskstore.go

* status: open
* priority: P1
* type: task
* depends-on: 79,80
* created: 2026-07-26T12:14:55Z
* updated: 2026-07-26T12:15:02Z

`globals.mode()` / `gitTaskRefsExist` / `tasks()` (`main.go:183,206,244`) delegate to `Detect` and the `OpenTasks*` family, keeping the `--file` / `--git` / `explicitTasksFile` precedence layered on top. One detection implementation means `md` and library consumers (rais) can never disagree about which store a project uses.

Delete the private `taskStore` interface and both adapters from `cmd/md/taskstore.go`. Move `errGitModeUnsupported` to `import.go`, its only remaining caller (`beads-import`).

`doctor.go`'s mode switch collapses onto `Tasks.Doctor()` — both backends already have a `Doctor` returning `[]DoctorFix`. Only the git-only divergence report (`GitStore.Diverged`) still type-asserts.

Everything wired through the old seam must behave identically afterwards: `get`, `list`, `ready`, `add`/`create`, `update`, `set-status`, `add-dep`, `rm-dep`, `del`, plus `mcp`, `webui`, `prime`, `convert`. `auto-save`/`auto-delete` stay no-ops in git mode (checked before the file-existence test, so a stray leftover `TASKS.md` is never staged or pruned), and stay hidden from help via `hiddenCommands()` rather than unregistered — the installed pre-commit hook runs them unconditionally.

## 82. Rewire pkg/webui and pkg/mcp onto Tasks, deleting the type-assert seams

* status: open
* priority: P1
* type: task
* depends-on: 
* created: 2026-07-26T12:14:55Z
* updated: 2026-07-26T12:15:02Z

`pkg/mcp.NewServer(store meads.Tasks, ...)`; `pkg/webui.Server.Store meads.Tasks`.

Deletions the interface makes possible:

* `storeLocation` + `fileLocator` (`webui/server.go:240-260`) become `store.Location()` + `store.Backend()`
* `startWatcher` (`webui/watch.go:70`) switches on `Backend()` instead of asserting: git polls `Revision()`, md/csv runs fsnotify on `Location()`. `refSnapshotter` is deleted
* the three hand-rolled test doubles go: `gitTaskStoreForTest` (`pkg/mcp/gitstore_test.go:23`), `gitTaskStoreStub` (`pkg/webui/watch_test.go:32`), and `noopStore` (`:269`) — whose "implements neither seam" case ceases to exist

Move the three `meads.NewStore(memfs.New(), "TASKS.md")` call sites (`pkg/mcp/server_test.go:18`, `pkg/webui/handlers_test.go:20`, `pkg/webui/bind_test.go:20`) to `OpenTasksFS`.

Keep the watcher's existing coverage: the git path must still fire after `git pack-refs` removes the loose ref file — that is exactly why git mode polls instead of using fsnotify.

## 83. Webhook file field points at a phantom TASKS.md in git mode

* status: open
* priority: P2
* type: bug
* created: 2026-07-26T12:14:55Z

`postWebhook` (`cmd/md/webhook.go:24`) has no `mode()` check, so in git mode it still emits `File: <cwd>/TASKS.md` — a path to a file that does not exist.

Consumers scope events by that path's **directory** (e.g. rais's `tasksFileGovernsDir`: accept only if `filepath.Dir(file)` is the terminal's working dir or an ancestor). `md` does not walk up to find the tasks file, so at the project root the phantom path resolves to the right directory and everything works — identical to file mode.

It misjudges `md` run from a **subdirectory**. In file mode that genuinely is a different (nonexistent) store, so rejecting is correct. In git mode it is the *same* repo-wide store, so the consumer silently drops a legitimate event — in rais's case, the agent task badge never appears.

### Fix

In git mode only, resolve `tasksFileAbs()` against `git rev-parse --show-toplevel` instead of `globals.Dir`. No new payload field, no consumer change, file mode untouched.

## 84. Git-mode tasks carry no meta.created / meta.updated

* status: open
* priority: P3
* type: bug
* created: 2026-07-26T12:14:55Z

`GitStore.Create`/`Update` (`pkg/meads/gitmutate.go`) set no `Meta` at all — grep confirms `"created"` appears nowhere in `gitmutate.go`/`gitstore.go`. File mode's `Store.Add` sets `t.Meta["created"]` (`pkg/meads/mutate.go:38-40`) and bumps `f.Meta["updated"]`.

Since `created`/`updated` are not in `knownMetaKeys` (`task.go:42`), file-mode JSON carries `"meta":{"created":"..."}` while git-mode JSON omits `meta` entirely. Task 57's own design doc specifies both fields in `task.json`, so this is an undocumented divergence from the design, not a deliberate omission.

Not required by rais — its frontend never reads `task.meta` — but it is a real `md list --json` / `md get --json` parity gap between backends.

## 85. Release meads v0.36.0

* status: open
* priority: P1
* type: task
* depends-on: 81,82,83,76
* created: 2026-07-26T12:14:55Z
* updated: 2026-07-26T14:38:42Z

`go fmt ./...`, `go test ./...`, tag `v0.36.0` and push.

Call out in the release notes that this is a **breaking** v0 change:

* `meads.TaskStore` is gone, replaced by `meads.Tasks`
* `pkg/mcp.NewServer` and `pkg/webui.Server.Store` now take `meads.Tasks`
* `meads.NewStore` / `meads.NewFileStore` are no longer the entry point — use `meads.OpenTasks(dir)`, `meads.OpenTasksFile(file)` or `meads.OpenTasksFS(fs, file)`

rais's integration work is blocked on this tag.
