# TASKS

a [meads](https://github.com/jpillora/meads) (`md`) managed task log

* created: 2026-02-14T11:42:09Z
* updated: 2026-03-10T13:42:36Z
* next-id: 13

## 6. auto-set next-id

to allow hand writing of the TASKS.md file. any time the TASKS.md file is locked. it should confirm that `next-id` is set HIGHER than the highest-id. if not, it should automatically reset it to highest-id + 1.

## 11. Cycle detection in validateDeps

* status: open
* priority: P1
* type: bug
* created: 2026-03-10T13:30:57Z

Prevent circular dependencies that deadlock the ready queue. Simple DFS in validateDeps.
