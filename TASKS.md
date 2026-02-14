# TASKS

a [meads](https://github.com/jpillora/meads) (`md`) managed task log

* created: 2026-02-14T11:42:09Z
* updated: 2026-02-15T00:27:39Z
* next-id: 11

## 5. auto delete

* status: draft
* created: 2026-02-14T11:46:18Z
* updated: 2026-02-14T12:53:08Z

a bit dangerous, cos dont want to lose data, but with a precommit hook the experience is okay. if we detect (1) were on the default branch (2) the TASKS file is commited and in sync (3) then find all tasks which are closed (4) delete them (5) ammend-commit with deletion.

with all this, users need to `md auto-delete` and this adds a git hook to do the checks above and just auto delete as progress in git occors

## 6. auto-set next-id

to allow hand writing of the TASKS.md file. any time the TASKS.md file is locked. it should confirm that `next-id` is set HIGHER than the highest-id. if not, it should automatically reset it to highest-id + 1.
