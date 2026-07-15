---
name: ocman-session-splitting
description: Use when splitting ocman/OpenCode work into child sessions, parallel sessions, or git worktrees via MCP, or when a task needs to change files in a different project than the current working directory.
---

# Ocman Session Splitting

Use ocman MCP tools as small orchestration actions. Keep policy in this skill, not in MCP help tools or large tool descriptions.

## When To Split

Split when work has independent parts that can finish without blocking the parent:

- Large investigation across unrelated areas.
- Test/lint/debug task that can run while parent continues.
- Isolated implementation that should not touch parent worktree.
- Review or verification of current changes by a second session.

Do not split for tiny edits, single-file reads, or tasks needing constant parent decisions.

## Cross-Project Changes

Always split when a task requires editing, creating, or deleting files that
live **outside the current working directory / project root** — a sibling
repo, a dependency checked out elsewhere, any path under a different project.
Do not edit those files inline. Delegate to a child rooted in that project:

1. `get_current_session_id` for the parent id.
2. `new_session(session_id, intent, worktree=false)` — the child's working
   directory becomes the other project's root, so its edits/builds/staging
   stay isolated from parent work. Use `worktree=true` + a `branch` if the
   change should not touch that project's working tree.

One child per target project; reuse it for related edits there. Give a
complete, self-contained intent (task, files, verification command) since the
child does not share parent context. Report which project you delegated to.

This does not apply to reading files elsewhere (read directly), files inside
the current project, or when the user explicitly asks for an inline edit.

## Tool Choice

Use `new_session` (default, shares the parent's working tree) when child only needs to research, review, or run read-only checks.

Use `new_session` with `worktree=true` and a `branch` when child may edit files, run formatters, stage changes, or otherwise interfere with parent work.

Pass `model` (a `"provider/model"` string) when the child should run on a different model than the parent default — e.g. a cheaper/faster model for review or a stronger model for hard implementation.

A worktree/child inherits the parent's "Allow always" permissions at split time when the `worktree.inherit_permissions` setting is on. When it does, `new_session` returns `permissionsInherited` (bool) and `permissionsInheritedCount` (int) — mention them in your summary so the user knows the child started with N inherited approvals (or none).

Use `get_session_status` for one child. Use `list_child_sessions` for all children from the current parent. Use `cancel_session` for stale or wrong child work.

Use `send_message_to_child` only for meaningful follow-up instructions. Ocman automatically delivers each finished child turn to the parent. Children should use `send_message_to_parent` only for mid-task findings or to ask for direction.

## Prompt Shape

Child prompt should be short and complete:

- State exact task.
- Say whether edits are allowed.
- List expected output.
- Name verification command if known.
- Ask child to return concise findings, changed files, and test results.

Example:

```text
Review the MCP tool descriptions for token bloat. Do not edit files. Return concrete findings with file/line references and any suggested wording.
```

## Concurrency Rules

Keep child count low. Prefer one to three children. Merge results in parent before spawning more.

Use worktrees for parallel code changes. Same-directory children are best for exploration and verification.

Do not request full transcripts from children. Ask for summaries, file references, and command results.

## Token Discipline

Prefer MCP tool outputs that return IDs, statuses, paths, and short summaries. Fetch details only when needed.

Avoid generic session management. Do not ask for MCP help unless debugging tool availability.
