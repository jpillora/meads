# TASKS

a [meads](https://github.com/jpillora/meads) (`md`) managed task log

* created: 2026-02-14T11:42:09Z
* updated: 2026-03-05T11:27:22Z
* next-id: 13

## 6. auto-set next-id

to allow hand writing of the TASKS.md file. any time the TASKS.md file is locked. it should confirm that `next-id` is set HIGHER than the highest-id. if not, it should automatically reset it to highest-id + 1.

## 12. list --history, to list all tasks using git history

* status: open
* priority: P2
* type: feature
* created: 2026-03-05T11:27:22Z

it should also reorder them into ascending order, so you see task 1 all the way to the end of the (virtual) file
