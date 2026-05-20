# TASKS

a [meads](https://github.com/jpillora/meads) (`md`) managed task log

* created: 2026-02-14T11:42:09Z
* updated: 2026-05-20T23:07:33Z
* next-id: 13

## 17. shorten the header timestamp (use relative time like the card footers)

* status: open
* priority: P3
* type: task
* created: 2026-05-20T16:41:02Z

Currently shows '· N tasks · updated 5/21/2026, 2:35:17 AM' in the file-meta — the locale string is ~20 chars and pushes the title to ellipsis on narrow widths. Card footers already use the relativeTime() helper ('updated 3m ago') — header should use the same so the format is consistent and compact. See renderMeta in pkg/webui/assets/app.js.

## 19. anchor card chips next to the title instead of pushing to far-right

* status: open
* priority: P3
* type: task
* created: 2026-05-20T16:41:14Z

.card .title has flex:1 which expands to fill the row, pushing chips and deps to the right edge. On a 1440px viewport the title 'Web ui responsive check' and the chips (inprogress//task) have hundreds of pixels of empty space between them. Suggested fix: change .card .title to flex: 0 1 auto (or use margin-right: auto on the title to push chips back left). See tmp/responsive/{1280,1440,1920}.png. May be obsoleted by the card max-width fix (#15).
