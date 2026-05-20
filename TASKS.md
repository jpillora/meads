# TASKS

a [meads](https://github.com/jpillora/meads) (`md`) managed task log

* created: 2026-02-14T11:42:09Z
* updated: 2026-05-20T15:27:23Z
* max-id: 12
* next-id: 13

## 3. webui keyboard shortcuts

* status: open
* priority: P2
* type: feature
* created: 2026-05-20T11:55:18Z

j/k to move card focus, n to new, e to edit, d to delete (with confirm), / to focus filter, esc to clear, enter to advance status. Show a help popover with ?

## 4. webui remove-dependency support

* status: open
* priority: P2
* type: feature
* created: 2026-05-20T11:55:18Z

Add DELETE /api/tasks/{id}/deps/{parent_id} and a chip-x UI on each dependency. Title fix is the JS comment in submitEditor flagging this as out of v1 scope.

## 5. webui dependency picker in editor

* status: open
* priority: P3
* type: feature
* created: 2026-05-20T11:55:18Z

Replace the plain comma-separated ID field with an autocomplete of existing tasks, showing id + title.

## 6. webui status-reason inline prompt

* status: open
* priority: P2
* type: feature
* created: 2026-05-20T11:55:18Z

When advancing to blocked or closed via the card button, prompt for a reason and PATCH it alongside status.

## 7. webui sort/group controls

* status: open
* priority: P3
* type: feature
* created: 2026-05-20T11:55:18Z

Header dropdown to choose sort (priority|id|status|updated) and toggle grouping by status. Persist in localStorage.

## 8. webui hide-closed toggle

* status: open
* priority: P3
* type: feature
* created: 2026-05-20T11:55:18Z

Header chip/checkbox to hide closed tasks; persist in localStorage; default to hidden once we have a way to see them again.

## 9. webui show created/updated per card

* status: open
* priority: P3
* type: feature
* created: 2026-05-20T11:55:18Z

Add a small muted timestamp in the card footer using the task meta map.

## 10. webui success toast on action

* status: open
* priority: P3
* type: task
* created: 2026-05-20T11:55:18Z

Currently only errors toast. Show a transient success toast after add/update/delete to confirm the change landed.

## 11. webui copy URL button

* status: open
* priority: P3
* type: task
* created: 2026-05-20T11:55:18Z

Header button to copy the current URL (with token) to the clipboard so users can share quickly with collaborators on the same host.
