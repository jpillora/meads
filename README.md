# 🍻 meads

Git-native task tracking in a single Markdown file. No database, no server, no dependencies — just `TASKS.md` and git.

## Quick Start

```bash
go install github.com/jpillora/meads/cmd/md@latest
```

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

All state lives in `TASKS.md`. Commit it to git and you get full history for free.

## Commands

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
md import beads                       Import from beads issue tracker
md prime                              Print LLM context for using md
md mcp                                Start MCP server over stdio
```

## Examples

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

## Installation

Requires Go 1.25.6+.

```bash
go install github.com/jpillora/meads/cmd/md@latest
```

Or download a binary from [releases](https://github.com/jpillora/meads/releases).

Or build from source:

```bash
git clone https://github.com/jpillora/meads.git
cd meads
go install ./cmd/md
```

## Notes

- **Format** — Tasks are stored as Markdown headings (`## 1 Title`) with `* key: value` metadata. The file is human-readable but all writes should go through the `md` CLI to maintain consistency.
- **Metadata** — Built-in keys are `status`, `priority`, `type`, and `depends-on`.
- **Concurrency** — Concurrent writes are safe. `meads` uses optimistic locking so multiple processes (or AI agents) can write to `TASKS.md` simultaneously without corruption.
- **AI-friendly** — The Markdown format is designed to be readable and writable by LLMs. Use `md prime` to print context for an AI agent, or `md mcp` to run an MCP server over stdio.
- **Minimal dependencies** — single static binary, no config files.
