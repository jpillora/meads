# Meads (`md`) Task Tracking Context

## Overview

`md` is running in **git mode**: tasks live as git refs (`refs/meads/tasks/<id>`), not in a file. There is no `TASKS.md` or `TASKS.csv` in this repo.

## MCP Server

The `md` MCP server is enabled. **Use MCP tools instead of CLI commands** for all task operations. The tool set is identical to file mode:

### Finding Work
- `ready_tasks` - List open tasks not blocked by dependencies (sorted by priority)
- `list_tasks` - List all tasks
- `get_task(id)` - Get a specific task by ID (a soft-deleted task's ref is kept forever, so this still resolves it)

### Creating Tasks
- `add_task(title, [status], [priority], [type], [description])` - Add a new task
  - `title` (required): task title
  - `status`: draft, open, inprogress, closed
  - `priority`: P0-P9 (0=critical, 4=backlog, default=P2)
  - `type`: bug, task, feature
  - `description`: detailed description

### Updating Tasks
- `update_task(id, [status], [priority], [title], [description])` - Update a task
- `delete_task(id)` - Delete a task (soft delete — see Rules)

### Dependencies
- `add_dependency(child_id, parent_id)` - Make child depend on parent
- `remove_dependency(child_id, parent_id)` - Remove child's dependency on parent
- Tasks blocked by unclosed dependencies are excluded from `ready_tasks`

## Common Workflows

**Starting a session:**
1. Call `ready_tasks` to find available work
2. Call `get_task(id)` to review task details
3. Call `update_task(id, status="inprogress")` to claim it

**Creating dependent tasks:**
1. Call `add_task(title="Build API endpoint", type="feature")` - returns new task ID
2. Call `add_task(title="Write tests for API", description="Cover edge cases")`  - returns new task ID
3. Call `add_dependency(child_id=<test_task_id>, parent_id=<api_task_id>)`

**Completing work:**
1. Call `update_task(id, status="closed")` to mark done

## Rules
- **There is no task file** - do NOT look for or try to read/edit `TASKS.md`/`TASKS.csv`; it does not exist in git mode. Always use MCP tools.
- **Do NOT shell out to `md` CLI commands or raw `git` plumbing on `refs/meads/*`** - use the MCP tools above
- **Nothing to stage or commit yourself** - every mutating tool call commits straight to that task's own ref the moment it runs.
- If a remote (`origin`) is configured, meads pushes `refs/meads/*` there automatically, at most once per `pushInterval` (default 1m). The push is synchronous but bounded by a timeout, and never fails the operation if the remote is unreachable.
- `delete_task` never removes anything - it soft-deletes (the ref is kept forever), so a deleted id is never reused and `get_task` still resolves it.
- Concurrent writes are safe via compare-and-swap on each task's own ref.
- Task IDs are auto-assigned integers.
