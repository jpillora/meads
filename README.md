# meads

Task tracking in a single file. No database, no server, no dependencies — just `TASKS.md` and git. Inspired by [beads](https://github.com/steveyegge/beads), but **much** simpler.

[![GoDev](https://img.shields.io/static/v1?label=godoc&message=reference&color=00add8)](https://pkg.go.dev/github.com/jpillora/meads/pkg/meads)
[![CI](https://github.com/jpillora/meads/workflows/CI/badge.svg)](https://github.com/jpillora/meads/actions?workflow=CI)

### Features

- All state lives in git — full audit trail, easy to revert, works with branches
- No server or database to maintain — just a single Markdown (or CSV) file
- Works offline, works in any terminal, works with diffs
- AI-friendly — agents can read and write tasks through the CLI or MCP server
- **Markdown** format (`TASKS.md`) for human-readable diffs
- **CSV** format (`TASKS.csv`) for spreadsheets and tooling, and clean git merges
- Simple field extraction from input. Title is first sentense. Description is the rest. Type prefixes (`bug:`, `task:`, `feature:`, `idea:`), Priority (`P0`-`P5`).
- Task dependencies with automatic blocking detection
- Concurrent-write safe via optimistic locking — multiple processes or AI agents can write simultaneously
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
md create "title"                     Alias for add
md get <id>                           View a task
md list                               List all tasks
md ready                              Show unblocked open tasks (by priority)
md update <id> --priority=P1          Update task fields
md set-status <id> <status>           Change status (draft|open|inprogress|closed)
md del <id>                           Delete a task
md add-dep <child> <parent>           Add a dependency
md init                               Initialize a new tasks file
md convert TASKS.md                   Convert between Markdown and CSV formats
md doctor                             Detect and fix duplicate task IDs
md prime                              Print LLM context for using md
md mcp                                Start MCP server over stdio
md webui                              Launch web UI for this TASKS file
```

### Web UI + VS Code extension

`md webui` hosts a localhost HTTP server with a web UI for viewing and
editing one `TASKS.md` (or `.csv`) file. It prints a single JSON line to
stdout on startup with the URL and access token:

```bash
md webui
# MEADS_WEBUI {"url":"http://127.0.0.1:54231","token":"…","file":"TASKS.md","format":"md"}
```

Pass `--open` to launch the browser automatically, or `--port 3000` for a
fixed port. All routes require the bearer token; change events are
streamed over Server-Sent Events at `/api/events`.

A VS Code extension in [`vscode/`](vscode/) registers itself as the default
renderer for `TASKS.md` / `TASKS.csv` and embeds the web UI. For v1 it
ships as a `.vsix` attached to each GitHub release — download from
[Releases](https://github.com/jpillora/meads/releases) and install via
*Extensions: Install from VSIX…*.

### Examples

**Add a task with type, priority, and body:**

```bash
md add "bug: Fix login P1. Session cookie expires prematurely"
```

**View ready work (sorted by priority):**

```bash
md ready
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

## Merging TASKS.md

When multiple branches create tasks independently (common with AI agents working in parallel), they each assign the next available ID locally. After merging, this can result in duplicate task IDs in the same file.

To fix this, run:

```bash
md doctor
```

`md doctor` scans for duplicate IDs and renumbers them. The first occurrence keeps its original ID; subsequent duplicates get new IDs. Any `depends-on` references within renumbered tasks are also updated to point to the correct new IDs.

### Notes

- **Format** — Two storage backends, auto-detected by file extension:
  - `TASKS.md` — Markdown headings (`## 1. Title`) with `* key: value` metadata. Human-readable diffs.
  - `TASKS.csv` — Standard CSV with soft-delete and computed next-id. Easy to import into spreadsheets and other tools.
- **Metadata** — Built-in keys are `status`, `priority`, `type`, `depends-on`, `tags`, `close-reason`, `created`, and `updated`.
- **Concurrency** — Concurrent writes are safe via optimistic file locking, so multiple processes (or AI agents) can write to the task file simultaneously without corruption.
- **AI-friendly** — Both formats are designed to be readable and writable by LLMs. Use `md prime` to print context for an AI agent, or `md mcp` to run an MCP server over stdio.
- **Minimal** — Single static binary, no config files.
