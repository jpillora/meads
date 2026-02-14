# TASKS

a [meads](https://github.com/jpillora/meads) (`md`) managed task log

* created: 2026-02-14T11:42:09Z
* updated: 2026-02-14T11:46:18Z
* next-id: 6

## 1 implement mcp server

* status: open

implement an mcp server over stdin for simple integration with existing tools

it should be a simple `md mcp` command that listens over stdio, and exposes commands to edit TASKS.md.

## 2 implement a "prime" command

embed a basic AGENTS.md file which can be created/appended/feed-into LLMs to teach them how to use `md`

## 3 in markdown, suffix numbers with dot

so `## 42 foo bar` becomes `## 42. foo bar`

parse should still work without it, it just needs the space.

but it should always write dot -> space. `<N>. `

## 4 list --md flag

* status: open

which just print  the tasks list as plain markdown

## 5 auto delete

* status: open
* created: 2026-02-14T11:46:18Z

a bit dangerous, cos dont want to lose data, but with a precommit hook the experience is okay. if we detect (1) were on the default branch (2) the TASKS file is commited and in sync (3) then find all tasks which are closed (4) delete them (5) ammend-commit with deletion.

with all this, users need to `md auto-delete` and this adds a git hook to do the checks above and just auto delete as progress in git occors

## 6 auto-set next-id

to allow hand writing of the TASKS.md file. any time the TASKS.md file is locked. it should confirm that `next-id` is set HIGHER than the highest-id. if not, it should automatically reset it to highest-id + 1.

## 7 add --limit -n flag to "ready"

limits number of results