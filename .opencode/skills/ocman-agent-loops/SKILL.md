---
name: ocman-agent-loops
description: Use when creating or controlling ocman agent loops — self-driving orchestrations that re-prompt agents on a trigger until a stop condition is met.
---

# Ocman Agent Loops

A loop fires an **action** on a **trigger** until a **stop condition** trips.
Keep policy here; MCP tool descriptions stay short.

## When To Use A Loop

Use a loop when work is repetitive and self-driven:

- Address PR review comments as they arrive (`pr_event` + `prompt_*`).
- Periodic check-in / status heartbeat (`schedule` + `prompt_root`).
- Orchestrate a plan: spawn one child per step on completion
  (`child_complete` + `spawn_worktree`).
- Keep iterating on a session after each turn (`turn_complete`).

Do NOT loop for one-off tasks — use `split_to_session` / `split_to_worktree`.

## Mandatory Safety

Every loop **requires a budget**. `create_loop` rejects a loop without
`max_cost_usd` or `max_tokens`. Always set the tightest budget that fits:

- `max_iterations` — hard cap on cycles (default 25).
- `max_cost_usd` **or** `max_tokens` — required spend cap.
- `max_duration` — wall-clock cap (e.g. `"8h"`, default 8h).
- `error_streak` — stop after N consecutive failed actions (default 3).

Stop conditions are checked **before every action**, so an over-budget loop
never sends "one more" prompt. Prefer small budgets; raise them if needed.

## Triggers

| Trigger | Fires when | Config |
|---|---|---|
| `schedule` | interval elapsed (floor 60s) | `interval_seconds` |
| `pr_event` | PR head changes or merges | `pr_number` (+ `poll_seconds`) |
| `child_complete` | a spawned child finishes | — |
| `turn_complete` | the root session goes idle | — |

## Actions

| Action | Effect |
|---|---|
| `prompt_root` | re-prompt the anchoring session |
| `prompt_child` | prompt a specific child (`trigger_config.target_session_id`) |
| `spawn_child` | new session in the same directory |
| `spawn_worktree` | new isolated git worktree session |

## Template Placeholders

`action_template` supports: `{{iteration}}`, `{{project}}`, `{{directory}}`,
`{{last_summary}}`, `{{trigger}}`, `{{pr_number}}`.

## Tools

- `create_loop` — author a loop (budget required).
- `list_loops` — loops for a session.
- `get_loop_status` — state, iteration, budget consumed, last summary.
- `pause_loop` / `resume_loop` — suspend/continue a paused loop.
- `restart_loop` — revive a completed/errored loop: clears iteration count
  and consumed budget, sets it active to run again from zero against the
  same settings. (Resume is paused-only; restart is for terminal loops.)
- `step_loop` — run exactly one cycle, then pause (use to test a new loop).
- `delete_loop` — stop permanently and remove from the list.

## Workflow

1. Start a new loop with `step_loop` after `create_loop` to verify the
   trigger fires and the prompt is right before letting it run unattended.
2. Use the tightest budget that completes the goal.
3. Check `get_loop_status` rather than reading full transcripts.
4. `pause_loop` if a loop goes down a wrong path; `delete_loop` to remove it.
