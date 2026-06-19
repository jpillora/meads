# TASKS

a [meads](https://github.com/jpillora/meads) (`md`) managed task log

* created: 2026-02-14T11:42:09Z
* updated: 2026-06-19T13:27:32Z
* max-id: 54
* next-id: 13

## 20. VS Code extension end-to-end manual test

* status: inprogress
* priority: P2
* type: feature
* depends-on: 
* created: 2026-05-21T07:29:29Z
* updated: 2026-05-21T16:36:32Z

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

## 30. Git mode

* status: draft
* priority: P2
* type: idea
* created: 2026-06-06T01:24:43Z
* updated: 2026-06-06T05:34:55Z

### Git mode for meads (`md` task 30)

#### Context
Today every task lives in a working‑tree `TASKS.md`/`.csv`, mutated under an
append‑"lock line" optimistic lock. Task 30 ("Git mode") wants task state to
live **purely in git** — as JSON in a dedicated ref, *out of the working tree* —
so there is no tracked file to diff/stage, history is the ref's commit log, and
`md init` seeds an empty tasks JSON as the first ("virtual") commit. Goal: same
`md` UX and speed, but the backing store is a git ref instead of a file.

#### Why these choices (measured this session, not assumed)
- **Remote ref write ≈ 2.5 s** (`ls-remote` to `origin` over SSH; dominated by
  handshake/RTT, payload‑independent) vs **local git op ≈ 3 ms**. ⇒ tasks must be
  written to a **local** ref; the remote is sync‑only, never on the command hot path.
- **go-git in‑process = 0.70 ms/write** for the full `blob→tree→commit→CAS ref`
  path vs **git‑CLI 4‑spawn = 31 ms/write** → **44× faster** (real on‑disk repo,
  arm64). ⇒ use **go-git** for all local read/write/history; reserve the network
  for the user's own `git push`/`fetch`.

#### Storage model
- Ref `refs/meads/tasks` → commit → tree → single blob `tasks.json`
  (`{"meta":{…},"tasks":[…]}`).
- Each mutation = a new commit parented on the prior; the ref is advanced via
  **CAS** (`CheckAndSetReference(new, old)`) — native optimistic locking that
  replaces the lock‑line scheme.
- History/recovery = walk the ref's commit parents, reading `tasks.json` at each.

#### Design — centralize behind ONE backend (per "centralise the heavy lifting")
All mutation/format/query logic stays shared; only *read*, *locked‑write*, and
*history* differ per backend.

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
- **`fileBackend`** — wraps today's code unchanged: `util.ReadFile`+`stripLockLines`,
  `acquireLock`/`releaseLock` (`lock.go`), CLI‑`Git` history (`git log --all -- file`,
  `git show hash:file`).
- **`gitBackend`** — go-git `storage/filesystem` storer over `osfs` (already vendored
  via go-billy) + `plumbing`/`object`/`filemode`: `read` = blob at the ref; `transact`
  = read ref commit → apply → write blob/tree/commit → `CheckAndSetReference` with a
  short retry loop on contention; `history` = walk parents.

`Store` keeps its `Format` and gains a `be backend`. Refactor `mutate.go`
(`Add/AddMany/Delete/DeleteMany/Update/Doctor/AutoClean`) from
`acquireLock()/releaseLock()` to `be.transact(…)`, and `query.go`
(`Get/Ready/GetHistory/GetWithHistory/committedIDs`) to read via
`be.read()/history()/headContent()`. One transaction shape; both backends reuse it.

#### New JSON format
`pkg/meads/json.go`: `jsonFormat` implementing `Format` — `Parse`=`json.Unmarshal`→`File`,
`Format`=indented `json.Marshal`, `HasPreamble()=true`, `EmptyFile()=` `{"tasks":[]}`.
Reuses existing `File`/`Task` JSON tags + `Task.MarshalJSON`. Self‑consistent round‑trip:
all `md` logic reads struct fields (the `t.Meta["status"]` etc. are only *written* by
setters), and project `File.Meta` (`created`/`updated`/`max-id`) round‑trips as‑is.

#### Wiring / detection (auto‑detect + override)
- `globals.store()`: `--file` (or no git repo) → fileBackend; `--git`/`MEADS_GIT=1` →
  gitBackend; else **auto** = gitBackend iff `refs/meads/tasks` exists (one cheap ref
  lookup). Git mode pins `format=json`, in‑tree name `tasks.json`.
- Add `--git`/`--file` globals (+ `MEADS_GIT`) in `cmd/md/main.go`.

#### `md init --git`
- Open repo (go-git); error if `refs/meads/tasks` already exists.
- Seed empty `{"tasks":[]}` blob→tree→commit (`meads: init`)→`SetReference` — the
  "first virtual commit."
- Configure a **fetch** refspec on `origin`: `+refs/meads/*:refs/meads/*` (additive, safe).
- Publish on push **without hijacking `push.default`**: install a **pre‑push hook**
  (reuse the hook plumbing in `cmd/md/auto_delete.go`) that runs
  `git push origin refs/meads/tasks`. ⚠️ Deliberately do NOT set `remote.origin.push`
  — configuring any push refspec replaces git's matching/simple default and would
  break normal branch pushes. Also print the manual push command.

#### Scope — MVP behind the backend (reading the unselected scope + "centralise" as MVP‑first)
**In:** backend abstraction, `gitBackend` (go-git), `jsonFormat`, detection,
`md init --git`, full CRUD, history/recovery, webhook (unchanged — it lives at the
command layer).

**Deferred as new `md` tasks** (file‑assuming integrations + the genuinely hard part):
- **Multi‑clone sync/merge** — a single shared mutable ref *diverges* across clones,
  so fetch must MERGE (union by ID + `doctor`‑style dedup/tombstones), not force‑overwrite.
  This is the real distributed‑hard problem, analogous to today's `md doctor` for file
  merges. MVP keeps fetch non‑force so divergence fails loudly rather than losing tasks.
- **webui watch** (`pkg/webui/watch.go`) — fsnotify watches a path; git mode must
  watch/poll the ref (`.git/refs/meads/tasks` / `packed-refs`).
- **auto‑delete hook** (`cmd/md/auto_delete.go`) — stages a working‑tree file on
  pre‑commit; in git mode there is no staged file (each change is already a ref commit)
  → rework or make it a no‑op.
- **convert/migrate** `TASKS.md ↔ git`; note md↔json conversion must sync struct
  fields→`Meta` so the markdown formatter still emits per‑task `* status:` lines.

#### Files
- **New:** `pkg/meads/backend.go`, `pkg/meads/git_backend.go`, `pkg/meads/json.go`.
- **Refactor:** `store.go` (hold `backend`), `mutate.go` + `query.go` + `lock.go`
  (move file logic into `fileBackend`), `git.go` (keep `Git` for any CLI remote bits).
- **CLI:** `cmd/md/main.go` (globals + detection in `store()`), `cmd/md/init.go` (`--git`).
- **Deps:** add `github.com/go-git/go-git/v5`.

#### Verification
- Build: `go install ./cmd/md` (per project rule, from `cmd/md/`).
- Tests: parametrize the existing `pkg/meads` + `e2e` suites over both backends — `e2e`
  already uses `memfs`; add a git‑backed `Store` fixture in a temp repo. Assert
  CRUD/ready/deps/doctor/tombstone parity between file and git modes.
- Manual smoke (temp repo): `git init` → `md init --git` → confirm `git status` is
  clean and `git for-each-ref refs/meads` shows the ref → `md add/update/del/get/ready`
  → `git log refs/meads/tasks` shows one commit per change → `md get <deleted-id>`
  recovers from history.
- Concurrency: run two `md add` in parallel; confirm CAS retry yields both (no lost update).
- Speed: confirm git‑mode `md add` stays single‑digit‑ms (the 0.70 ms write path).

## 45. web UI: inline quick-edit of status, priority and type via chips

* status: open
* priority: P3
* type: idea
* created: 2026-06-19T11:43:30Z

Changing status, priority or type currently requires opening the full editor dialog. Make the chips on each card interactive: click a chip to pick a new value from a small popover and PATCH immediately, for faster triage. Builds on the existing chip rendering in pkg/webui/assets/app.js.

## 46. web UI: dependency graph / tree visualization

* status: open
* priority: P3
* type: idea
* created: 2026-06-19T11:43:30Z

Dependencies are the core differentiator but are only shown as flat per-card links. Add an optional graph or tree view of the task DAG (parents and children, highlighting blocked chains and ready leaves). Reuse the existing /api/tasks data and render with a lightweight no-build approach to match the current vanilla JS stack.

## 49. web UI: single markdown body field with derived title in the editor

* status: open
* priority: P3
* type: idea
* created: 2026-06-19T11:54:03Z

The editor dialog (pkg/webui/assets/index.html + app.js) has separate Title and Description inputs. ../rais MeadsTaskDetail instead edits one markdown body and derives the title from its first line, matching md add rich parsing (title is the text before the first period-space or newline). Consider collapsing to a single body editor: less chrome, fewer fields, and consistent with the CLI. Keep an explicit fallback (Untitled) when the first line is empty.

## 50. web UI: persist the unsaved New task form as a draft

* status: open
* priority: P3
* type: feature
* created: 2026-06-19T11:54:03Z

openEditor() calls form.reset(), so a half-written new task is lost if the dialog is closed or the page reloads. ../rais MeadsTaskDetail autosaves the in-progress add form to localStorage (body/type/priority) and restores it on mount, clearing it on successful create. Add the same draft persistence to the meads new-task form under a single localStorage key so accidental closes do not lose work; clear it on submit and on explicit cancel.

## 53. Document auto-save pre-commit hook behavior in CLAUDE.md

* status: open
* priority: P3
* type: task
* created: 2026-06-19T12:00:21Z

Add a note to CLAUDE.md explaining that a pre-commit hook auto-stages TASKS.md into every commit, and agents should NOT unstage or remove it. Reduces agents fighting the hook.
