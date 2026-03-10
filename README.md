# meads

> [beads](https://github.com/steveyegge/beads) but much simpler

Git-native task tracking in a single file. No database, no server, no dependencies — just `TASKS.md` (or `TASKS.csv`) and git.

[![GoDev](https://img.shields.io/static/v1?label=godoc&message=reference&color=00add8)](https://pkg.go.dev/github.com/jpillora/meads)
[![CI](https://github.com/jpillora/meads/workflows/CI/badge.svg)](https://github.com/jpillora/meads/actions?workflow=CI)

### Features

- All state lives in a single file — `TASKS.md` (Markdown) or `TASKS.csv` (CSV) — commit it to git and get full history for free
- Two storage formats: Markdown for human-readable diffs, CSV for tooling and spreadsheets
- Rich input parsing: type prefixes (`bug:`, `feature:`), priority (`P0`-`P9`), title and description in one string
- Task dependencies with automatic blocking detection
- Concurrent-write safe via optimistic locking
- AI-friendly — `md prime` prints LLM context, `md mcp` runs an MCP server over stdio

### Install

**Binaries**

[![Releases](https://img.shields.io/github/release/jpillora/meads.svg)](https://github.com/jpillora/meads/releases)
[![Releases](https://img.shields.io/github/downloads/jpillora/meads/total.svg)](https://github.com/jpillora/meads/releases)

Find [the latest pre-compiled binaries here](https://github.com/jpillora/meads/releases/latest) or download and install it now with `curl https://i.jpillora.com/meads! | bash`

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
# 1 Fix the login bug
# 2 Write tests for login fix

md set-status 1 closed
# updated task 1
```

The resulting `TASKS.md`:

```markdown
# TASKS

a [meads](https://github.com/jpillora/meads) (`md`) managed task log

* created: 2025-01-15T09:00:00Z
* next-id: 3

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
md get <id>                           View a task
md list                               List all tasks
md ready                              Show unblocked open tasks (by priority)
md update <id> --priority=P1          Update task fields
md set-status <id> <status>           Change status (draft|open|inprogress|closed)
md del <id>                           Delete a task
md add-dep <child> <parent>           Add a dependency
md prime                              Print LLM context for using md
md mcp                                Start MCP server over stdio
```

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

### Notes

- **Format** — Two storage backends, auto-detected by file extension:
  - `TASKS.md` — Markdown headings (`## 1. Title`) with `* key: value` metadata. Human-readable diffs.
  - `TASKS.csv` — Standard CSV with soft-delete and computed next-id. Easy to import into spreadsheets and other tools.
- **Metadata** — Built-in keys are `status`, `priority`, `type`, and `depends-on`.
- **Concurrency** — Concurrent writes are safe via optimistic file locking, so multiple processes (or AI agents) can write to the task file simultaneously without corruption.
- **AI-friendly** — Both formats are designed to be readable and writable by LLMs. Use `md prime` to print context for an AI agent, or `md mcp` to run an MCP server over stdio.
- **Minimal** — Single static binary, no config files.
