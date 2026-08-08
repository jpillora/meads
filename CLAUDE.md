# Meads (`md`) Task Tracking Context

## Agent Directives

- After completing a task successfully, always git commit and git push
- Always rebuild `md` by running `go install` from `cmd/md/`
- Always use `md` to manage tasks, see below

## Overview

`md` is a git-native task tracker. It supports two storage modes: a single
tasks file (`TASKS.md` or `TASKS.csv`) or, alternatively, git mode (tasks
stored directly as git refs, no working-tree file at all - see `md prime`'s
own "Overview" for how to tell which one is active). **This repo currently
uses file mode** - the tracked `TASKS.md` you see in `git status` is real,
and every rule below assumes it. If that ever changes (e.g. after `md
convert --to-git`), re-run `md prime` for instructions that match whichever
mode is actually active - it always describes the real thing, not a
hardcoded assumption the way this file's hand-written rules below do.

## Essential Commands

### Finding Work
- `md ready` - Show open tasks not blocked by dependencies (sorted by priority)
- `md list` - List all tasks
- `md list --json` - List all tasks as JSON
- `md list --tag=api` / `md ready --tag=api` - Filter by tag (`--tag=a,b` requires both)
- `md get <id>` - Get a specific task (a deleted task absent from TASKS.md is recovered from git history)
- `md get --json <id>` - Get a specific task as JSON

### Creating Tasks
- `md add "Fix the login bug"` - Add a simple task
- `md add "bug: Fix login P1. Session cookie expires"` - Rich input parsing
  - Type prefix: `bug:`, `task:`, `feature:`, `idea:` (optional)
  - Priority: `P0`-`P9` (0=critical, 4=backlog, default=P2)
  - Title: text before the first `. ` (period+space) or newline
  - Description: text after that split point
- `md add --title="Fix login" --type=bug --priority=P1 --description="Details here"` - Flag-based
- `md add --title="Fix login" --description-file=/path/to/notes.md` - Description from file
- For rich Markdown, read the description from a quoted HEREDOC so the shell passes backticks, dollar signs, and backslashes through literally:
  ```bash
  md add --title="Fix login" --description-file=- <<'EOF'
  ## Context

  The session cookie expires while `document.hidden` is true.
  EOF
  ```
- `md add --tags=api,web-ui "Fix login"` - Set tags (lowercase letters, numbers and dashes)

### Updating Tasks
- `md update <id> --status=draft|open|inprogress|closed` - Update status
- `md update <id> --priority=P1` - Update priority
- `md update <id> --title="New title"` - Update title
- `md update <id> --description-file=/path/to/notes.md` - Update description from file
- For rich Markdown, prefer stdin via a quoted HEREDOC; `--description-file=-` means read stdin and needs no shell escaping:
  ```bash
  md update <id> --description-file=- <<'EOF'
  ## Notes

  - Preserves `code spans` literally
  - Supports real newlines without `\n` escapes
  EOF
  ```
- `md update <id> --tags=api,web-ui` - Replace all tags (`--tags=` clears them)
- `md update <id> --add-tags=docs` / `--rm-tags=api` - Add or remove tags, keeping the rest
- `md set-status <id> <status>` - Shorthand for status changes
- `md del <id>` - Delete a task

### Dependencies
- `md add-dep <child> <parent>` - Make child depend on parent
- `md rm-dep <child> <parent>` - Remove child's dependency on parent
- `md add --depends-on=<id> "Task title"` - Add task with dependency
- Tasks blocked by unclosed dependencies are excluded from `md ready`

## Common Workflows

**Starting a session:**
```bash
md ready              # Find available work
md get <id>           # Review task details
md set-status <id> inprogress  # Claim it
```

**Creating dependent tasks:**
```bash
md add "feature: Build API endpoint"    # Returns ID, e.g. 5
md add "Write tests for API. Cover edge cases" --depends-on=5
```

**Completing work:**
```bash
md set-status <id> closed    # Mark task done
```

## Rules
- **Do NOT read or edit TASKS.md directly** - always use `md` commands to read and modify tasks
- **ALWAYS include changes to TASKS.md in the next git commit after task is closed**
- Task IDs are auto-assigned integers
- Git handles versioning and history
- The file uses optimistic locking for concurrent access

## Git Hooks (auto-save / auto-delete)

A pre-commit hook (installed by `md auto-save`) **auto-stages `TASKS.md` into
every commit**, printing `md: auto-staged TASKS.md` to stderr. So `TASKS.md` may
appear in a commit you did not explicitly `git add` — this is expected. **Do NOT
unstage, `git rm`, or revert it to "tidy" a commit; let the hook include it.**

A companion `md auto-delete` hook prunes closed tasks from `TASKS.md` on commit
(printing `md: removed closed task N`), so closed tasks drop out of the file
while staying recoverable from git history (`md get <id>` reads them back).

Both hooks **skip during `git rebase`, `git merge`, and `git cherry-pick`** —
staging would race git for `.git/index.lock` and is redundant while a commit is
replayed. If a plain `git commit --amend` ever fails with an index-lock error,
bypass the hook just for that op with `git -c core.hooksPath=/dev/null commit
--amend` (this is a targeted override, not `--no-verify`).
