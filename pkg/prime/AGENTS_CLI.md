# Meads (`md`) Task Tracking Context

## Overview

`md` is a git-native task tracker that stores all tasks in a single file — `TASKS.md` (Markdown) or `TASKS.csv` (CSV). No database, no config files — just your task file and git.

## Essential Commands

### Finding Work
- `md ready` - Show open tasks not blocked by dependencies (sorted by priority)
- `md list` - List all tasks
- `md list --json` - List all tasks as JSON
- `md list --tag=api` - List tasks carrying a tag (comma-separated requires all of them, e.g. `--tag=api,backend`)
- `md ready --tag=api` - Same filter over ready work
- `md get <id>` - Get a specific task
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
- For rich Markdown, read the description from a quoted HEREDOC so the shell passes backticks, dollar signs, and backslashes literally:
  ```bash
  md add --title="Fix login" --description-file=- <<'EOF'
  ## Context

  The session cookie expires while `document.hidden` is true.
  EOF
  ```
- `md add --title="Fix login" --tags=api,web-ui` - Set tags (comma-separated; each tag is lowercase letters, numbers and dashes)

### Updating Tasks
- `md update <id> --status=draft|open|inprogress|closed` - Update status
- `md update <id> --priority=P1` - Update priority
- `md update <id> --title="New title"` - Update title
- `md update <id> --description="Short details"` - Update a short description inline (JSON-style escapes such as `\n` still decode)
- For rich Markdown, prefer stdin via a quoted HEREDOC; `--description-file=-` means read stdin and does not require shell escaping:
  ```bash
  md update <id> --description-file=- <<'EOF'
  ## Notes

  - Preserves `code spans` literally
  - Supports real newlines without `\n` escapes
  EOF
  ```
- `md update <id> --description-file=/path/to/notes.md` - Update description from a markdown file
- `md update <id> --tags=api,web-ui` - Replace all tags (`--tags=` clears them)
- `md update <id> --add-tags=docs` / `md update <id> --rm-tags=api` - Add or remove tags, keeping the rest
- `md set-status <id> <status>` - Shorthand for status changes
- `md del <id>` - Delete a task. The row is dropped, but a "max-id" mark in the file's project meta keeps its id from being reused
- `md del <id> --force` - Also release that id for reuse. Irreversible, and the next `md add` can hand the number to a different task
- `md restore <id>` - Undo a delete. File mode prunes tombstone rows on write, so this only reaches one the file still carries; anything else has to be recovered from the tasks file's git history

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
- **Do NOT read or edit the task file (TASKS.md or TASKS.csv) directly** - always use `md` commands to read and modify tasks
- **ALWAYS include changes to the task file in the next git commit after task is closed**
- Task IDs are auto-assigned integers
- Git handles versioning and history
- The file uses optimistic locking for concurrent access
