# Agent Loops - Requirements

## Overview

A feature that lets ocman run **loops**: long-lived, self-driving orchestrations
that prompt agents repeatedly instead of requiring the user to prompt each step
by hand. Where the existing session-split MCP (`spec/session-split-mcp/`) spawns
a child session and injects a *one-shot* result back into the parent, agent
loops add the missing **continuation edge**: when a triggering condition fires,
ocman evaluates a loop policy and automatically sends the next prompt — to the
same session, or to a freshly-spawned child/worktree session.

This is the "design loops that prompt your agents" workflow popularized by
Theo (t3.gg) and Pete Koomen: the human writes the loop once (or asks the agent
to build it), then steps back. The agent runs the next step, verifies, commits,
files a PR, addresses review-bot comments, merges, and triggers the next stacked
PR — without the human copy-pasting between threads.

ocman is uniquely positioned for this because it is already the observability +
orchestration shell around OpenCode sessions: it has child-session tracking
(`child_sessions`, migration v9), a 5-second completion watcher
(`mcp_watcher.go`), headless permission auto-approval (`autoapprove_watcher.go`),
worktree-per-task isolation, and a PR/Issue sidebar wired to GitHub/Forgejo.
Loops compose these existing primitives rather than replacing them.

The feature ships **four loop patterns**, all built on one shared loop engine:

1. **PR-comment auto-address** — watch a PR; when review-bot or human comments
   land, prompt the implementer session to address them; re-trigger on each new
   PR head until approved/merged.
2. **Orchestrator with dynamic sub-loops** — a root loop plans stacked PRs and
   spawns per-PR `implement → review → re-review` child loops, merging each and
   triggering the next.
3. **Heartbeat / scheduled wake-up** — a loop wakes every N minutes, inspects
   state (child statuses, PRs, issues), and directs work to threads.
4. **Linear /goal-style loop** — one session that, on each completed turn, is
   re-prompted "are you done? if not, continue" until a goal predicate holds.

## Goals

- Let a loop run **unattended** across many agent turns while remaining fully
  **observable and interruptible** from the ocman UI.
- Reuse the existing child-session + worktree + watcher machinery; add a thin
  loop-orchestration layer, not a parallel system.
- Make loops **safe by default**: every loop has hard stop conditions
  (max iterations, cost/token budget, wall-clock deadline) and a one-click
  Stop/Pause that the video explicitly identifies as the missing safety net.
- Allow **the agent itself** to define a loop mid-session (matching the video's
  "I asked the model to make a loop and it made one"), via new MCP tools.
- Allow **the user** to define a loop with no code: from the command palette,
  the Loops view, or a "Watch this PR" action in the existing PR sidebar.
- Surface **cost** prominently (the video's headline caveat: loops burn many
  more tokens, and a wrong path burns them for longer).

## Non-Goals

- Fully autonomous, zero-oversight loops on production codebases. The video
  itself warns against this; ocman's design keeps a human in the stop-loop.
- A general-purpose workflow engine / DAG editor. Loops are a small set of
  policy-driven patterns, not arbitrary user-authored graphs.
- Replacing OpenCode's own `/goal` primitive. The linear-loop pattern is an
  ocman-side equivalent for platforms/sessions that lack it, and an
  observability wrapper for those that have it.

## Target Users

The single-user ocman maintainer running multiple concurrent OpenCode sessions,
who wants to (a) offload multi-stage work to self-driving loops and (b) keep a
live, interruptible view of what those loops are doing and what they cost.

Loops are consumed by:

1. **AI coding agents** (OpenCode) — the model calls loop MCP tools mid-session
   to create/inspect/stop loops it is orchestrating.
2. **Human users** — via the Loops view, the command palette (`/loop`), and the
   PR/Issue sidebar.

## Functional Requirements

### FR-1: Loop object

- **Description**: A loop is a first-class persisted object tying a root session
  to a **trigger**, an **action**, and **stop conditions**. It has a lifecycle
  the user and agent can observe and control.
- **Acceptance Criteria**:
  - A loop has: `id`, `platform`, `root_session_id`, `directory`, `project_name`,
    `pattern` (one of the four), `trigger`, `action`, `action_template`,
    `current_task`, `stop_conditions`, `state` (`active`, `paused`, `stopped`,
    `completed`, `failed`), `iteration` counter, audit timestamps, and aggregate
    counters (iterations run, tokens/cost consumed, child sessions spawned).
  - A loop has a short `title` and optional `description` for UI display. The
    title defaults from the pattern + target (e.g. `Watch PR #42`).
  - A loop persists in `state.db` and survives an ocman restart: an `active`
    loop resumes being driven after restart.
  - A loop references zero or more `child_sessions` it has spawned (reusing the
    existing table; see Data Requirements).

### FR-2: Trigger types

- **Description**: A loop advances when its trigger fires. Triggers are the
  generalization of the existing one-shot child-completion watcher.
- **Trigger types** (v1):
  1. **`child_complete`** — fires when a tracked child session reaches a
     terminal state (`completed`/`error`). This is the existing watcher edge,
     promoted from "report once" to "evaluate loop policy".
  2. **`schedule`** — fires on a fixed interval (`interval_seconds`, min 60s).
     Powers the heartbeat pattern.
  3. **`pr_event`** — fires when a watched PR changes: new head SHA, new
     review comments, review state change (approved/changes-requested), or
     merge. Powers the PR-comment auto-address pattern. Built on the existing
     forge clients (GitHub/Forgejo) from the PR/Issue sidebar.
  4. **`turn_complete`** — fires when the root session finishes a turn (reaches
     `waiting`/`done`). Powers the linear /goal-style pattern.
- **Acceptance Criteria**:
  - Each trigger type is evaluated by the loop engine without blocking other
    loops.
  - Each trigger exposes a human-readable label, next-check time, and last-fired
    detail for the UI (e.g. `Every 10m`, `PR #42 comments`, `Root turn done`).
  - `schedule` triggers respect a minimum interval (default floor 60s) to bound
    token burn.
  - `pr_event` polling reuses the existing forge auth (env-var tokens with
    `gh`/`tea` fallback) and detection logic; it does not introduce new auth.
  - `pr_event` stores stable detection state (head SHA, seen comment/review IDs,
    and merge state), not only counts, so edited/resolved comments do not cause
    missed or repeated work.
  - A trigger that cannot be evaluated (e.g. forge unreachable, session gone)
    is retried with backoff and never silently kills the loop.

### FR-3: Action and continuation

- **Description**: When a trigger fires and stop conditions are not met, the
  loop performs an **action**: it sends a prompt (rendered from a template) to
  a session, or spawns a new child/worktree session.
- **Action types** (v1):
  1. **`prompt_root`** — send the rendered prompt to the loop's root session
     (linear pattern).
  2. **`prompt_child`** — send the rendered prompt to a specific tracked child
     session (PR-comment pattern: re-prompt the implementer).
  3. **`spawn_child`** — spawn a new same-dir child via the existing
     `split_to_session` path (review/verify sub-task).
  4. **`spawn_worktree`** — spawn a new isolated worktree child via the existing
     `split_to_worktree` path (next stacked PR).
- **Acceptance Criteria**:
  - The action template is rendered with loop context (iteration number, last
    summary, project/directory, PR number/comments, child statuses) before
    sending.
  - The rendered prompt is recorded per iteration for auditability (see FR-9).
  - `spawn_*` actions reuse the existing launcher; the new child is linked to
    the loop (`loop_id`) and to the loop's root session (`parent_session_id`).
  - Sub-loops are supported: a `spawn_*` action may itself create a child loop
    (orchestrator pattern). The data model must represent a loop hierarchy
    (`parent_loop_id`).
  - Action dispatch is idempotent across ocman restarts: once an action is about
    to send a prompt or spawn a child, the iteration is durably marked so the
    same action is not repeated after a crash.

### FR-4: Stop conditions (safety)

- **Description**: Every loop has mandatory, enforced stop conditions. A loop
  cannot be created without them; the engine never runs a loop past any.
- **Stop conditions** (all enforced; defaults set conservatively):
  1. **`max_iterations`** — hard cap on trigger→action cycles (default 25).
  2. **`max_cost_usd`** / **`max_tokens`** — aggregate budget across the loop
     and all its descendant child sessions (default budget required; no
     "unlimited" in v1).
  3. **`deadline`** / **`max_duration`** — wall-clock cutoff (default 8h).
  4. **`goal_predicate`** — optional success condition (e.g. "PR merged",
     "all stacked PRs merged", a model-evaluated "is the goal met?"). When it
     holds, the loop transitions to `completed`.
  5. **`error_streak`** — consecutive failed iterations cutoff (default 3),
     to stop a loop stuck going down a wrong path (the video's explicit
     failure mode).
- **Acceptance Criteria**:
  - Creating a loop without at least the budget and iteration caps is rejected.
  - When any stop condition is hit, the loop transitions to a terminal state
    (`completed` for `goal_predicate`, `stopped`/`failed` otherwise) and a
    summary is injected into the root session.
  - Stop-condition evaluation happens **before** every action, so an
    over-budget loop never sends one more prompt.

### FR-5: Manual control (Stop / Pause / Step)

- **Description**: The user can interrupt any loop at any time.
- **Acceptance Criteria**:
  - **Stop** transitions the loop to `stopped`, halts all future triggers, and
    optionally cancels in-flight child sessions (user choice).
  - **Pause** suspends trigger evaluation without losing loop state; **Resume**
    continues from the same iteration counter.
  - **Step once** runs exactly one trigger→action cycle then returns to
    `paused` (for cautious supervision).
  - Controls are exposed both via the UI (FR-7) and via MCP tools (FR-6).
  - Stopping/pausing takes effect within one engine tick (≤ engine interval).

### FR-6: MCP tools

- **Description**: New MCP tools let the agent define and control loops itself,
  extending the existing `internal/mcp/` tool set.
- **Tools** (v1):
  - `create_loop` — create a loop (pattern, trigger, action template, stop
    conditions). Returns `loop_id` and `state`.
  - `list_loops` — list loops for a root session or directory, with live
    counters.
  - `get_loop_status` — detailed status of one loop (state, iteration, budget
    consumed, child tree, last iteration summary).
  - `stop_loop` / `pause_loop` / `resume_loop` / `step_loop` — lifecycle
    control.
- **Acceptance Criteria**:
  - Tool descriptions stay short; loop policy/guidance lives in a repo-local
    skill (consistent with the existing `ocman-session-splitting` skill).
  - `create_loop` validates stop conditions (FR-4) and rejects unsafe configs.
  - The tools are localhost-only (same boundary as existing MCP tools).
  - Existing split/status/comm MCP tools are unchanged and continue to work.

### FR-7: Loops UI

- **Description**: A dashboard view that makes running loops observable and
  controllable — the differentiator over raw Codex/Claude Code.
- **Acceptance Criteria**:
  - A per-project **Loops view** (`/project/<dir>/loops`) lists active and
    recent loops as cards.
  - Each card shows: pattern, state, iteration counter, **cost/tokens consumed
    vs budget** (prominent), project name/directory, trigger label, next trigger
    check/fire time, elapsed vs deadline, the tree of child sessions it spawned
    (with their statuses), and the last iteration summary.
  - Each card has **Stop / Pause-Resume / Step** controls (FR-5).
  - A **cost guardrail banner** appears when a loop crosses a configurable
    fraction of its budget (e.g. 80%).
  - An **activity timeline** per loop ("woke up → checked PR #42 → filed
    review thread → addressed 3 comments → re-review").
  - Loop state updates stream live (reuse the existing SSE/global-events
    infrastructure; no busy polling in the browser).
  - All UI affordances are **capability-gated** via `/api/capabilities`
    (no `platform === '...'` branching; `make lint` enforces this).

### FR-7a: Workflow visualization

- **Description**: Users need to see not only "a loop is running", but the
  workflow it is driving: project, trigger, current task, spawned sessions, and
  what will happen next.
- **Acceptance Criteria**:
  - A **workflow map** is available per loop, rendered as a small directed graph
    or timeline: trigger -> action -> target session/worktree -> outcome -> next
    trigger.
  - The map shows nested sub-loops as expandable nodes, not as a separate page.
  - Nodes include stable labels: project/repo, branch/worktree when known,
    trigger type, action type, session status, PR/issue number, and budget state.
  - The per-project Loops view groups loops by `project_name` / `directory`, with
    active loops first and terminal loops collapsed under "Recent".
  - A global Loops view lists loops across projects with filters for project,
    state, pattern, trigger type, and budget warning state.
  - The timeline and graph use the same `loop_iterations` data so they cannot
    disagree. The graph may be approximate in v1; the audit timeline is source
    of truth.
  - Clicking a workflow node opens the existing session, worktree, PR/issue, or
    loop detail page when available.

### FR-8: Entry points

- **Description**: Low-friction ways to start a loop.
- **Acceptance Criteria**:
  - **PR/Issue sidebar**: a "Watch this PR (loop)" action on a PR row creates a
    PR-comment auto-address loop pre-filled with that PR number and the source
    session.
  - **Command palette**: a `/loop` command opens a create-loop flow (mirroring
    the existing `/wt` worktree command).
  - **Loops view**: a "New loop" button with pattern presets.
  - **Agent**: `create_loop` MCP tool (FR-6).

### FR-9: Auditability

- **Description**: Every loop iteration is recorded so the user can understand
  retroactively what a long-running loop did overnight (the video: "this thread
  is so long I can't even scroll up to my first prompt").
- **Acceptance Criteria**:
  - Each iteration records: timestamp, trigger that fired, rendered prompt,
    target session, resulting child session id (if any), outcome status, and
    a short summary.
  - Iteration history is queryable per loop and rendered in the activity
    timeline (FR-7).
  - Records are retained for stopped/completed loops (subject to a retention
    policy / cleanup; see Open Questions).

## Non-Functional Requirements

### NFR-1: Localhost-only control surface

Loop MCP tools and loop control endpoints accept localhost connections only,
consistent with existing tmux/worktree/MCP endpoints. Non-localhost requests
return 403.

### NFR-2: No blocking; bounded concurrency

- Loop tool calls and control endpoints return within a few seconds.
- The loop engine runs loops concurrently without one slow loop (e.g. a forge
  call) starving others.
- A global cap on concurrently *active* loops (default small, e.g. 10) bounds
  resource use; creating beyond the cap is rejected with a clear error.

### NFR-3: Crash safety

- Loop state is persisted such that an ocman restart resumes `active` loops
  and does not double-fire a trigger or double-send an action for an iteration
  already recorded (idempotent iteration advancement).
- A panic in one loop's tick must not kill the engine or other loops
  (`runWithRecover`, consistent with existing watchers).

### NFR-4: Cost transparency and enforcement

- Token/cost accounting aggregates the loop's own actions plus all descendant
  child sessions, surfaced live in the UI and enforced as a hard stop (FR-4).
- The accounting source is the same usage data the rate-limit-surfacing feature
  exposes (`spec/opencode-rate-limit-surfacing/`); loops must not invent a
  second, divergent accounting path.

### NFR-5: No regression

Adding loops must not affect existing functionality. All existing Go and
frontend tests continue to pass; CI remains green. The existing one-shot
child-session watcher behavior is preserved for child sessions not attached to
any loop.

### NFR-6: Capability gating

A new capability flag (e.g. `agentLoops`) is exposed on `/api/capabilities`;
the frontend gates all loop UI on it. `make lint` (including
`scripts/check-platform-branching.sh`) continues to pass.

## Data Requirements

### New `state.db` tables (migration v15)

> The current latest migration is v14 (multi-remote); loops add v15.

**`loops`** — one row per loop.

| Column | Type | Notes |
|---|---|---|
| `id` | TEXT PK | loop ID |
| `platform` | TEXT NOT NULL | e.g. `"opencode"` |
| `root_session_id` | TEXT NOT NULL | session the loop reports back to |
| `parent_loop_id` | TEXT | null for top-level loops; set for sub-loops |
| `directory` | TEXT NOT NULL | working directory / repo root |
| `project_name` | TEXT | display name resolved from the project directory |
| `title` | TEXT NOT NULL | short UI label |
| `description` | TEXT | optional human-authored context |
| `current_task` | TEXT | short label for what the loop is doing now |
| `pattern` | TEXT NOT NULL | `pr_address`, `orchestrator`, `heartbeat`, `linear` |
| `trigger_type` | TEXT NOT NULL | `child_complete`, `schedule`, `pr_event`, `turn_complete` |
| `trigger_config` | TEXT (JSON) | e.g. `{ "interval_seconds": 300 }`, `{ "pr_number": 42 }` |
| `action_type` | TEXT NOT NULL | `prompt_root`, `prompt_child`, `spawn_child`, `spawn_worktree` |
| `action_template` | TEXT NOT NULL | prompt template |
| `stop_conditions` | TEXT (JSON) | `{ max_iterations, max_cost_usd, max_tokens, deadline, error_streak, goal_predicate }` |
| `state` | TEXT NOT NULL | `active`, `paused`, `stopped`, `completed`, `failed` |
| `iteration` | INTEGER NOT NULL | cycles completed |
| `error_streak` | INTEGER NOT NULL | consecutive failures |
| `tokens_used` | INTEGER NOT NULL | aggregate (loop + descendants) |
| `cost_usd` | REAL NOT NULL | aggregate (loop + descendants) |
| `created_at` | INTEGER NOT NULL | Unix ms |
| `updated_at` | INTEGER NOT NULL | Unix ms |
| `completed_at` | INTEGER | null until terminal |
| `last_summary` | TEXT | summary of the most recent iteration |

Indexes: `(state)` for the engine's "find active loops" scan;
`(root_session_id)` and `(directory)` for listing.

**`loop_iterations`** — one row per trigger→action cycle (audit trail, FR-9).

| Column | Type | Notes |
|---|---|---|
| `id` | INTEGER PK AUTOINCREMENT | |
| `loop_id` | TEXT NOT NULL | FK → loops.id |
| `seq` | INTEGER NOT NULL | iteration number within the loop |
| `fired_at` | INTEGER NOT NULL | Unix ms |
| `trigger_detail` | TEXT | what fired (e.g. "new comment on PR #42") |
| `rendered_prompt` | TEXT | prompt actually sent |
| `target_session_id` | TEXT | session the action targeted |
| `child_session_id` | TEXT | child spawned this iteration (if any) |
| `outcome` | TEXT | `pending`, `ok`, `error`, `skipped` |
| `summary` | TEXT | short result summary |
| `started_at` | INTEGER | set before a prompt/spawn side effect starts |
| `completed_at` | INTEGER | set when the action result is known |

Index: `(loop_id, seq)`.

### Existing data touched

- **`child_sessions`** (migration v9): add a nullable `loop_id` column so a
  child spawned by a loop is linked to it. Children with `loop_id = NULL`
  behave exactly as today (one-shot report-back). Added via the v15 migration.
- **OpenCode read-only DB**: read for session status inference and usage/cost
  accounting (unchanged access pattern; read-only).
- **Forge (GitHub/Forgejo)**: read PR head/comments/review state via existing
  sidebar clients for `pr_event` triggers.

## Integration Points

- **`internal/mcp/`** — new loop tools alongside the existing split/status/comm
  tools; reuse `SessionLauncher` and `PromptComposer`.
- **`internal/server/mcp_watcher.go`** — generalized so the `child_complete`
  edge can drive a loop (when the child has a `loop_id`) instead of only
  reporting once. Non-loop children keep current behavior.
- **`internal/server/` watchers** — a new loop engine goroutine started in
  `StartOnListener` next to `runChildSessionWatcher` and
  `runAutoApproveWatcher`, wrapped in `runWithRecover`.
- **PR/Issue sidebar (`spec/pr-issue-sidebar/`)** — reuse forge clients/auth
  for `pr_event`; add the "Watch this PR (loop)" action.
- **Rate-limit surfacing (`spec/opencode-rate-limit-surfacing/`)** — single
  source of truth for token/cost accounting.
- **SSE / global events (`internal/server/broadcast.go`)** — push live loop
  state to the Loops view.
- **Projects index** — resolve `directory` to `project_name` for grouping and
  display; the directory remains the stable key.
- **`internal/state/`** — `loops` + `loop_iterations` tables; `child_sessions`
  gains `loop_id`.
- **Settings (`setting` table, migration v12)** — store user defaults for stop
  conditions and the "watch PR" prompt template (consistent with the existing
  PR/Issue template settings).

## Constraints

- **Platform**: v1 supports OpenCode only; loops register only when the
  OpenCode adapter is active.
- **Host locality**: v1 drives only projects owned by the local ocman host.
  Remote projects from multi-remote are visible as context but cannot start loops
  until host-routed loop execution exists.
- **No exported Go**: all new code under `internal/`.
- **No platform branching in the frontend**: loop UI is capability-gated.
- **Read-only access to OpenCode's DB**: loops never write to it.
- **Mandatory budgets**: no unbounded loops in v1 (FR-4).
- **No double-send on restart**: action dispatch must be idempotent. Prefer a
  tiny DB-backed pending iteration over any in-memory-only guard.
- **Tooling**: requires `git`, `tmux`, `opencode` on PATH; forge triggers
  require a configured GitHub/Forgejo remote + token (same as the sidebar).

## Assumptions

- Re-prompting a session in `waiting`/`done` state via `Platform.SendMessage`
  continues the conversation (already true; this is the existing injection
  mechanism). *Architect to confirm for the linear pattern's re-prompt.*
- Per-session usage/cost is obtainable at the granularity loops need for budget
  enforcement. *Architect to confirm against the rate-limit-surfacing data.*
- Forge PR head/comment/review-state polling at a modest interval (e.g. 30–60s)
  is sufficient for the PR pattern; no webhook receiver is required in v1.
  *Architect to decide poll vs. webhook.*
- A child spawned by a loop should report its result to the loop (which decides
  what's next), not directly to the human, except via the loop's summary
  injections. *Architect to confirm the reporting topology for sub-loops.*

## Out of Scope

- Arbitrary user-authored DAG/workflow editor.
- Multi-user scoping / per-user loop ownership (ocman is single-user).
- Non-OpenCode platforms (Codex, etc.) — the data model should not preclude
  them, but adapters are future work.
- Webhook-based forge triggers (v1 polls; webhooks are a future enhancement).
- Automatic merging to protected/production branches without explicit
  per-loop opt-in (the video warns against unattended prod merges).
- Cross-machine loops (the video's "another machine on my network" setup);
  v1 loops run within one ocman process on one host.
- Automatic worktree cleanup when a loop completes (track for future work).
- LLM-authored loop *templates* generated by ocman; v1 lets the agent author
  loops via `create_loop`, but ocman supplies fixed pattern presets.

## Success Criteria

- A user clicks "Watch this PR" in the sidebar, walks away, and returns to find
  review-bot comments addressed and the PR re-pushed — with a visible activity
  timeline and a cost figure, and a Stop button that worked whenever pressed.
- An agent can call `create_loop` to build an orchestrator that files and merges
  a sequence of stacked PRs, each with its own review sub-loop, all visible as a
  loop tree in the Loops view.
- A loop never exceeds its configured iteration/cost/time budget, and a stuck
  loop self-terminates on an error streak.
- A heartbeat loop wakes on schedule and reports findings to its root session.
- No regression: existing one-shot child-session behavior and all CI checks
  remain green.

## Open Questions

1. **Engine model**: one shared engine goroutine ticking all loops, vs. one
   goroutine per active loop, vs. extending the existing watchers. Tick-based
   shared engine is simplest and matches existing watcher style; per-loop
   goroutines isolate slow forge calls better. *Architect to decide.*

2. **PR triggers: poll vs. webhook**. Polling reuses the sidebar clients with
   zero new infra; webhooks are timelier but need a receiver + per-forge setup.
   *Architect to decide for v1.*

3. **Cost accounting granularity**: can per-session token/cost be aggregated
   reliably across a loop's descendant tree from the rate-limit-surfacing data,
   or is an additional accounting hook needed? *Architect to confirm.*

4. **Goal predicate evaluation**: for `goal_predicate` like "is the goal met?",
   options are (a) a deterministic check (PR merged via forge API), (b) a model
   call judging completion, (c) a marker the agent emits. *Architect to define
   the allowed predicate kinds for v1.*

5. **Sub-loop reporting topology**: does a sub-loop report to its parent loop,
   to the root session, or both? Affects how summaries are injected and how the
   loop tree is rendered. *Architect to specify.*

6. **Re-prompt vs. fresh-session for linear loops**: re-prompting one ever-
   growing session hits context limits (the video's "can't scroll to my first
   prompt"); spawning fresh sessions loses continuity. *Architect to decide the
   default and whether it's configurable.*

7. **Iteration/audit retention**: `loop_iterations` could grow large for long
   loops. Define a retention/cleanup policy (cap rows per loop? prune on
   terminal state after N days?). *Architect to specify.*

8. **Interaction with auto-approve**: loops imply unattended runs, which imply
    headless permission auto-approval (`autoapprove_watcher.go`). Should a loop
    require/enable auto-approve for its sessions, and how is that scoped safely?
    *Architect to specify the relationship.*

9. **Workflow graph layout**: v1 can use a simple vertical flow/timeline with
   indentation for sub-loops. A node graph is nicer, but likely not worth a new
   dependency unless the timeline proves insufficient.
