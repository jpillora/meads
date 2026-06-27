# Meads (`md`) Task Tracking Context

## Overview

`md` is a git-native task tracker that stores all tasks in a single file — `TASKS.md` (Markdown) or `TASKS.csv` (CSV). No database, no config files — just your task file and git.

## MCP Server

The `md` MCP server is enabled. **Use MCP tools instead of CLI commands** for all task operations. The following tools are available:

### Finding Work
- `ready_tasks` - List open tasks not blocked by dependencies (sorted by priority)
- `list_tasks` - List all tasks
- `get_task(id)` - Get a specific task by ID

### Creating Tasks
- `add_task(title, [status], [priority], [type], [description])` - Add a new task
  - `title` (required): task title
  - `status`: draft, open, inprogress, closed
  - `priority`: P0-P9 (0=critical, 4=backlog, default=P2)
  - `type`: bug, task, feature
  - `description`: detailed description

### Updating Tasks
- `update_task(id, [status], [priority], [title], [description])` - Update a task
- `delete_task(id)` - Delete a task

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
- **Do NOT read or edit the task file (TASKS.md or TASKS.csv) directly** - always use MCP tools to read and modify tasks
- **Do NOT shell out to `md` CLI commands** - use the MCP tools above
- **ALWAYS include changes to the task file in the next git commit after task is closed**
- Task IDs are auto-assigned integers
- Git handles versioning and history
