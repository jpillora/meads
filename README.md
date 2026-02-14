# meads

Git-native task tracking in a single Markdown file. No database, no server, no dependencies — just `TASKS.md` and git.

## Quick Start

```bash
go install github.com/jpillora/meads@latest
```

```bash
md add "Fix the login bug" "500 error when session cookie is expired"
# added task 0001

md add "Write tests for login fix"
# added task 0002

md ready
# 0001 Fix the login bug
# 0002 Write tests for login fix

md del 0001
# deleted task 0001
```

All state lives in `TASKS.md`. Commit it to git and you get full history for free.

## Examples

**Add a task with a description:**

```bash
md add "Implement OAuth" "Add Google sign-in using OAuth 2.0 flow"
```

**View ready work (sorted by priority):**

```bash
md ready
# 0003 Critical hotfix        (priority: 5)
# 0001 Implement OAuth         (priority: 2)
```

**Use dependencies to block tasks:**

Edit `TASKS.md` to add `* depends-on: 0001` to a task. It won't appear in `md ready` until task `0001` is closed.

**Track progress with status:**

Tasks support three statuses: `open`, `inprogress`, `closed`. Set them by editing the `* status:` line in `TASKS.md`.

## Installation

Requires Go 1.25.6+.

```bash
go install github.com/jpillora/meads@latest
```

Or build from source:

```bash
git clone https://github.com/jpillora/meads.git
cd meads
go build -o md .
```

The binary is called `md`. Place it on your `PATH`.

## Notes

- **Format** — Tasks are stored as Markdown headings (`## 0001 Title`) with `* key: value` metadata. The file is human-readable but all writes should go through the `md` CLI to maintain consistency.
- **Metadata** — Built-in keys are `status`, `priority`, and `depends-on`. Custom keys are supported — add any `* key: value` line you need.
- **Concurrency** — Concurrent writes are safe. `meads` uses append-based optimistic locking so multiple processes (or AI agents) can write to `TASKS.md` simultaneously without corruption.
- **AI-friendly** — The Markdown format is designed to be readable and writable by LLMs. Point an agent at `TASKS.md` and it can parse, query, and reason about your backlog.
- **Minimal dependencies** — single static binary.

## Future

- **Task history** — `md history 0001` to show when a task was added, modified, and closed. Since all state lives in `TASKS.md`, this is just `git blame` and `git log` under the hood — no extra metadata to store or sync.
