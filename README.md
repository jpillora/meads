# meads

Task tracking that lives entirely in git. No database, no server, no dependencies — a Markdown/CSV file, or (in git mode) nothing but git refs. Inspired by [beads](https://github.com/steveyegge/beads), but **much** simpler.

[![GoDev](https://img.shields.io/static/v1?label=godoc&message=reference&color=00add8)](https://pkg.go.dev/github.com/jpillora/meads/pkg/meads)
[![CI](https://github.com/jpillora/meads/workflows/CI/badge.svg)](https://github.com/jpillora/meads/actions?workflow=CI)

### Features

- All state lives in git — full audit trail, easy to revert, works with branches
- No server or database to maintain — a single Markdown/CSV file, or (in git mode) nothing but git refs
- Works offline, works in any terminal, works with diffs
- AI-friendly — agents can read and write tasks through the CLI or MCP server
- **Markdown** format (`TASKS.md`) for human-readable diffs
- **CSV** format (`TASKS.csv`) for spreadsheets and tooling, and clean git merges
- **Git mode** — skip the tasks file entirely: tasks live as git refs (`refs/meads/tasks/<id>`), each with its own version history (see [Git mode](#git-mode))
- Simple field extraction from input. Title is first sentense. Description is the rest. Type prefixes (`bug:`, `task:`, `feature:`, `idea:`), Priority (`P0`-`P5`).
- Task dependencies with automatic blocking detection
- Concurrent-write safe via optimistic locking, with retry on contention — multiple processes or AI agents can write simultaneously
- `md prime` prints LLM context, `md mcp` runs an MCP server over stdio

### Install

**Binaries**

[![Releases](https://img.shields.io/github/release/jpillora/meads.svg)](https://github.com/jpillora/meads/releases)
[![Releases](https://img.shields.io/github/downloads/jpillora/meads/total.svg)](https://github.com/jpillora/meads/releases)

Find [the latest pre-compiled binaries here](https://github.com/jpillora/meads/releases/latest) or download and install it now with

```
curl "https://i.jpillora.com/meads!?as=md" | bash
```

**Source**

```sh
go install github.com/jpillora/meads/cmd/md@latest
```

### Quick Start

```bash
md add "Fix the login bug. 500 error when session cookie is expired"
# added task 1

md add "Write tests for login fix"
# added task 2

md ready
# 1. Fix the login bug
# 2. Write tests for login fix

md set-status 1 closed
# task 1 status set to closed
```

The resulting `TASKS.md`:

```markdown
# TASKS

a [meads](https://github.com/jpillora/meads) (`md`) managed task log

* created: 2025-01-15T09:00:00Z

## 1. Fix the login bug

* status: closed
* created: 2025-01-15T09:00:00Z

500 error when session cookie is expired

## 2. Write tests for login fix

* status: open
* created: 2025-01-15T09:00:01Z
```

### Usage

```
md add "title"                        Add a simple task
md add "bug: Fix login P1. Details"   Rich input (type, priority, body)
md add --depends-on=1 "Write tests"   Add with dependency
md add --description-file=-           Read a description from stdin/HEREDOC
md create "title"                     Alias for add
md get <id>                           View a task (recovers deleted tasks from git history)
md list                               List all tasks
md list --tag=api                     Filter by tag (also on ready; --tag=a,b requires both)
md ready                              Show unblocked open tasks (by priority)
md update <id> --priority=P1          Update task fields
md update <id> --add-tags=api         Add tags (--rm-tags removes, --tags replaces)
md update <id> --description-file=-   Read a description from stdin/HEREDOC
md set-status <id> <status>           Change status (draft|open|inprogress|closed)
md del <id>                           Delete a task (soft — restorable, id stays spent)
md del <id> --force                   Erase it instead — unrecoverable, frees the id for reuse
md restore <id> / --all               Undo a soft delete
md add-dep <child> <parent>           Add a dependency
md rm-dep <child> <parent>            Remove a dependency
md init                               Initialize — git mode in a repo, TASKS.md outside one
md init --md / --csv                  Force a tasks file instead of git mode
md convert TASKS.md                   Convert between Markdown and CSV formats
md convert TASKS.md --to-git          Migrate a tasks file into git mode
md doctor                             Detect and fix duplicate task IDs (in git mode, also repairs incomplete setup)
md prime                              Print LLM context for using md
md mcp                                Start MCP server over stdio
md webui                              Launch web UI for the current task store
```

### Web UI

`md webui` hosts a localhost HTTP server with a web UI for viewing and
editing one `TASKS.md` (or `.csv`) file. It prints a single JSON line to
stdout on startup with the URL and access token:

```bash
md webui
# {"url":"http://127.0.0.1:54231","token":"…","file":"TASKS.md","format":"md"}
```

Pass `--open` to launch the browser automatically, or `--port 3000` for a
fixed port. All routes require the bearer token; change events are
streamed over Server-Sent Events at `/api/events`.

The filter box takes `type:bug`, `status:open`, `priority:P1`, `tag:api`,
`is:ready`, `#3`, and bare words as free text. Different facets AND together;
repeating one ORs (`status:open status:draft` shows both) — except tags, which
AND, so `tag:api,web-ui` wants both, as `md list --tag=a,b` does. Clicking a
tag chip on a card adds or removes that one token.

Pass `--no-token` to serve without auth — no token is generated and every
request is allowed, so the printed URL needs no `?token=`. Only the
loopback-origin check still guards the server, so keep this to trusted
setups (a local reverse proxy that adds its own auth, a kiosk, a test
harness) and never combine it with `--host 0.0.0.0`. `--no-token` and
`--token` are mutually exclusive.

### Git mode

Git mode skips the tasks file entirely: every task lives at its own ref
(`refs/meads/tasks/<id>`, a small commit chain giving that one task its own
version history), and repo-wide settings live at `refs/meads/config`. There
is no `TASKS.md`/`TASKS.csv` in the working tree at all. Every linked
worktree of the same repo automatically shares the same task list — they
already share one `.git`, and therefore one set of refs.

**Enable it:**

```bash
md init          # git mode, since this is a repo
md init --md     # a TASKS.md instead
```

Git mode is what `md init` creates inside a git repository — it is the better
backend wherever it can run, and a repo is its only requirement. Outside one,
`md init` falls back to a `TASKS.md`. `--md`/`--csv` force a file either way,
and an existing tasks file is left alone rather than shadowed.

Detection after that is automatic: any `md` command finds git mode active
whenever `refs/meads/*` is non-empty, no flag required. Override either way
with `--git`/`--file`, or set `MEADS_GIT=1` to default a whole shell to git
mode.

**Migrate an existing tasks file:**

```bash
md convert TASKS.md --to-git      # file → git mode; refuses if git mode already has tasks
md convert TASKS.md --from-git    # git mode → file; refuses if the file already exists
```

Both directions preserve task ids exactly, including soft-deleted
(tombstone) tasks — `--to-git` also recovers any id already pruned from the
working file by `md auto-delete`, straight from git history, so nothing
gets silently reassigned. `--to-git` also uninstalls the `md auto-save` and
`md auto-delete` hooks, which have no job left once there is no tasks file to
stage or prune. (`--from-git` does not reinstall them: installing a hook
changes how every future commit behaves, so it stays opt-in.)

Run either `md init` or `md convert --to-git`, not both — `--to-git` needs no
prior init, and adds the fetch refspec itself, so a migrated repo can share
task refs like any other. (`md init` after a migration still refuses, since
`refs/meads/` is no longer empty; there is nothing left for it to do.) A repo
migrated by an older version, which skipped the refspec, is
repaired by `md doctor`.

**Sharing across clones:** `md init --git` adds a fetch refspec so `git
fetch`/`git clone` also download `refs/meads/*` — into a separate
`refs/meads-remote/*` namespace, never overwriting your own unsynced work.
meads also pushes `refs/meads/*` to `origin` automatically whenever a
remote is configured, so you don't need to `git push` for task changes to
reach it. The push runs at most once per `pushInterval` (default `1m`), so
roughly one command per interval waits for it; it is bounded by a timeout
and never fails your command if the remote is unreachable.

Run `md doctor` after fetching: it renumbers a duplicate id left behind when
two clones each created a task offline at the same id, and reports (but does
not yet auto-resolve) a genuine divergence — the same task edited
differently by two clones since they last shared a common ancestor. meads
never force-pushes or guesses at a merge; resolving a real divergence today
is manual.

**Caveats, honestly:**

- `md beads-import` only knows how to import into a tasks file — not
  supported in git mode.
- `md auto-save`/`md auto-delete` (pre-commit hooks that stage the tasks
  file into every commit, and prune closed tasks out of it) no-op in git
  mode: there is no working-tree file to stage or prune.
- Cross-clone divergence resolution is manual, as described above.

### Examples

**Add a task with type, priority, and body:**

```bash
md add "bug: Fix login P1. Session cookie expires prematurely"
```

**View ready work (sorted by priority):**

```bash
md ready
```

**Tag work, then filter by tag:**

```bash
md add "Rate-limit the login endpoint" --tags=api,security
md update 1 --add-tags=urgent   # --rm-tags removes, --tags= clears
md ready --tag=api,security     # ready work carrying BOTH tags
```

**Create dependent tasks:**

```bash
md add "feature: Build API endpoint"           # returns ID 3
md add "Write tests for API" --depends-on=3    # blocked until 3 is closed
```

**Track progress:**

```bash
md set-status 3 inprogress   # claim it
md set-status 3 closed       # done
```

**Write a rich Markdown description without shell escapes:**

```bash
md update 3 --description-file=- <<'EOF'
## Notes

- Real newlines and `code spans` are preserved literally.
- The quoted delimiter prevents shell expansion of `$variables`.
EOF
```

`--description-file` (a path or `-`) is taken literally — no escape decoding —
with trailing blank lines trimmed. Inline `--description` still decodes
JSON-style escapes, so `\n` there means a newline.

## Merging TASKS.md

When multiple branches create tasks independently (common with AI agents working in parallel), they each assign the next available ID locally. After merging, this can result in duplicate task IDs in the same file.

To fix this, run:

```bash
md doctor
```

`md doctor` scans for duplicate IDs and renumbers them. The first occurrence keeps its original ID; subsequent duplicates get new IDs. Any `depends-on` references within renumbered tasks are also updated to point to the correct new IDs.

### Notes

- **Format** — Three storage backends, auto-detected (by file extension, or by the presence of git-mode refs — see [Git mode](#git-mode)):
  - `TASKS.md` — Markdown headings (`## 1. Title`) with `* key: value` metadata. Human-readable diffs.
  - `TASKS.csv` — Standard CSV with soft-delete and computed next-id. Easy to import into spreadsheets and other tools.
  - **Git mode** — no file at all; each task is its own git ref (`refs/meads/tasks/<id>`).
- **Metadata** — Built-in keys are `status`, `priority`, `type`, `depends-on`, `tags`, `close-reason`, `created`, and `updated`.
- **Tags** — A comma-separated set under the `tags` key (`* tags: api,web-ui`, a CSV column, or a JSON array in git mode). Each tag is lowercase letters, numbers and dashes; values are lowercased and de-duplicated on input, and rejected if they contain anything else.
- **Concurrency** — Concurrent writes are safe *and* they land: file mode takes an optimistic file lock and releases it with an atomic replace, git mode compare-and-swaps each task's own ref. A writer that loses the race retries with jittered backoff (up to ~1.6s of budget, typically ~0.8s) rather than failing, so multiple processes — or AI agents — can write simultaneously without corruption and without dropped writes. Contention past that budget is reported as an error, never silently discarded.
- **AI-friendly** — Every backend is designed to be readable and writable by LLMs. Use `md prime` to print context for an AI agent (it describes whichever mode is actually active), or `md mcp` to run an MCP server over stdio.
- **Minimal** — Single static binary, no config files.
