# Hook Staging vs `.git/index.lock`

Why the pre-commit hooks retry their `git add`, and how the failure they guard
against was diagnosed. Recorded here because the investigation took two
occurrences and one wrong turn (TASKS #67, fixed 2026-07-26).

## Symptom

`md auto-delete` aborts the commit it runs inside:

```
md: auto-staged TASKS.md          <- auto-save's git add succeeded
md: removed closed task 58        <- auto-delete rewrote TASKS.md
staging TASKS.md: exit status 128 <- auto-delete's git add FAILED
```

Intermittent, and never destructive: `runFromHook` restores its backup on a
failed `git add`, so the closed task reappears and staged changes survive.
Retrying the commit succeeds.

## Root cause

Contention for `.git/index.lock` with a *foreign* git process.

Rewriting `TASKS.md` leaves its stat info stale in the index. The next
index-refreshing git command — `git status`, `git diff`, `git diff-index` —
must re-hash the file and write the refreshed index back, and that write takes
`.git/index.lock`. auto-delete issues its `git add` microseconds after the
rewrite, i.e. straight into the window it just created.

Such a command runs constantly in a normal dev environment. A shell prompt with
git dirty-state enabled (`__fish_git_prompt_showdirtystate yes`, or the
zsh/starship equivalents) runs one on every prompt render; editors and sibling
agents add more.

Two details that initially looked contradictory both fall out of this:

- **Only auto-delete fails, never auto-save** — even though auto-save runs its
  identical `git add` moments earlier in the same hook. auto-save does not
  rewrite the file first, so it is not racing a window of its own making.
- **No `git commit` is required.** A standalone `GITHOOK=1 md auto-delete` from
  an interactive shell failed the same way, then succeeded on immediate retry.

### The wrong turn

An early investigation "ruled out" index.lock: a scratch-repo hook probed for
`.git/index.lock` and found it absent, and two consecutive `git add` calls both
succeeded. That was a false negative. The contention is a sub-second race
against another process, so a point-in-time probe almost always misses it, and
two adds in a quiet repo race nothing at all.

Compounding it, `ExecGit.Run` used `cmd.Run()`, which discards stderr — so both
occurrences reported a bare `exit status 128` and git's `fatal:` line, which
names the lock outright, was thrown away.

## Reproduction

Prepend a lock-holder to the repo's pre-commit hook so a lock is held across the
window the hook runs in:

```bash
touch .git/index.lock; ( sleep 0.4; rm -f .git/index.lock ) &
```

Then close a task and commit. Before the fix this reproduces every time:

```
md: removed closed task 4
staging TASKS.md: exit status 128
COMMIT EXIT=1
```

After the fix, the same scenario commits cleanly (`COMMIT EXIT=0`) with the task
pruned and staged.

## Fix

- `ExecGit.Run` captures stderr and folds it into the returned error. A stuck
  lock now reports `staging TASKS.md: exit status 128: fatal: Unable to create
  '/repo/.git/index.lock': File exists.` rather than an exit code.
- `meads.IsIndexLocked(err)` matches on the `index.lock` path fragment, not
  git's prose — git translates the prose when a message catalogue is installed.
- `stageFile` (cmd/md/stage.go), shared by both hooks, retries `git add` on lock
  contention with 20/40/80/160/320/640ms backoff (~1.2s total) and fails fast on
  every other error. Contending operations are short, so waiting clears it.

A lock that outlives the backoff is not contention but a stale
`.git/index.lock` from a crashed git process, which waiting cannot fix. That
still errors, and auto-delete still restores its backup.

## Why the hooks stay separate

Merging auto-save and auto-delete into one invocation would remove the
double staging, but they are not interchangeable: auto-save runs on every
branch, auto-delete only on the default branch, and each is independently
installable (`md auto-save --disable` / `md auto-delete --disable`). The
double `git add` only happens on the removal path — auto-delete returns early
when there is nothing to prune — so the redundancy is rare. `stageFile` gives
the shared-code benefit without the semantic change.

## Related

`sequencerInProgress` (cmd/md/hook.go) is a different guard for the same
resource: during rebase/merge/cherry-pick, git holds the index for the replay,
so both hooks skip staging entirely rather than retry into a lock that will not
clear. See TASKS #54.
