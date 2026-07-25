# TASKS

a [meads](https://github.com/jpillora/meads) (`md`) managed task log

* created: 2026-02-14T11:42:09Z
* updated: 2026-07-25T10:04:07Z
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
* updated: 2026-07-25T08:08:50Z

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
  ref, each with its expected old-oid. This is now the *only* place the atomic
  batch primitive is needed on the write path.

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
- **Push asynchronously.** A remote ref op measured ≈2.5 s; blocking every
  `md update` on that would be unusable. Fire-and-forget, log failures.
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
6. Auto-push (async, `pushInterval`).
7. Two-stage lock, `remoteLocking` opt-in.
8. Reconcile/merge for divergence + `doctor` for git mode.
9. Integrations: webui watch (poll refs, not fsnotify on a file); `md auto-save`
   and `md auto-delete` become **no-ops in git mode** — there is no working-tree
   file to stage, and nothing to prune since refs are never removed.

## 62. Git mode phase 5: init --git, fetch refspec, mode detection

* status: open
* priority: P2
* type: task
* depends-on: 
* created: 2026-07-25T08:19:07Z
* updated: 2026-07-25T08:19:16Z

`md init --git`, remote refspec wiring, and automatic mode detection. Design of record: task 57.

### Build

- **`md init --git`** — initialise git mode in the current repo; error clearly if `refs/meads/*` already exists
- **Fetch refspec** — set `+refs/meads/*:refs/meads/*` on origin. This is additive and safe. Without it a default clone does NOT fetch these refs (verified on GitHub and Gitea), so tasks would silently not sync
- **Detection** — git mode iff `refs/meads/tasks/*` exists (one cheap ref lookup). Honour explicit `--git` / `--file` flags and an env override

### CRITICAL trap

**Do NOT set `remote.origin.push`.** Configuring any push refspec replaces git's matching/simple default and breaks ordinary branch pushes for the user. Use an explicit refspec at push time, or a pre-push hook — reuse the hook plumbing in cmd/md/auto_delete.go.

### Acceptance

- init on a fresh repo produces a working git-mode store
- second init errors instead of clobbering
- detection selects git mode without an explicit flag
- **regression test: a normal `git push` of a branch still behaves identically after init** (guards the refspec trap above)

## 63. Git mode phase 6: async auto-push on pushInterval

* status: open
* priority: P2
* type: task
* depends-on: 62
* created: 2026-07-25T08:19:07Z
* updated: 2026-07-25T08:19:16Z

Push `refs/meads/*` to origin on an interval, without ever blocking a command. Design of record: task 57.

### Build

- After each mutation, compare now against the last-push timestamp; if elapsed > `pushInterval` (phase 4 config), trigger a push
- **Last-push timestamp is LOCAL state** at `$GIT_COMMON_DIR/meads/last-push` — never a ref, never pushed. Push cadence is per-clone; a shared ref would make every clone fight over it
- Push refspec `refs/meads/*:refs/meads/*` explicitly (see phase 5 — do not configure `remote.origin.push`)

### CRITICAL

**The push must be asynchronous.** A remote ref operation measured ~2.5 s over SSH, against ~3 ms for a local one. Blocking every `md update` on that makes the tool unusable. Fire-and-forget, log failures, and never fail the user's command because a push failed.

### Divergence

A non-fast-forward rejection means another clone has diverged. Surface it clearly and legibly; resolving it is phase 8. Do not force-push.

### Acceptance

- Mutation commands return promptly regardless of network state
- Push fires only after `pushInterval` has elapsed
- Push failure (offline, auth, rejection) does not fail the mutation
- Non-fast-forward rejection produces an actionable message

## 64. Git mode phase 7: two-stage lock + opt-in remoteLocking

* status: open
* priority: P2
* type: task
* depends-on: 
* created: 2026-07-25T08:19:07Z
* updated: 2026-07-25T08:19:16Z

Opt-in global lock for rare multi-step operations. Design of record: task 57.

### Why this is small

Per-ref CAS already prevents lost updates, so this lock exists only for operations that cannot be expressed as one batch (format migration, long maintenance). It is **off by default**, which is also what preserves offline operation.

### Build — two stages

1. **Local** — `flock` on `$GIT_COMMON_DIR/meads.lock`
   - flock specifically, because the kernel releases it on process death (clean exit, crash, `kill -9`, OOM), so it cannot leak. A lock stored as data can.
   - The common git dir means it is automatically shared by every linked worktree.
   - Needs build-tagged implementations: unix `flock`, Windows `LockFileEx`.
   - Guard self-deadlock with an in-process mutex + refcount — two `flock` calls on separate fds in one process block each other.
2. **Read config** (phase 4)
3. **Remote** — only if `remoteLocking` is true
   - Acquire = CAS `refs/meads/lock` from the 40-zero oid (create-if-absent)
   - Release = CAS delete against your own oid, which makes releasing someone else's lock impossible
   - Lease `{holder, expires}`; an expired lock is stolen via CAS, so exactly one thief wins

### Ordering and failure

- Acquire local, then remote. Release remote **after** the data write — the remote lock brackets the whole critical section.
- If remote acquisition fails, release the local lock before returning, or it leaks.
- **Fail closed**: `remoteLocking` on + network unavailable = error. Silently proceeding is the one outcome that loses mutual exclusion invisibly.
- Detect "lock is held" by re-reading the ref, never by matching rejection text.

### Acceptance

- Local lock is released after the holder is SIGKILLed
- A second local holder blocks while the first holds
- N racers against one expired remote lock: exactly one steals it
- Remote acquisition failure releases the local lock

## 65. Git mode phase 8: divergence reconcile + doctor for git mode

* status: open
* priority: P3
* type: task
* depends-on: 63
* created: 2026-07-25T08:19:07Z
* updated: 2026-07-25T08:19:16Z

The hardest phase, deliberately deferred from MVP. Design of record: task 57.

### Problem 1 — offline divergence

Two clones both mutate task 42 while disconnected. Each builds a commit chain from a common ancestor. The second push is correctly rejected as non-fast-forward.

Resolving this means genuinely merging two versions of one task. Options to evaluate: field-level merge, last-writer-wins by commit timestamp, or interactive resolution.

**MVP requirement: fail loudly rather than lose data.** A wrong automatic merge is worse than a clear error.

### Problem 2 — duplicate ids across clones

Two disconnected clones both create task 58. Create-if-absent cannot coordinate across a network partition — and note a shared counter ref would not have helped either, so this is not a consequence of dropping it.

Port `md doctor` renumbering to git mode:
- One atomic batch (phase 1) over every affected ref, each carrying its expected old-oid
- Renumbering MUST also rewrite `dependsOn` references that point at moved ids
- Soft-deleted refs participate: their ids are still allocated and must not be reused

### Acceptance

- Simulate divergence with two clones; MVP surfaces it without data loss
- `doctor` renumbers duplicates and fixes all `dependsOn` edges
- `doctor` is atomic: an aborted run leaves no partial renumbering

## 66. Git mode phase 9: integrations (webui watch, hooks, migration)

* status: open
* priority: P3
* type: task
* depends-on: 62
* created: 2026-07-25T08:19:07Z
* updated: 2026-07-25T08:19:16Z

Make the rest of the toolchain git-mode aware. Design of record: task 57.

### Build

- **webui watch** (pkg/webui) — currently fsnotify on a single file. Git mode must observe refs instead. Note that refs move between loose files and `packed-refs`, so file watching alone is unreliable; polling the ref oids is simpler and more robust. Feeds the existing SSE `/api/events` stream.
- **`md auto-save`** (cmd/md/auto_save.go) — **no-op in git mode.** It stages `TASKS.md` into commits; there is no working-tree file to stage.
- **`md auto-delete`** (cmd/md/auto_delete.go) — **no-op in git mode.** It prunes closed tasks from the file to keep it small; there is no file to shrink, and refs are never removed.
- **Migration** — `md convert` between `TASKS.md`/`.csv` and git mode, both directions.
- **webhook** — lives at the command layer and should be unaffected; verify rather than assume.

### Acceptance

- webui live-updates when a task ref changes, including when refs are packed
- both hooks detect git mode and no-op cleanly, without erroring or printing noise
- migration round-trips a fixture file -> git mode -> file with no data loss
- webhook still fires with correct payloads in git mode

## 67. auto-delete hook git add fails with exit 128, aborting commit

* status: open
* priority: P1
* type: bug
* created: 2026-07-25T08:55:24Z

The `md auto-delete` pre-commit hook can fail with `staging TASKS.md: exit status 128`, which aborts the user's `git commit`.

### Symptom

Observed 2026-07-25 while committing phase 1 of git mode. Hook output was:

```
md: auto-staged TASKS.md          <- auto-save's git add succeeded
md: removed closed task 58        <- auto-delete rewrote TASKS.md
staging TASKS.md: exit status 128 <- auto-delete's git add FAILED
```

The commit was aborted. **Not data-destructive**: `runFromHook` in
cmd/md/auto_delete.go correctly restored its backup on the failed `git add`
(the closed task reappeared in TASKS.md), and all staged changes survived.
Retrying the commit succeeded once TASKS.md had already been cleaned.

### What has been ruled out

- **Not `.git/index.lock` contention.** Verified empirically in a scratch repo:
  a pre-commit hook that probes for `.git/index.lock` finds it *absent*, and two
  consecutive `git add` calls from within the hook both succeed — under both
  `git commit -m` and `git commit -F -` (message on stdin).
- **Not a sequencer state.** No rebase/merge/cherry-pick was in progress;
  `sequencerInProgress()` would have skipped the hook entirely.
- **Not a standalone failure.** Running `GITHOOK=1 md auto-delete` directly, with
  a closed task present, succeeds: it removes the task, stages the file, exits 0.

So the failure appears specific to running inside `git commit`, and only on
auto-delete's `git add` — auto-save's identical `git add` in the same hook run
had already succeeded moments earlier. That asymmetry is the key clue: the
difference between the two call sites is that auto-delete first runs
`store.AutoClean(git)`, which itself shells out to git (`git log --all -- <file>`
and `git show <hash>:<file>` per commit, via committedIDs in pkg/meads/query.go).

### Why it is hard to diagnose

`ExecGit.Run` (pkg/meads/git.go) uses `cmd.Run()`, which discards stderr, so the
failure surfaces only as a bare `exit status 128`. Git's actual fatal message —
which would almost certainly name the cause — is thrown away.

### Suggested approach

1. **First, make it diagnosable.** Give `ExecGit.Run` the same stderr capture
   that `outputRaw` already has, folding git's message into the returned error.
   This is worth doing regardless of this bug. Note `ExecGit` now has an
   `outputRaw` helper (added in phase 1) that does exactly this and can be reused.
2. Reproduce: close a task, then commit any change, and capture the real stderr.
3. Consider whether auto-save and auto-delete should be a single hook invocation
   rather than two separate `md` processes both staging the same file. The double
   staging is redundant work and doubles the exposure to whatever this is.

### Acceptance

- The real git error is surfaced in the message, not just an exit code
- A reproduction exists as a test (or the root cause is documented as
  environmental and the hook is made resilient to it)
- Closing a task and committing does not abort the commit

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
