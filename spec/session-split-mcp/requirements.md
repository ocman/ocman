# Session Split MCP - Requirements

## Overview

An MCP (Model Context Protocol) server embedded in ocman that enables AI coding
agents and human users to split work from an active session into new parallel
sessions or isolated git worktrees. The MCP server is exposed over HTTP/SSE as
part of the existing ocman process.

The core workflow: while working on a feature, the model (or user) notices a
separable concern — a bug fix, a refactor, a spike — and calls a split tool.
ocman composes a focused prompt enriched with context from the current session,
launches a new OpenCode session (optionally in a fresh worktree), and returns
the result through the waiting MCP call when that child session completes.

## Goals

- Allow AI agents to autonomously decompose work into parallel sessions without
  leaving the current conversation.
- Allow users to explicitly request a split via natural language ("split this
  into a new session").
- Compose rich, context-aware prompts for child sessions automatically, reducing
  the cognitive overhead of context-passing.
- Deliver child session results back to the parent session without requiring
  manual copy-paste.
- Lay the groundwork for more advanced orchestration patterns (fan-out, review,
  merge) in future iterations.

## Target Users

The primary user is the ocman maintainer running multiple concurrent OpenCode
sessions. The MCP is consumed by:

1. **AI coding agents** (OpenCode) — the model calls MCP tools autonomously
   mid-session when it decides to split work.
2. **Human users** — the user types a natural-language instruction in the chat
   ("split the linting fix into a separate session") and the model calls the MCP
   tool on their behalf.

## Functional Requirements

### FR-1: MCP server embedded in ocman

- **Description**: ocman exposes an MCP server on its existing HTTP port under
  a dedicated path (e.g. `/mcp`). The server uses the HTTP/SSE transport as
  defined by the MCP specification. It is registered at startup alongside the
  existing API routes.
- **Acceptance Criteria**:
  - The MCP server is reachable at `http://localhost:<port>/mcp`.
  - It advertises the tools defined in FR-2 through FR-6 via the MCP
    `tools/list` method.
  - The server starts and stops with the ocman process — no separate binary or
    process is required.
  - A new `-mcp` flag (or equivalent) can disable the MCP server if needed;
    it is **enabled by default**.
  - The MCP endpoint is **localhost-only** (consistent with tmux and worktree
    endpoints).

### FR-2: `split_to_session` tool

- **Description**: Launch a new OpenCode session in the same working directory
  as the parent session, with a composed prompt derived from the caller's intent
  and automatically enriched context.
- **Inputs**:
  - `session_id` (required) — the parent session ID, used to fetch context.
  - `intent` (required) — a brief description of the sub-task (provided by the
    caller).
  - `context_options` (optional) — object controlling which context is injected
    (see FR-7). Defaults to all context enabled.
- **Outputs**:
  - `child_session_id` — the ID of the newly created session.
  - `status` — initial status (`starting`).
- **Acceptance Criteria**:
  - A new OpenCode session is launched in the same `cwd` as the parent session.
  - The composed prompt is passed to the new session at launch.
  - The parent–child relationship is persisted in `state.db` (see Data
    Requirements).
  - The tool returns within a few seconds; session launch is non-blocking.

### FR-3: `split_to_worktree` tool

- **Description**: Launch a new OpenCode session in a fresh git worktree,
  isolated from the parent session's working tree. Reuses the existing worktree
  creation logic from the worktree-sessions feature.
- **Inputs**:
  - `session_id` (required) — the parent session ID.
  - `intent` (required) — brief description of the sub-task.
  - `branch` (required) — branch name for the new worktree.
  - `base_ref` (optional) — base ref for the new branch; defaults to the repo's
    default branch.
  - `context_options` (optional) — controls injected context (see FR-7).
- **Outputs**:
  - `child_session_id` — ID of the new session.
  - `worktree_path` — on-disk path of the created worktree.
  - `branch` — resolved branch name.
  - `status` — initial status (`starting`).
- **Acceptance Criteria**:
  - A new git worktree is created under
    `<repo-parent>/.worktrees/<repo-name>/<slug>/` (same convention as the
    existing worktree-sessions feature).
  - A new OpenCode session is launched inside that worktree via the existing
    tmux launcher.
  - The parent–child relationship is persisted in `state.db`.
  - If the worktree already exists for the given branch, it is reused
    (idempotent, consistent with FR-4 of the worktree-sessions spec).

### FR-4: `get_session_status` tool

- **Description**: Return the current status of a session previously spawned
  via `split_to_session` or `split_to_worktree`.
- **Inputs**:
  - `child_session_id` (required).
- **Outputs**:
  - `status` — one of `starting`, `running`, `completed`, `error`, `cancelled`.
  - `summary` — brief summary of what the session did (populated when
    `completed`; derived from the session's last assistant message).
  - `error` — error message if `status` is `error`.
- **Acceptance Criteria**:
  - Status is inferred from the child session's last message (same logic as
    existing session status inference in `internal/db/`).
  - `summary` is non-empty when status is `completed`.

### FR-5: `list_child_sessions` tool

- **Description**: List all sessions spawned from a given parent session,
  with their current status.
- **Inputs**:
  - `session_id` (required) — the parent session ID.
- **Outputs**:
  - Array of objects, each containing: `child_session_id`, `intent`,
    `status`, `created_at`, `worktree_path` (if applicable).
- **Acceptance Criteria**:
  - Returns all children regardless of status (including cancelled/completed).
  - Results are ordered by `created_at` descending.

### FR-6: `cancel_session` tool

- **Description**: Cancel a running child session. Sends a termination signal
  to the underlying OpenCode process.
- **Inputs**:
  - `child_session_id` (required).
- **Outputs**:
  - `success` — boolean.
  - `message` — human-readable confirmation or error.
- **Acceptance Criteria**:
  - The child session's OpenCode process is terminated (via tmux or process
    signal).
  - The session's status in `state.db` is updated to `cancelled`.
  - Cancelling an already-completed or already-cancelled session returns
    `success: true` (idempotent).
  - Cancelling a session that does not belong to the caller's parent session
    returns an error.

### FR-7: Automatic prompt enrichment

- **Description**: When composing the prompt for a child session, the MCP
  enriches the caller-provided `intent` with context automatically extracted
  from the parent session and its repository state.
- **Context sources** (all enabled by default, each individually toggleable via
  `context_options`):
  1. **`recent_messages`** — the last N messages from the parent session's
     conversation history (default N=10, configurable).
  2. **`relevant_files`** — file paths mentioned or edited in the parent
     session's recent messages.
  3. **`git_branch`** — the current branch of the parent session's working
     directory.
  4. **`git_diff_stat`** — output of `git diff --stat` in the parent session's
     working directory (summary of uncommitted changes).
  5. **`project_metadata`** — repo name, working directory path.
- **Acceptance Criteria**:
  - The composed prompt is a structured, human-readable text that a fresh
    OpenCode session can act on without additional context.
  - Each context source can be individually disabled by setting its key to
    `false` in `context_options`.
  - If a context source is unavailable (e.g. no git repo, no messages), it is
    silently omitted rather than causing an error.
  - The composed prompt is stored alongside the child session record in
    `state.db` for auditability.

### FR-8: Child result delivery

- **Description**: When a child session completes (status transitions to
  `completed` or `error`), ocman returns the result from the waiting split tool.
- **Acceptance Criteria**:
  - The split tool result includes the child session's terminal status and final
    assistant text.
  - Delivery happens without user intervention.
  - If the MCP caller disconnects, it can reconnect to the original wait
    without sending another child prompt, including after an ocman restart.
  - Ocman queues a reconnect reminder for the parent and defers it until the
    parent's active turn is idle.

## Non-Functional Requirements

### NFR-1: Localhost-only

- **Description**: The MCP endpoint must only accept connections from localhost,
  consistent with the existing tmux and worktree endpoints.
- **Acceptance Criteria**: Requests from non-localhost origins return 403.

### NFR-2: No blocking the parent session

- **Description**: All MCP tool calls must return quickly. Session launch,
  worktree creation, and prompt composition must not block the caller for more
  than a few seconds.
- **Acceptance Criteria**:
  - `split_to_session` and `split_to_worktree` return within 5 seconds under
    normal conditions.
  - Long-running operations (worktree creation, OpenCode launch) happen
    asynchronously after the tool call returns.

### NFR-3: Idempotency

- **Description**: Calling `split_to_worktree` with the same parent session,
  intent, and branch twice must not create duplicate worktrees or sessions.
- **Acceptance Criteria**: Second call reuses the existing worktree and session,
  returning the same `child_session_id`.

### NFR-4: No regression

- **Description**: Adding the MCP server must not affect existing ocman
  functionality.
- **Acceptance Criteria**: All existing Go and frontend tests continue to pass;
  CI remains green.

### NFR-5: Capability gating

- **Description**: The MCP server's availability is reflected in
  `/api/capabilities` so the frontend can surface relevant affordances without
  platform branching.
- **Acceptance Criteria**:
  - A new capability flag (e.g. `mcp_server`) is exposed on
    `/api/capabilities`.
  - `make lint` continues to pass (no `platform === 'opencode'` branching
    introduced).

## Data Requirements

### New `state.db` tables

**`child_sessions`**

Tracks the parent–child relationship between sessions and stores the composed
prompt and status.

| Column | Type | Notes |
|---|---|---|
| `id` | TEXT PK | child session ID |
| `platform` | TEXT | platform of both parent and child |
| `parent_session_id` | TEXT | FK → sessions |
| `intent` | TEXT | caller-provided intent string |
| `composed_prompt` | TEXT | full enriched prompt sent to child |
| `worktree_path` | TEXT | null if not a worktree split |
| `branch` | TEXT | null if not a worktree split |
| `status` | TEXT | `starting`, `running`, `completed`, `error`, `cancelled` |
| `created_at` | DATETIME | |
| `completed_at` | DATETIME | null until terminal state |
| `summary` | TEXT | populated on completion |

### Existing data touched

- **OpenCode's read-only DB** (`session`, `message`, `part` tables) — read
  during prompt enrichment to extract recent messages and referenced files.
- **`state.db`** — new `child_sessions` table added via versioned migration.

## Integration Points

- **OpenCode HTTP API** — used to launch new sessions and (if supported) inject
  messages into existing sessions. Exact endpoints TBD (see Open Questions).
- **OpenCode SQLite DB** — read-only access to `session`, `message`, `part`
  tables for context extraction.
- **`git` CLI** — `git diff --stat`, `git branch --show-current`,
  `git symbolic-ref` for context enrichment and worktree creation.
- **`tmux` CLI** — launching child sessions via the existing
  `launchOpencodeInTmux` helper.
- **Existing ocman internals**:
  - `internal/server/tmux.go` — reused for session launch.
  - `internal/platforms/opencode/` — reused for session status inference.
  - `internal/db/` — reused for reading OpenCode's session/message data.
  - `internal/state/` — extended with `child_sessions` table.
  - `internal/server/` — new MCP handler registered on the existing mux.

## Constraints

- **Platform**: v1 supports OpenCode only. The MCP server is registered only
  when the OpenCode adapter is active.
- **Transport**: HTTP/SSE (MCP spec). stdio transport is out of scope for v1.
- **Tooling**: requires `git`, `tmux`, and `opencode` on PATH (same as existing
  worktree-sessions feature).
- **No exported Go**: all new code lives under `internal/` per repo convention.
- **No platform branching in the frontend**: any UI affordances must be
  capability-gated; `make lint` enforces this.
- **Read-only access to OpenCode's DB**: ocman must not write to OpenCode's
  SQLite database.

## Assumptions

- OpenCode can be launched with a pre-supplied prompt (e.g. via a CLI flag or
  stdin). *Architect to confirm the exact mechanism.*
- The MCP HTTP/SSE transport is compatible with how OpenCode discovers and
  connects to MCP servers. *Architect to confirm OpenCode's MCP client
  configuration.*
- A child session launched in the same `cwd` as the parent will appear as a
  distinct session in OpenCode's DB (different `session_id`). *Architect to
  confirm.*
- `git diff --stat` in the parent session's `cwd` is a sufficient proxy for
  "uncommitted changes summary". *Architect may prefer `git diff --name-only`
  or a richer format.*
- The last 10 messages from the parent session are a reasonable default for
  context injection. *Architect to tune based on token budget considerations.*
- Cancelling a session via tmux kill is sufficient; no OpenCode-level graceful
  shutdown is needed. *Architect to confirm.*

## Out of Scope

- **`fan_out` tool** (spawn N parallel sessions for the same task) — planned
  future feature. The data model should accommodate it (multiple children per
  parent), but the tool itself is not in v1.
- **`await_all`** — block until all child sessions of a parent complete and
  aggregate their results. Future feature (depends on `fan_out` and the
  callback injection mechanism from FR-8).
- **`merge_session`** — pulling a child session's diff back into the parent
  branch. Future feature.
- **`handoff_session`** — transferring context to a fresh session when the
  context window is full. Future feature.
- **`summarize_session` / `recall_context`** — session memory and retrieval.
  Future feature.
- **`request_review`** — spawning a dedicated reviewer session. Future feature.
- **`diff_sessions` / `elect_winner`** — comparing or selecting between
  parallel sessions. Future feature (depends on `fan_out`).
- **`broadcast_context`** — pushing context updates to all active children.
  Future feature (depends on `fan_out`).
- **stdio MCP transport** — v1 uses HTTP/SSE only.
- **Multi-user scenarios** — ocman is single-user; no auth scoping of child
  sessions per user.
- **Frontend UI for orchestration** — no new ocman dashboard views in v1 beyond
  what already exists for sessions and worktrees. The MCP is the interface.
- **Automatic worktree cleanup** when a child session completes or is cancelled.

## Success Criteria

- An AI agent can call `split_to_session` or `split_to_worktree` mid-session
  and the child session starts with a coherent, context-rich prompt — without
  the user having to manually copy-paste context.
- The parent session receives an automatic notification when the child session
  completes.
- The user can ask "split this linting fix into a new session" in natural
  language and the model handles it end-to-end via the MCP tools.
- No regressions in existing ocman functionality (CI green).

## Open Questions

1. **Child session launch mechanism**: Does OpenCode support receiving an
   initial prompt via a CLI flag (e.g. `opencode --prompt "..."`) or stdin?
   This determines how `split_to_session` and `split_to_worktree` pass the
   composed prompt to the new session. *Architect to investigate OpenCode's
   CLI interface.*

2. **Callback injection mechanism**: How does ocman inject a result message
   into a running parent session? Options include: (a) POST to OpenCode's HTTP
   API if it supports message injection, (b) `tmux send-keys` to paste text
   into the session's pane, (c) a shared file/pipe the parent session polls,
   (d) a webhook that OpenCode fires on session end. *Architect to investigate
   OpenCode's API and determine feasibility.*

3. **MCP client configuration in OpenCode**: How does a running OpenCode
   session discover and connect to ocman's MCP server? Does it require a
   config file entry, a CLI flag, or is it auto-discovered? *Architect to
   confirm the OpenCode MCP client setup.*

4. **Session status polling**: Should ocman poll OpenCode's DB to detect when
   a child session transitions to `completed`, or can it hook into an event
   (SSE, webhook)? The existing auto-archive loop polls every 24 h — a
   shorter polling interval (e.g. every 5 s) may be needed for timely
   callbacks. *Architect to decide polling strategy.*

5. **Token budget for context enrichment**: The composed prompt may become
   very large if recent messages + diff are included. Should there be a
   configurable token/character cap? *Architect to define limits.*

6. **Security of the MCP endpoint**: The MCP server is localhost-only, but
   should individual tool calls be further scoped to the session that spawned
   them (e.g. only the parent session can cancel its own children)? *Architect
   to decide on session-level authorization.*
