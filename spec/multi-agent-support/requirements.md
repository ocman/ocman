# Multi-Agent Support - Requirements

## Terminology

This document uses two distinct "agent" concepts that are easy to
confuse. The implementation resolves the ambiguity by using separate
terms; the text below is kept close to its original wording but the
reader should map as follows:

- **Platform** (implementation term: `platforms.Platform`, `Session.Platform`,
  `?platform=<id>`): the coding-agent tool that produces a session —
  OpenCode, Claude Code, Codex, Gemini, ... Every session in ocman
  belongs to exactly one platform. When this document says "agent
  adapter", "agent-agnostic dashboard", "agent identifier", or "adding
  a third agent", it means **platform**.
- **Agent** (implementation term: `MessageData.Agent`, OpenCode `/agent`
  catalog, `AgentPicker`): the composer-level role within a single
  session — OpenCode's "build", "plan", subagent names. A platform may
  or may not surface this concept (OpenCode does; Claude Code does
  not). When this document says "agent mode", "composer agent", "agent
  catalog", or talks about OpenCode's own agent types, it means
  **agent** in this narrower sense.

The Go code uses `platforms.Platform` to describe the broader concept
and preserves `Agent` for the narrower one, so there is no overloaded
identifier in the source tree.

## Overview

Today, ocman is a dashboard for OpenCode session data: it reads OpenCode's SQLite
database, proxies OpenCode's live HTTP API, and offers features (metrics,
archiving, tmux integration, whisper, composer) that are all coupled to
OpenCode's data model and runtime surface.

This feature re-positions ocman as an **agent-agnostic dashboard for AI coding
assistants**. Users who work with multiple agents should be able to see all of
their sessions in one place, and users who don't use OpenCode at all should
still be able to adopt ocman.

The first agent added alongside OpenCode is **Claude Code** (Anthropic's
`claude` CLI). The architecture must be designed so additional agents (Codex,
Gemini, Aider, ...) can be added later without core changes.

## Goals

1. Let users browse, manage, and interact with sessions from multiple coding
   agents in a single dashboard.
2. Achieve the closest feasible feature parity between supported agents via a
   well-defined adapter layer, degrading gracefully where an agent lacks a
   capability.
3. Enable adoption by users who do not use OpenCode at all.
4. Keep the core ocman experience (archiving, tmux integration, whisper,
   composer, status tracking) available for every supported agent from v1.
5. Establish an agent adapter abstraction that is cheap to extend for future
   agents.

## Target Users

- **Multi-agent developers**: use OpenCode for some work and Claude Code for
  other work; want one dashboard instead of switching context.
- **Claude-Code-only developers**: don't use OpenCode at all; want ocman's
  session browser, archiving, tmux integration, and composer on top of
  Claude Code.
- **OpenCode-only developers**: existing audience; the multi-agent work must
  not regress their experience.

## Functional Requirements

### FR-1: Agent adapter abstraction

- **Description:** Introduce an `Agent` adapter interface in the Go backend
  that encapsulates everything ocman needs from a specific agent. All
  agent-specific code lives behind this interface; no `switch on agent type`
  logic leaks into handlers or the frontend domain logic.
- **Responsibilities of an adapter (minimum):**
  - Discovery: detect whether this agent is installed / has any data on disk.
  - Session listing: enumerate all sessions (with enough metadata to populate
    `Session`).
  - Session detail: fetch messages and parts for a given session.
  - Status inference: compute `busy / waiting / done / error` for a session.
  - Liveness: detect whether a session is currently being written to by a
    running agent process, and whether a tmux pane hosts it.
  - Composer: send a new user message to a session.
  - Live-event ingestion (optional per agent): accept out-of-band status
    events (e.g. hooks) and update in-memory session state.
- **Acceptance Criteria:**
  - OpenCode and Claude Code are implemented as two adapters satisfying the
    same interface.
  - Adding a third agent requires only a new adapter implementation plus a
    registration; no API handler or frontend component needs modification for
    basic features.
  - All HTTP API handlers delegate to an adapter resolved from the session's
    agent attribute.

### FR-2: Session model carries an agent identifier

- **Description:** Every session in ocman is tagged with the agent that
  produced it (e.g. `"opencode"`, `"claude-code"`). The `Session` type, API
  payloads, and frontend session views are extended with an `agent` field.
- **Acceptance Criteria:**
  - `Session.Agent` is a required, non-empty string on every session returned
    by the API.
  - The frontend displays the agent for each session in list views (e.g.
    badge or icon) and detail views.
  - Session IDs are namespaced such that two agents using colliding IDs
    cannot clash in ocman's state (see FR-11 and FR-12).

### FR-3: Claude Code session discovery

- **Description:** Ocman auto-discovers Claude Code installations by scanning
  `~/.claude/projects/` for encoded-cwd directories containing `<uuid>.jsonl`
  session files. No configuration is required.
- **Acceptance Criteria:**
  - On startup and on demand, ocman enumerates all `.jsonl` files under
    `~/.claude/projects/<encoded-cwd>/` (top level only; sub-agent
    transcripts under `<sessionId>/subagents/` are handled separately --
    see FR-5).
  - The encoded-cwd is decoded back to a real filesystem path for the
    session's `directory` field.
  - Missing `~/.claude/` directory is not an error; the Claude Code adapter
    is simply inactive.
  - Discovery is cheap enough to run at least on the same cadence as the
    OpenCode session list refresh (assume O(hundreds) of files).

### FR-4: Claude Code session ingestion

- **Description:** Each Claude Code `.jsonl` file is parsed into ocman's
  `Session`, `Message`, and `Part` model. This happens on demand (when a
  session is opened) and incrementally (for updates) -- not via a one-shot
  import.
- **Acceptance Criteria:**
  - The following mappings are implemented (non-exhaustive, see Data
    Requirements):
    - `Session.ID` <- filename stem (the session UUID).
    - `Session.Directory` <- decoded cwd, or the `cwd` field on the first
      event (whichever is more reliable).
    - `Session.Title` <- `slug` field when present, else truncated first
      user prompt.
    - `Session.TimeCreated` <- timestamp of the first event.
    - `Session.TimeUpdated` <- timestamp of the last event or file mtime.
    - `Session.MessageCount` <- count of `user` + `assistant` events.
    - `Session.TotalInputTokens` / `TotalOutputTokens` <- sum of per-message
      `usage.input_tokens` / `usage.output_tokens`.
    - `Session.Status` <- inferred from the last `user`/`assistant` event
      using the same logic as OpenCode (`InferSessionStatus`).
  - `MessageData` is populated with role, model, provider (`"anthropic"`),
    tokens (input/output/cache-read/cache-write), timestamps, and
    `Finish` (stop_reason).
  - Each element of `message.content[]` becomes a `Part`
    (text / thinking / tool_use / tool_result). Tool-result parts are joined
    with the sibling `toolUseResult` structured payload where present.
  - Fields with no Claude Code equivalent (`ShareURL`,
    `Summary{Additions,Deletions,Files}`, per-request `DurationMs`,
    `TotalCost`) are returned as null/zero without breaking clients.
  - Cost: recorded as `null` or `0` at ingestion time; computing cost from
    tokens x model-price table is explicitly out of scope for v1 (see Out of
    Scope).

### FR-5: Sub-agent transcripts

- **Description:** Claude Code stores sub-agent (Task-tool) transcripts under
  `~/.claude/projects/<cwd>/<sessionId>/subagents/agent-<agentId>.jsonl` with
  metadata in `.meta.json` siblings. Ocman must represent these consistently.
- **Acceptance Criteria:**
  - Sub-agent events either appear inline in the parent session's message
    timeline (marked with `MessageData.Agent = "subagent"` or similar) or are
    reachable as a drill-down from the parent tool_use part -- the choice is
    an architect decision, but it must be consistent with how OpenCode's
    multi-agent flows are rendered today.
  - Sub-agent transcripts do not appear as top-level sessions in the session
    list.

### FR-6: Claude Code status detection

- **Description:** For each Claude Code session, ocman reports a live status
  (`busy / waiting / done / error`) comparable to what it does for OpenCode.
- **Acceptance Criteria:**
  - Base signal: infer status from the last event in the `.jsonl`, using the
    same rules as OpenCode (role / stop_reason / error).
  - Liveness signal: a session is `busy` if a `claude` process currently holds
    the `.jsonl` file open (verified via `lsof`) OR if its mtime has advanced
    within the last N seconds (configurable, default ~30s).
  - Crisp signal (when hooks are installed -- see FR-7): hook events update a
    per-session in-memory status that takes precedence over the
    file-inspection signals.
  - Process detection must filter out unrelated processes named `claude`
    (e.g. macOS `Claude.app` desktop helper); matching should be done on full
    argv.

### FR-7: Claude Code hooks integration (live status + pending signals)

- **Description:** To provide crisp live status, pending-permission, and
  pending-question signals for Claude Code, ocman installs hook entries into
  `~/.claude/settings.json` that POST events to an ocman HTTP endpoint.
- **Acceptance Criteria:**
  - On first startup where `~/.claude/settings.json` exists and lacks
    ocman-owned hooks, ocman **automatically installs** its hook entries
    (per user decision: auto-install, option A). The installation is
    idempotent and identifiable (e.g. hooks carry an ocman-specific marker
    command path or comment) so they can be updated or removed cleanly.
  - Hook events installed (at minimum): `SessionStart`, `Stop`, `Notification`,
    `PreToolUse`. The exact set is refined during architecture but must be
    sufficient to drive `busy / waiting / done`, pending-permission, and
    pending-question signals.
  - Ocman exposes an HTTP endpoint (e.g. `POST /api/hooks/claude`) that
    receives hook payloads and updates the in-memory live-session state for
    the affected session.
  - The hook endpoint is reachable only from `localhost` (same policy as the
    existing tmux endpoints).
  - Pre-existing user hooks in `~/.claude/settings.json` must be preserved;
    ocman's hooks are additive.
  - If hooks are not installed for any reason, status detection falls back to
    FR-6's file-based signals; pending-permission and pending-question
    indicators are simply not shown. (Per user decision on Q11: hooks are
    the primary path; no alternative pending-signal inference is built.)

### FR-8: Claude Code composer

- **Description:** Users can send a new user message to a Claude Code session
  from the ocman composer, just as they can for OpenCode.
- **Acceptance Criteria:**
  - The composer invokes a runtime-selected strategy based on session
    liveness:
    - **Live + tmux pane found for the session:** inject the prompt into the
      existing tmux pane (paste-bracketed where practical) so the already-
      running `claude` process handles it. No new `claude` subprocess is
      spawned. (Leverages the existing ocman tmux integration.)
    - **Live but no tmux pane found:** composer is disabled with a clear
      user-facing message explaining why (avoids fighting for the jsonl
      flock).
    - **Not live:** ocman spawns `claude -p --resume <sessionId>
      --permission-mode acceptEdits "<prompt>"`, captures stdout, and
      streams the response back to the frontend.
  - `--permission-mode acceptEdits` is the default for headless composer
    invocations.
  - The composer surfaces errors from the subprocess (non-zero exit, stderr)
    to the user.
  - Liveness detection for routing uses the same signals as FR-6.
  - Tmux is an accepted runtime dependency for composer-against-live-session.

### FR-9: Feature parity across agents (session browsing, archive, auto-archive, tmux, whisper)

- **Description:** Every feature listed below works identically for OpenCode
  and Claude Code from v1.
- **Acceptance Criteria:**
  - **Session list / detail / message view:** Claude Code sessions appear in
    the same list views and detail pages as OpenCode sessions.
  - **Archive / seen tracking:** Claude Code sessions can be archived and
    marked seen; state persists in ocman's state.db (see FR-11).
  - **Auto-archive:** the existing 7-day stale-session background job
    archives Claude Code sessions on the same schedule.
  - **Tmux jump:** the existing tmux integration works for Claude Code
    sessions (it is already agent-agnostic on the tmux side).
  - **Whisper transcription:** works identically regardless of target agent
    (it is already agent-agnostic).

### FR-10: Unified + per-agent UX (hybrid)

- **Description:** The default session list, dashboard, and project views
  present sessions from all detected agents together. Users can filter by
  agent and/or drill into a single agent's view.
- **Acceptance Criteria:**
  - The default session list is unified across agents, each row showing
    its agent badge.
  - A filter control lets users narrow to one agent.
  - All detected agents are always shown; there is no UI to hide an agent.
    (Per user decision on Q7.4.)
  - Per-agent drill-down views are reachable (e.g. via a filter preset or
    route) so users who only care about one agent have a clean view.

### FR-11: Cross-agent project grouping

- **Description:** Projects are derived from `directory` (cwd) and may
  contain sessions from multiple agents. A project view shows all sessions
  for that cwd, regardless of agent.
- **Acceptance Criteria:**
  - Project identity is `directory`-based, not agent-specific.
  - Opening a project shows a unified session list for that cwd, mixed
    across agents, with agent badges.
  - Project-level aggregates (session count, last used, etc.) sum across
    agents.

### FR-12: Ocman state.db is agent-aware

- **Description:** Ocman's writable state (archived, seen, and any future
  per-session state) is keyed by `(agent, session_id)`, not just
  `session_id`, to avoid collisions and to allow per-agent scope.
- **Acceptance Criteria:**
  - Schema migration adds an `agent` column to the relevant state tables.
  - Archive/seen operations from existing OpenCode users migrate cleanly
    (existing rows get `agent = "opencode"` on migration).
  - Archive/seen is a **per-agent** concept: archiving a Claude Code session
    does not affect an OpenCode session that happens to share an ID.
    (Per user decision on Q7.3.)

### FR-13: Historical ingestion of Claude Code sessions

- **Description:** On first run (or any run) of ocman with Claude Code
  detected, all pre-existing `.jsonl` sessions under `~/.claude/projects/`
  are available in the dashboard -- not just sessions created after ocman
  is installed.
- **Acceptance Criteria:**
  - Ocman does not require sessions to be "live" or "seen during ocman
    runtime" to appear; any readable `.jsonl` file is listed.
  - Performance target: initial listing of N sessions from disk completes
    within an acceptable time for typical users (see NFR-1).

### FR-14: Graceful degradation for unsupported capabilities

- **Description:** Where Claude Code cannot match an OpenCode feature in v1
  (e.g. cost per request, share URLs, summary stats), the API returns null/
  zero and the frontend hides or muted-states the corresponding UI rather
  than breaking.
- **Acceptance Criteria:**
  - The Session and Message types remain a superset; unsupported fields are
    nullable or zero-valued.
  - Frontend views render correctly for sessions whose unsupported fields
    are null/zero (no `NaN`, no error boundaries triggered).
  - No error is shown for features that are legitimately unavailable for
    an agent.

## Non-Functional Requirements

### NFR-1: Performance of Claude Code ingestion

- **Description:** Reading JSONL from disk must be fast enough that ocman
  feels snappy for realistic data sizes.
- **Acceptance Criteria:**
  - Session list: enumerating and building session summaries for O(1,000)
    Claude Code sessions returns to the frontend in under ~1s on typical
    developer hardware.
  - Session detail: opening an individual session's messages/parts is
    comparable in latency to opening an OpenCode session of similar size.
  - Incremental updates: re-reading a session whose `.jsonl` has grown is
    incremental (tail reads, not full re-parses) where practical.

### NFR-2: Safe modification of user configuration

- **Description:** Auto-installing Claude Code hooks into the user's
  `~/.claude/settings.json` must not damage existing user configuration.
- **Acceptance Criteria:**
  - A timestamped backup of `~/.claude/settings.json` is created before the
    first mutation.
  - Hook installation is idempotent: repeated installs do not duplicate
    entries.
  - Hook entries are identifiable (e.g. via a unique command path or
    sentinel comment) and removable by ocman via an (internal) uninstall
    routine.
  - Pre-existing hooks from the user or other tools are preserved.

### NFR-3: Security of hook endpoint

- **Description:** The HTTP endpoint that receives Claude Code hook events
  is a local-trust endpoint.
- **Acceptance Criteria:**
  - Requests are rejected unless they originate from `localhost` (same
    policy as existing tmux endpoints).
  - Payload size is bounded; malformed JSON is logged and discarded
    without crashing the server.

### NFR-4: No regression for existing OpenCode users

- **Description:** All existing OpenCode-only workflows continue to function
  after the multi-agent work lands.
- **Acceptance Criteria:**
  - An ocman instance with only OpenCode data (no `~/.claude/`) behaves
    identically to today from the user's perspective, modulo the presence
    of an agent badge on sessions.
  - State.db migration is automatic and non-destructive.
  - No new required configuration for OpenCode users.

### NFR-5: Extensibility of the adapter layer

- **Description:** Adding a third agent (e.g. Codex) should be achievable
  without modifying the core or existing adapters.
- **Acceptance Criteria:**
  - A new agent is added by implementing the `Agent` interface and
    registering the adapter.
  - No schema change is required in ocman's state.db for a new agent
    (the agent identifier is a string).
  - HTTP API handlers dispatch by agent identifier; no new endpoints are
    needed for a new read-only agent adapter.

## Data Requirements

### Entities

- **Session (extended):** gains an `Agent` string field. All other fields
  remain; fields unsupported by some agents become nullable/zero.
- **Message / MessageData / Part:** unchanged shape; Claude Code ingestion
  populates the existing fields where equivalents exist.
- **Ocman state (archived, seen, future):** keyed by `(agent, session_id)`.

### Claude Code -> ocman mapping (summary)

See FR-4 for the authoritative mapping. Notable sources:

- Claude Code stores sessions as newline-delimited JSON at
  `~/.claude/projects/<encoded-cwd>/<sessionId>.jsonl`.
- Each line is typed (`user`, `assistant`, `attachment`, `system`,
  `permission-mode`, `progress`, `last-prompt`, `queue-operation`,
  `file-history-snapshot`); ocman ingests `user` and `assistant` as
  messages. Other types are used for metadata inference (e.g.
  `system.turn_duration` for durations, `permission-mode` for mode history).
- Session-level metadata (cwd, gitBranch, version, slug) is duplicated on
  most events -- it is safe to derive from the first events.
- Tool calls and results live inside `message.content[]` blocks
  (`tool_use`, `tool_result`), not as top-level events. Ocman maps these to
  `Part` entries.
- Token usage lives in `assistant.message.usage` (input_tokens,
  output_tokens, cache_read_input_tokens, cache_creation_input_tokens).
- Cost is not persisted per-message by Claude Code; ocman stores `null`/`0`
  for now.

### Data flow (new)

```
Claude Code on disk           Ocman                       Frontend
----------------------        --------------------        ------------
~/.claude/projects/*.jsonl -> ClaudeCode adapter  -----\
~/.claude/settings.json   <-  (installs hooks)         -> unified Session/Message API -> SPA
claude process running    <-  lsof + process check   -/
tmux panes                <-> tmux integration (existing)
claude -p subprocess      <-  composer (headless path)
POST /api/hooks/claude    <-  hook events -> in-memory live state
```

## Integration Points

- **OpenCode SQLite DB** (`~/.local/share/opencode/opencode.db`): existing
  read-only access. Unchanged.
- **OpenCode HTTP API** (via discovered `--port` instances): existing. Unchanged.
- **Claude Code filesystem** (`~/.claude/projects/`, `~/.claude.json`,
  `~/.claude/settings.json`, `~/.claude/tasks/<id>/.lock`): new read / limited
  write access (settings.json hooks only).
- **`claude` CLI** as a subprocess: new. Used by composer in headless mode
  (`claude -p --resume <id> --permission-mode acceptEdits "<prompt>"`).
- **Tmux**: existing ocman dependency. Extended with paste-to-pane injection
  for Claude Code live-session composer.
- **`lsof`**: existing dependency (for OpenCode port discovery). Extended to
  check Claude Code jsonl file holders and process argv filtering.
- **Ocman state.db**: writable SQLite under `~/.local/share/ocman/state.db`.
  Schema migration adds the `agent` column.

## Constraints

### Technical

- **CGo / SQLite** stays required (OpenCode data access).
- **macOS / Linux only**: `lsof` continues to be used; Windows is not in
  scope (existing constraint, unchanged).
- **Tmux dependency for live Claude Code composer**: accepted per user
  decision (Q9.2).
- **`claude` CLI in `$PATH`**: required for composer in headless mode;
  discovered via which/PATH lookup. Absence disables only the headless
  composer path, not the rest of the Claude Code integration.
- **Single ocman instance owns hook installation**: multiple concurrent
  ocman instances editing `~/.claude/settings.json` is not a designed-for
  scenario in v1.

### Business / scope

- V1 ships with OpenCode + Claude Code only. Codex and others are explicitly
  future work but must be accommodated by the adapter design.
- Metrics dashboard for Claude Code is explicitly deferred (data model
  must not preclude it).

### Team

- No tests exist in the repo today (per `AGENTS.md`); there is no hard
  requirement in this spec to add a test suite, though architecture may
  recommend it for the adapter layer.

## Assumptions

(For the Architect to validate.)

1. **Claude Code `.jsonl` format is stable enough across minor versions**
   that a single parser can handle the versions users are running. Model IDs
   and a handful of optional fields evolve; the outer envelope does not.
2. **`claude -p --resume <id> "<prompt>"` appends to the same session jsonl
   as a live interactive session** if the session is not currently held open
   by another process. Given flock on `~/.claude/tasks/<id>/.lock`, a
   concurrent live TUI will block or conflict -- which is why the hybrid
   composer strategy in FR-8 avoids spawning when the session is live.
3. **Claude Code hooks (`SessionStart`, `Stop`, `Notification`, `PreToolUse`)
   are sufficient** to drive `busy / waiting / done`, pending-permission, and
   pending-question signals to parity with OpenCode. If they are not,
   pending signals may degrade and we accept that.
4. **The `claude` binary is on `$PATH` for users who want composer in
   headless mode.** If it is not, composer is simply disabled with a clear
   message.
5. **Encoded-cwd -> real path decoding is reversible** for all paths users
   encounter (Claude Code replaces `/` with `-` in directory names; edge
   cases around paths containing `-` are handled by also reading the `cwd`
   field on the first event).
6. **Process detection via `lsof` + argv filter** is reliable enough to
   distinguish the `claude` CLI from other processes (notably macOS's
   `Claude.app`).
7. **macOS `Claude.app` coexistence** is resolved by matching on full argv
   (e.g. path containing `/claude` without being a `.app` bundle executable).
8. **Silent auto-install of hooks** (per user decision Q10 option A) is
   acceptable for v1. NFR-2 mitigates risk via backup and idempotency.

## Out of Scope

- **Support for agents other than OpenCode and Claude Code** in v1 (Codex,
  Gemini, Aider, Cursor, Copilot CLI are future work, but the adapter layer
  is designed for them).
- **Metrics dashboard for Claude Code**: deferred. The data model is
  designed not to preclude it, but no metrics features are delivered for
  Claude Code in v1.
- **Cost computation for Claude Code** (e.g. tokens x model-price table):
  out of scope for v1. Cost-related fields are null/zero for Claude Code
  sessions.
- **Windows support**: unchanged from today -- not supported.
- **Streaming Claude Code sub-agent progress events in real time** to the
  frontend as they happen: out of scope; sub-agent transcripts are
  displayed post-hoc from the jsonl.
- **Bidirectional state sync between ocman archive state and Claude Code**:
  Claude Code has no archive concept; archive is ocman-local only.
- **Editing or deleting Claude Code sessions via ocman**: read-only +
  composer only; no destructive operations.
- **Cross-agent session merging** (treating the "same" conversation across
  two agents as one session): out of scope; we only group by project (cwd).
- **Heuristic pending-permission detection when hooks are not installed**
  (per Q11 decision A): not implemented; hooks are the sole source of
  pending signals.

## Success Criteria

1. A Claude Code user with no OpenCode installation can run ocman, see all
   their historical Claude Code sessions listed in the dashboard, open any
   of them, browse messages and parts, archive them, mark them seen, and
   send a new message via the composer -- with no manual configuration.
2. A user with both OpenCode and Claude Code sees a unified session list by
   default, with agent badges distinguishing rows, and can filter by agent.
3. For an actively-running Claude Code session (TUI open), ocman reports
   `busy` status; when the turn completes, status transitions to `waiting`
   or `done` within a few seconds of the underlying event.
4. Sending a message via the composer works end-to-end in both the
   live-tmux and headless `claude -p` paths, and is disabled with a clear
   message in the live-without-tmux case.
5. An OpenCode-only user upgrading to the new version notices no behavioral
   regression except the addition of an agent badge; existing archive and
   seen state is preserved.
6. Adding a third agent (e.g. Codex) is demonstrably feasible by
   implementing the `Agent` interface only; this is validated during the
   architecture phase via a short paper design for Codex, not by shipping it.

## Open Questions

The following are flagged for the Architect to resolve or confirm:

1. **Sub-agent rendering (FR-5):** inline-in-parent vs. drill-down vs.
   hybrid. Needs a UX decision informed by how busy real-world parent
   sessions become when sub-agents are used heavily.
2. **Exact hook set and payload shape (FR-7):** which subset of Claude
   Code's hook events is minimal-sufficient, and what JSON the hook
   command should POST. Requires prototyping.
3. **Liveness detection threshold (FR-6):** the default mtime window for
   considering a session `busy` in the absence of hook events. Start with
   ~30s, tune empirically.
4. **In-memory vs. persisted live state:** per-session live state from
   hooks (busy/pending/etc.) can be held in-memory and rebuilt on restart,
   or persisted. In-memory is simpler; persistence helps multi-process
   scenarios. Lean toward in-memory for v1.
5. **Frontend agent filter control:** exact placement and whether it is a
   global setting or per-view. Open for design.
6. **Composer UI differences per agent:** whether to expose a per-message
   permission-mode selector for Claude Code, or always use `acceptEdits`.
   Spec currently mandates `acceptEdits`; revisit in UX phase if needed.
7. **Claude Code CLI version pinning / detection:** whether to warn the
   user on versions known to have a different JSONL schema. Defer until
   we see a breaking change in the wild.
