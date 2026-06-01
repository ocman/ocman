---
name: ocman-session-splitting
description: Use when splitting ocman/OpenCode work into child sessions, parallel sessions, or git worktrees via MCP.
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

## Tool Choice

Use `split_to_session` when child can share the current working tree and only needs to research, review, or run read-only checks.

Use `split_to_worktree` when child may edit files, run formatters, stage changes, or otherwise interfere with parent work.

Use `get_session_status` for one child. Use `list_child_sessions` for all children from the current parent. Use `cancel_session` for stale or wrong child work.

Use `send_message_to_child` only for meaningful follow-up instructions. Use `send_message_to_parent` from a child when reporting findings or asking for direction.

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
