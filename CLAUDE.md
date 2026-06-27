# Meads (`md`) Task Tracking Context

## Agent Directives

- After completing a task successfully, always git commit and git push
- Always rebuild `md` by running `go install` from `cmd/md/`
- Always use `md` to manage tasks, see below

## Overview

`md` is a git-native task tracker that stores all tasks in a single `TASKS.md` file. No database, no config files - just Markdown and git.

## Essential Commands

### Finding Work
- `md ready` - Show open tasks not blocked by dependencies (sorted by priority)
- `md list` - List all tasks
- `md list --json` - List all tasks as JSON
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

### Updating Tasks
- `md update <id> --status=draft|open|inprogress|closed` - Update status
- `md update <id> --priority=P1` - Update priority
- `md update <id> --title="New title"` - Update title
- `md update <id> --description-file=/path/to/notes.md` - Update description from file
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
