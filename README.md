# meads

Beads, but **m**uch simpler, built with **M**arkdown.

Uses a single `TASKS.md` file as the database and relies on git for history.

## TASKS.md Format

The file is human-readable but **not** human-writable. All mutations go through the `md` CLI tool.

Tasks are stored as a flat series of entries:

```markdown
## 0001 Fix the login bug

* status: open
* priority: 1
* depends-on: 0003

The login page throws a 500 when the session cookie is expired.
```

### Keys

- `status` — `open`, `inprogress`, or `closed`
- `priority` — numeric, higher priority tasks are listed first
- `depends-on` — ID of another task this one depends on

You can set any other key you like. The format is freeform and LLM-queryable.

## CLI — `md`

All writes to `TASKS.md` go through the `md` command:

| Command    | Description                                      |
|------------|--------------------------------------------------|
| `md add`   | Add a new task                                   |
| `md del`   | Delete a task                                    |
| `md ready` | Show all open tasks (not blocked by dependencies)|

## File Locking

`meads` uses a simple append-based lock:

1. Append `\nlock:<random-uuid>\n` to `TASKS.md`
2. Read the file back
3. If your UUID is the **first** `lock:` line in the file, you hold the lock and can proceed with changes
4. Otherwise, back off — another writer won the race
