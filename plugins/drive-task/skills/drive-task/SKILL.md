---
name: drive-task
description: Drive one Meads (`md`) task from a task ID or “drive task” request through related-task reconciliation, planning, isolated worktree implementation, adversarial review, mandatory human completion review, gated integration, cleanup, and closure. Use only when the user asks to “drive task” or explicitly invokes `$drive-task`; do not use for planning-only requests, ordinary implementation, arbitrary pull requests, or repositories that do not use Meads.
---

# Drive task

Drive one task from intake through planning, implementation, verification, integration, and cleanup. Continue until the task is closed or a concrete decision or external blocker requires the user.

You are authorized to perform the normal local workflow mutations described by the repository instructions: create and update the Meads task, create a task branch and linked worktree, implement and test there, commit reviewed work, integrate it locally into the repository's integration branch, use the repository's staging environment, and safely clean up.

You are not authorized to push code, deploy to production, use force operations, discard unrelated work, change credentials, or bypass a required human decision.

When goal tools are available, create a goal for the outcome and keep it active until the outcome is complete. Keep any available progress note current throughout the drive.

Before acting, read `AGENTS.md` completely and run `md --help`. Treat the runbook as authoritative for priorities, branch names, worktree locations, deployments, validation, integration, agent coordination, notifications, and cleanup. Treat the installed Meads CLI help as authoritative for task commands. Do not assume names or commands that the repository or CLI does not define.

## Coordinate shared resources

The repository's integration branch and staging deployment are shared resources. Task branches, task worktrees, and per-worktree deployments are isolated unless `AGENTS.md` says otherwise.

Before accessing the integration branch, and before building, deploying, restarting, or testing the shared staging environment:

1. Reliably identify the current agent or terminal using the environment and tools documented by the repository. Never guess the sender identity.
2. Use the available agent or terminal coordination tools to inspect every other active agent and determine whether one is using or about to use the same resource.
3. If the resource is clearly free, proceed. Repeat this check immediately before a mutation when substantial work followed the earlier check.
4. If another agent is using the resource, or ownership is ambiguous, do not touch it. Send an informational message that identifies this agent, states the resource and operation needed, and says that this drive will wait. Do not ask the other agent to abandon or interrupt its work.
5. Wait using the repository's event-based coordination mechanism when available, then inspect the active agents again. A transient idle state alone is not confirmation that a resource is free.
6. Keep the shared-resource window short. Tell any waiting agent when the resource is free.

Run this gate separately for initial integration-branch synchronization and branch creation, final rebase and integration, and each staging interaction. An earlier check does not reserve a resource for the rest of the drive. If the repository provides no reliable way to coordinate a shared mutation, pause and ask the user before performing it.

## 1. Create and reconcile the Meads task

The user's current instruction is authoritative over tracked tasks. Create a new Meads task for the current drive unless the user explicitly asks to continue an existing drive.

If the request names a Meads task ID, read it with `md get <id>` as context for the new task. For a free-form request, inspect draft, open, and in-progress tasks with `md list --json` and compare titles and descriptions semantically. Use `md list --history` only when history could clarify the relationship. Existing tasks may inform the new task but must not override the current instruction.

Create the new task with `md add` from the user's current intent and retain its returned ID. Use the repository's default priority unless the user supplied one or repository evidence supports another priority under `AGENTS.md`.

Reconcile every related old task after creating the new one:

- Update its description with an accurate account of what has already been done.
- Mention the new task by ID and explain which scope moved to it.
- If the new task supersedes all remaining scope, close the old task.
- Otherwise, keep the old task non-closed and state exactly what remains.

Do not close an old task merely because it overlaps. If a related task was already closed, preserve that status while adding any needed relationship note.

Use only `md` commands for Meads task storage. Never manipulate `refs/meads/*` or another Meads backing store directly.

Once the new task exists and related tasks are reconciled, use `md update` to normalize its description to these top-level sections while preserving useful evidence and intent:

```markdown
## Summary

<current state and problem>

<target state and solution>

## Drive progress

- [x] 1. Create and reconcile the Meads task
- [ ] 2. Plan the task
- [ ] 3. Implement in a worktree
- [ ] 4. Verify changes
- [ ] 5. Adversarial review
- [ ] 6. Apply the human completion review gate
- [ ] 7. Commit and integrate
- [ ] 8. Clean up and close the task

## Plan

<implementation-ready plan>

## Files to modify

<approximate files and line-count delta>

## Implementation notes

<post-implementation notes>

## Verification notes

<post-verification notes>
```

## 2. Plan the task

Read the whole task, related tasks, current code, tests, and relevant history. Confirm that the request is still valid. Ask only questions whose answers materially change scope, behavior, risk, or acceptance.

Make the plan implementation-ready: intended modules and boundaries, behavior, edge cases, acceptance criteria, focused validation, and live verification where relevant. Prefer a modular change where the code has a genuine boundary, but do not invent abstractions to satisfy the template. Use `md update` to update the task's **Plan** and **Files to modify** sections.

Check the `2. Plan the task` item only when the plan is ready to implement.

## 3. Implement in a worktree

Set the task to in progress with `md set-status <id> inprogress`.

Synchronize the repository-defined integration branch with its remote according to `AGENTS.md`. Create a task branch named `md<id>` and a linked worktree at the repository-defined location. Keep all implementation in the task worktree. Start a per-worktree deployment only when runtime, API, or browser validation requires it.

Split the implementation into independently verifiable stages. When subagents are available and authorized, delegate each stage sequentially with a specific prompt that confines it to the task and worktree. Review each result before continuing and use another focused pass for fixes when useful. Keep the progress note and task checklist current as stages complete.

If evidence changes the plan, update the recorded plan before diverging. Record useful decisions in **Implementation notes**.

## 4. Verify changes

Provide evidence that the change works. Run the repository-required automated checks and use a worktree deployment, API client, or browser for live validation when relevant.

Retain fresh completion evidence. For visual changes, capture a screenshot that clearly demonstrates the result. For non-visual changes, retain concise command output, request and response evidence, or another observable artifact. Record the evidence in **Verification notes**.

## 5. Adversarial review

When subagents are available and authorized, run an independent adversarial review with the strongest available reasoning and no unnecessary model override. Give the reviewer the raw task, repository constraints, complete diff, untracked files, and validation evidence without priming it with an intended answer or suspected issue.

Ask for concrete correctness, security, data-loss, regression, concurrency, cleanup, test, and acceptance failures with file and line evidence. Have the reviewer rank findings using the repository's priority definitions.

Assess every finding. Resolve all findings that the repository classifies as release-blocking or as material correctness issues. Resolve lower-priority findings when the value justifies the scope. If independent review is unavailable, perform a fresh self-review and disclose that limitation at the human completion review gate.

## 6. Apply the human completion review gate

This gate is mandatory and never auto-clears. Reach it only when implementation and review fixes are complete, every acceptance criterion and required check passes, and a final end-to-end test succeeds against the worktree deployment when one is relevant. Keep the task worktree and deployment open and unchanged throughout the gate.

Prepare a review request that includes:

- The task and worktree.
- Diff size and file statistics.
- Validation evidence and review findings.
- Residual risks.
- The exact proposed commit and integration action.
- A verified review URL when the repository provides a remotely reachable worktree deployment.
- A fresh screenshot attachment for visual work, using the repository's configured notification mechanism.

Notify the user through the configured mechanism, then pause for explicit approval. Notification delivery, silence, and automatic continuations are not approval. Do not commit, integrate, stop the deployment, remove the worktree or branch, or close the task while approval is pending.

Awaiting approval is a normal in-progress state, not a failed or blocked drive. Keep the goal active and retain the worktree and deployment unchanged. Do not emit the final wrap-up while waiting.

If required end-to-end testing or demonstrative evidence is not possible, do not present the work as complete or send the completion-review notification. Report the concrete blocker and preserve the recoverable worktree state.

## 7. Commit and integrate

Proceed only after the user explicitly approves the human completion review gate.

Re-run the shared-resource gate. Synchronize the integration branch, then rebase the task branch if needed. If conflicts require significant rework, update the task checklist and return to verification. Resolve only minor, mechanical conflicts during the rebase.

Commit the reviewed work and perform a clean linear fast-forward integration according to `AGENTS.md`. Do not push unless the user separately authorizes it.

Run the required checks again after integration, including a final end-to-end test when applicable.

## 8. Clean up and close the task

After the work lands in the integration branch, stop and remove the task deployment, remove the linked worktree, delete the task branch, and clean up only task-owned resources.

Re-read the task with `md get <id>`, correct any inaccuracies with `md update`, complete the progress checklist, and close it with `md set-status <id> closed`.

The final wrap-up is a terminal report, not a gate-status update. Emit it only when the drive genuinely terminates. A pending human review does not terminate the drive. Use `FAIL` only when the drive terminates without completing and closing the task, not while approval is pending.

End every completed or terminated drive with these eight labeled lines in this order:

```text
Task summary: <what was done>
Commit status: <commit and local integration status, whether it was pushed, and LOC delta as +additions/-deletions>
Staging manual testing status: <passed, failed, not run, or not applicable, with concise evidence>
Verification status: <automated and other verification results>
Worktree status: <worktree, branch, deployment, and owned-resource cleanup or retention status>
Token usage: <goal-recorded token total, or unavailable>
Time taken: <elapsed goal runtime>
Final goal status: SUCCESS or FAIL
```

State any blocker in the relevant status line. Calculate the LOC delta from the landed commit, or from the remaining worktree diff when no commit exists. Report exact goal token and elapsed-time values when goal tools provide them.
