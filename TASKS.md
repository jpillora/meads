# TASKS

a [meads](https://github.com/jpillora/meads) (`md`) managed task log

* created: 2026-02-14T11:42:09Z
* updated: 2026-02-14T14:05:07Z
* next-id: 11

## 1. implement mcp server

* status: inprogress
* updated: 2026-02-14T12:17:38Z

implement an mcp server over stdin for simple integration with existing tools

it should be a simple `md mcp` command that listens over stdio, and exposes commands to edit TASKS.md.

use `github.com/modelcontextprotocol/go-sdk` server

it should have FAST unit tests, since the interface should be io.ReadWriter. and just use an `go-sdk` MCP client

## 5. auto delete

* status: draft
* created: 2026-02-14T11:46:18Z
* updated: 2026-02-14T12:53:08Z

a bit dangerous, cos dont want to lose data, but with a precommit hook the experience is okay. if we detect (1) were on the default branch (2) the TASKS file is commited and in sync (3) then find all tasks which are closed (4) delete them (5) ammend-commit with deletion.

with all this, users need to `md auto-delete` and this adds a git hook to do the checks above and just auto delete as progress in git occors

## 6. auto-set next-id

to allow hand writing of the TASKS.md file. any time the TASKS.md file is locked. it should confirm that `next-id` is set HIGHER than the highest-id. if not, it should automatically reset it to highest-id + 1.

## 10. Test closed task for auto-delete

* status: closed
* created: 2026-02-14T14:05:07Z
