# Multi-Remote Support - Requirements

## Overview

Multi-remote support lets a single "hub" ocman instance attach to other
ocman instances running on different hosts over the network, and manage
all of their coding-agent sessions from one place. Sessions from every
attached machine appear in one unified list; the operator can open, watch
(live events), and drive (send prompts, respond to permissions/questions,
abort, compact) any remote session as if it were local. Creating a new
session is machine-aware: the hub picks the host based on where the target
project already exists, and prompts the operator to choose when the same
project exists on more than one machine.

The hub talks REST to the browser (unchanged) and fans out to each remote
over a long-lived **gRPC** connection (unary commands plus server-streamed
updates). The remote ocman gains a gRPC server surface (secured by a
remote-access token) that the hub dials; its existing localhost REST behavior
is untouched.

This builds on the existing `Platform` adapter abstraction
(`internal/platforms/`) and the host-local features (lsof port discovery,
tmux, worktrees, session changes, PR/Issue sidebar) — all of which remain
host-local and continue to execute on the machine that owns the session.

## Goals

- Manage coding-agent sessions across multiple machines from one ocman UI.
- Present local and remote sessions in a single, uniform, host-agnostic UI.
- Stream live session activity from remotes to the hub and back.
- Make new-session creation pick the right machine automatically when the
  project lives on exactly one host, and prompt the operator when it lives
  on several.
- Keep the frontend host-agnostic: the UI never branches on "is this remote";
  routing and capability decisions are made server-side.
- Degrade gracefully — an offline or slow remote must never block the
  unified session list.

## Target Users

- **Solo developers / operators** running OpenCode on several machines
  (e.g. a laptop, a workstation, a GPU box) who want a single dashboard to
  see and control every session without SSHing between hosts.
- The operator owns and trusts all machines involved (single-tenant); there
  is no notion of sharing remotes between distinct untrusting users in v1.

## Functional Requirements

### FR-1: Hub-to-remote connectivity over gRPC

- Description: The hub establishes one long-lived gRPC client connection to
  each attached remote ocman. Hub→remote commands use unary RPCs (e.g.
  send-message, respond-permission, abort, compact, start-session,
  worktree/tmux actions); remote→hub updates use server-streaming RPCs over
  the same HTTP/2 connection (session list changes, live events, project
  inventory changes, health). The frontend continues to use REST against the
  hub only; the hub fans out over gRPC.
- Acceptance Criteria:
  - A reachable remote with a valid token connects and reports `connected`.
  - Hub→remote commands and remote→hub event/update streams flow over the
    same long-lived gRPC connection.
  - The channel reconnects automatically after a transient network drop.

### FR-2: Remote identity (random ID + user label)

- Description: Each ocman generates a **random, stable instance ID on first
  startup**, persisted in its own `state.db`. The remote reports this ID
  (plus its hostname) during the gRPC handshake. For attached remotes, the
  hub uses the random ID as the canonical identifier for namespacing sessions
  and routing commands. The hub's own local machine is the v1 exception: it
  retains the routing/display sentinel `local` for backward-compatible local
  state while still persisting a real instance ID for its own identity. The
  operator may attach and later change a friendly **display name** on the hub
  side; the name is hub-local and does not affect routing.
- Acceptance Criteria:
  - An ocman instance has the same instance ID across restarts.
  - Two distinct ocman instances have distinct IDs.
  - Renaming an attached remote on the hub changes only its displayed name;
    sessions and routing remain bound to the random ID.

### FR-3: Local machine modeled as a built-in remote

- Description: The hub's own local machine is modeled as a built-in remote
  for display, capabilities, and host-service routing, so that local and
  remote sessions flow through one uniform server-side abstraction. The
  local instance still has a real random instance ID persisted in `state.db`,
  but v1 uses the routing/display sentinel `local` for self and keeps local
  session platform keys backward-compatible (`opencode`, not
  `r-<hubID>:opencode`). It appears in the settings list as a non-removable,
  non-editable "This machine" entry.
- Acceptance Criteria:
  - Local sessions carry `remoteId = "local"` / `remoteName = "This machine"`
    while retaining their existing bare platform key for state compatibility.
  - Remote sessions are addressed with an explicit platform key
    (`r-<remoteID>:<platform>`) plus the session ID; remote session detail and
    mutation routes are platform-qualified to avoid cross-host session-ID
    collisions.
  - The "This machine" entry cannot be removed or have its address/token
    edited.

### FR-4: Remote-access token authentication

- Description: Each ocman generates and persists a remote-access token in its
  own `state.db`. The token authenticates **inbound** gRPC connections (the
  hub presents the remote's token when dialing). The remote's existing REST
  auth (`OCMAN_AUTH_PASSWORD` / localhost trust) is unchanged and independent.
- Acceptance Criteria:
  - A hub presenting the correct token completes the handshake.
  - A hub presenting a missing/incorrect token is rejected, and the hub shows
    an `auth-failed` health status for that remote.
  - The token survives remote restarts.

### FR-5: Token surfaced in each ocman's settings

- Description: Each ocman's own Settings page displays its remote-access
  token status masked by default, with an explicit reveal/copy action. The
  operator copies it from the remote's settings page and pastes it into the
  hub's add-remote form.
- Acceptance Criteria:
  - The token is retrievable only via an explicit authenticated reveal action
    and shown masked otherwise, with a working copy-to-clipboard control.
  - The token value is not exposed to unauthenticated clients.

### FR-6: Unified session list with host badge

- Description: Sessions from the local machine and all reachable remotes are
  merged into one unified list, interleaved, each carrying a host badge
  (display name) alongside the existing platform badge.
- Acceptance Criteria:
  - With at least one remote connected, the list shows both local and remote
    sessions in a single view.
  - Each row clearly indicates which machine owns the session.

### FR-7: Cross-host session detail and live events

- Description: Opening a remote session shows full detail (messages/parts)
  and streams live activity. The remote streams session events to the hub
  over gRPC; the hub re-emits them to the browser as SSE, so the frontend
  consumes events identically regardless of host.
- Acceptance Criteria:
  - Opening a remote session renders its transcript.
  - Remote session detail/event/control requests include the owning platform
    key (for example `?platform=r-abc123:opencode`) so two hosts may safely
    have the same `session_id`.
  - Live updates for a remote session appear in the hub UI in near-real-time
    while the remote is connected.

### FR-8: Cross-host composer (send-message)

- Description: Sending a prompt to a remote session routes the command over
  gRPC to the owning remote, which invokes its local composer. Responses flow
  back via the live-event stream (FR-7).
- Acceptance Criteria:
  - A prompt sent from the hub to a remote session reaches the remote's agent
    and produces a visible response in the hub UI.

### FR-9: Cross-host interactive controls

- Description: Respond-permission, respond-question / reject-question, abort,
  and compact all route over gRPC to the owning remote and execute there.
- Acceptance Criteria:
  - Each control, when invoked for a remote session, takes effect on the
    owning remote and the result is reflected in the hub UI.
  - Controls an owning agent/platform does not support remain gated by the
    existing capability mechanism.

### FR-10: Host-local actions execute on the owning host

- Description: tmux operations and worktree-session creation are inherently
  host-local. When invoked from the hub for a remote session/project, the
  command is routed over gRPC and **executed on the owning remote host**
  (the tmux pane / worktree / launched OpenCode process live on that remote).
  The hub operator does not attach the remote's pane in their local terminal.
- Acceptance Criteria:
  - A tmux action for a remote session runs on the owning remote and reports
    success/failure back to the hub.
  - Creating a worktree session for a remote project runs `git worktree add`
    + launches OpenCode in tmux on the owning remote, and the new session
    appears in the unified list attributed to that remote.

### FR-11: Per-remote project inventory (cached)

- Description: The hub maintains a cached per-remote inventory of projects
  that are already checked out / known to each remote's project index. The
  inventory is fetched/subscribed over gRPC, refreshed periodically and on
  reconnect. The local machine contributes its own inventory the same way.
- Acceptance Criteria:
  - After connecting a remote, the hub holds a list of that remote's known
    projects.
  - The inventory updates after reconnect and on periodic refresh.

### FR-12: Project identity for cross-host matching

- Description: "The same project" is determined by repository identity:
  primarily the git remote/origin URL, falling back to the project directory
  basename when there is no git remote. Two hosts with a checkout of the same
  origin are treated as the same project.
- Acceptance Criteria:
  - Two checkouts of the same origin URL on different hosts match as one
    project.
  - A repo with no remote matches by directory basename.

### FR-13: Machine-aware new-session creation

- Description: The core new-session action becomes remote-aware. When the
  operator starts a session for a project, the hub computes the project's
  repo identity (FR-12), looks up which machines (local + remotes) already
  have that project (FR-11), and selects the host as follows:
  - **Exactly one match:** auto-select that machine, no prompt.
  - **Multiple matches:** prompt the operator to choose the machine.
  - **Zero matches:** prompt the operator to pick a remote explicitly.
  The session is then created on the chosen host (FR-10 mechanics for the
  launch), and appears in the unified list attributed to that host.
- Acceptance Criteria:
  - Single-host project → session starts on that host with no prompt.
  - Multi-host project → operator is prompted and the session starts on the
    chosen host.
  - Unknown project → operator is prompted for a remote and the session
    starts there.

### FR-14: Remote management settings page

- Description: A settings page to manage remotes. Adding a remote takes an
  address (host:port or URL), a remote-access token, and an optional display
  name. On save the hub dials the remote, performs the handshake (exchanges
  random ID, fetches hostname + project inventory), and reports success or
  failure. Each listed remote shows: editable display name, reported
  hostname, random ID, connection/health status with last-seen time, masked
  re-enterable address/token, session count, and project count. Per-remote
  actions: edit name, edit address/token, remove, reconnect, enable/disable.
  The local machine appears as a non-removable "This machine" entry (FR-3).
- Acceptance Criteria:
  - Adding a remote with a valid address+token results in a `connected`
    status and its sessions/projects becoming available.
  - Adding with a bad address or token surfaces a clear failure status.
  - Edit name / edit address+token / remove / reconnect / enable-disable all
    behave as described.

### FR-15: Graceful degradation for offline/slow remotes

- Description: When a remote is unreachable or slow, the unified list still
  renders (local + reachable remotes). The unreachable remote's sessions are
  shown stale with an offline indicator (or omitted with an indication), and
  per-remote health/last-seen is visible on the settings page. Slow remotes
  are bounded by a short timeout so they never block the aggregate list.
- Acceptance Criteria:
  - With one remote down, the list still renders all other sessions promptly.
  - The down remote's status shows `offline` with a last-seen time.
  - A slow remote does not delay the list beyond the configured timeout.
  - Stale offline sessions are best-effort and memory-only in v1; after a hub
    restart, an offline remote may show no stale sessions until it reconnects.

### FR-16: Protocol version handshake

- Description: The gRPC handshake exchanges a protocol version over a
  long-lived gRPC client connection from hub to remote. Hub commands use unary
  RPCs and remote updates use server-streaming RPCs over that same HTTP/2
  connection. Incompatible versions surface as a clear health status rather
  than silent breakage.
- Acceptance Criteria:
  - A remote with an incompatible protocol version shows an
    `incompatible-version` (or equivalent) health status and is not used for
    session operations.

### FR-17: Host-agnostic frontend

- Description: The frontend must not branch on whether a session is local vs.
  remote, nor on a specific remote's identity. Host routing and capability
  gating are decided server-side and surfaced to the UI (host badge,
  capability flags, reason strings), consistent with the existing
  capabilities-driven UI convention (AD-12a / `useCapabilities()`).
- Acceptance Criteria:
  - No frontend code branches on remote identity to decide behavior.
  - Remote-specific capability differences are conveyed via server-provided
    flags/reasons, not hardcoded in the UI.

## Non-Functional Requirements

### NFR-1: List aggregation latency / non-blocking fan-out

- Description: Aggregating the unified session list across up to ~10 remotes
  must not block on any single slow/offline remote. Each remote fetch is
  bounded by a short timeout; results are merged as they arrive.
- Acceptance Criteria: With one remote artificially delayed beyond the
  timeout, the unified list returns within the timeout budget using the
  remaining remotes.

### NFR-2: Scale

- Description: v1 must comfortably support up to ~10 attached remotes
  (fan-out, polling, and channel management sized accordingly).
- Acceptance Criteria: With 10 connected remotes, list aggregation, event
  streaming, and command routing remain responsive.

### NFR-3: Transport security

- Description: Token auth on the gRPC channel is mandatory. TLS is
  recommended and supported, but the operator may run plaintext gRPC over a
  trusted overlay network (Tailscale/WireGuard/VPN), matching ocman's
  existing localhost-trusting posture.
- Acceptance Criteria: A connection without a valid token is rejected. TLS,
  when configured, secures the channel.

### NFR-4: Secret handling

- Description: Remote-access tokens stored on the hub (for dialing remotes)
  are protected/obfuscated in the hub's `state.db` using an app-local secret
  and never returned to unauthenticated clients; tokens are masked in all UI
  surfaces. This protects against accidental disclosure and casual inspection,
  but does not protect against an attacker who can read the full state DB and
  app-local secret. A follow-up ticket should move stored remote tokens to the
  OS keychain/keyring.
- Acceptance Criteria: Tokens are not exposed in plaintext to the browser
  beyond an explicit authenticated reveal/copy affordance on the owning
  instance. Stored tokens for attached remotes are never returned in plaintext.

### NFR-5: Reconnection resilience

- Description: Transient network failures trigger automatic reconnection with
  backoff; on reconnect the hub refreshes the remote's project inventory and
  resumes event streaming without operator intervention.
- Acceptance Criteria: After a simulated network drop and recovery, the
  remote returns to `connected` and resumes streaming automatically.

### NFR-6: Backward compatibility

- Description: With no remotes configured, ocman behaves exactly as today
  (local-only). Existing REST endpoints and the localhost-only posture are
  unchanged for users who never attach a remote.
- Acceptance Criteria: A fresh install with zero remotes is behaviorally
  identical to the pre-feature build.

## Data Requirements

Entities the hub must track (storage location to be decided by the
Architect; hub-side state likely lives in `state.db`):

- **Remote** — hub-side records have a hub-local primary key plus an optional
  learned `remoteID` (the remote's random instance ID, populated after a
  successful handshake), `displayName`, `address`, token (protected/masked),
  `enabled`, `lastSeen`, `healthStatus`, `hostname`, `protocolVersion`. The
  local machine is a built-in Remote with `remoteId = "local"` for routing and
  display, while still having its own persisted random instance ID.
- **Instance identity** — each ocman persists its own random instance ID and
  its remote-access token in its `state.db`.
- **Session addressing** — local sessions keep the existing `(platform,
  session_id)` key. Remote sessions use a compound platform key
  (`r-<remoteID>:<platform>`) plus `session_id`; all remote session routes are
  platform-qualified to avoid session-ID collisions. The existing
  `Session.platform` is joined by remote/host display attributes surfaced to
  the UI as a host badge.
- **Project inventory (cached, per remote)** — list of known projects with
  their repo identity (origin URL and/or directory basename) used for the
  new-session matching in FR-13.

Data flow (high level): browser ⇄ REST ⇄ hub ⇄ gRPC ⇄ remote ocman ⇄
(local platform adapters, tmux, git, project index) on the remote host.
Session events originate on the remote and stream hub-ward; commands
originate at the hub and stream remote-ward.

## Integration Points

- **gRPC** — new bidirectional channel between hub and remotes (new server
  surface on the remote, new client on the hub).
- **Existing ocman REST API** — unchanged for the browser; the hub remains
  the only thing the frontend talks to.
- **Existing `Platform` adapters / `internal/platforms`** — run on the host
  that owns the session; surfaced to the hub via gRPC.
- **Existing host-local subsystems** (lsof port discovery, tmux, worktrees,
  project index, session changes, PR/Issue sidebar) — continue to run on the
  owning host.
- **Network overlay (operator-managed)** — Tailscale/WireGuard/VPN or direct
  reachability; not managed by ocman.

## Constraints

- **Technical:**
  - Go backend; pure-Go SQLite (`modernc.org/sqlite`); React/TS frontend
    (existing stack).
  - Host-local features (tmux, worktrees, lsof, DB reads) cannot be performed
    across hosts from the hub's machine; they must execute on the owning host.
  - The frontend must stay host-agnostic (no remote-identity branching),
    enforced consistently with the existing platform-branching rules.
- **Business / operational:**
  - Single-tenant trust model: one operator owns all machines.
  - Operator is responsible for network reachability and (optionally) TLS or
    a trusted overlay.
- **Scope:**
  - Only the core new-session action is remote-aware in v1.

## Assumptions

(For the Architect to validate.)

- **A-1:** The remote ocman gaining a gRPC server + token auth is acceptable
  new server-side surface, despite the earlier "remote needs no remote-mode
  code" intent — the bidirectional gRPC decision (and dedicated token)
  supersede the pure-REST-client idea. *Flagged explicitly for validation.*
- **A-2:** gRPC is the chosen transport for both command dispatch and event
  streaming (vs. proxying REST per-request). The hub re-emits remote events
  to the browser as SSE.
- **A-3:** A single random instance ID per ocman is sufficient as the routing
  key; collisions across ~10 instances are negligible.
- **A-4:** The remote's existing project index is sufficient to build the
  per-remote project inventory used for FR-13 matching.
- **A-5:** Repo identity via origin URL (basename fallback) is a good-enough
  "same project" heuristic; normalization rules (e.g. ssh vs https origin
  forms, trailing `.git`) are an Architect detail.
- **A-6:** Hub-side protection for stored remote tokens uses an app-local key
  managed like the existing `auth_secret` in `state.db`; a follow-up ticket
  will move this to the OS keychain/keyring for stronger at-rest protection.
- **A-7:** Capability/version differences between hub and remote are conveyed
  to the UI through the existing capabilities mechanism plus a per-remote
  health status.

## Out of Scope

- SSH tunnel management; Tailscale/VPN auto-configuration (operator's
  responsibility).
- Hub-side MCP aggregation or splitting work onto remotes; MCP stays per-host.
- Remote-aware PR/Issue sidebar and Worktrees-view entry points (only the
  core new-session action is remote-aware in v1).
- Cross-host file transfer or cloning a never-seen repository onto a remote.
- Aggregated cross-host metrics/cost roll-ups on the dashboard (local-only in
  v1; tracked as a follow-up ticket — see Open Questions).
- Multi-tenant / shared-remote access between distinct untrusting users.

## Success Criteria

- **Headline acceptance (v1 "done"):** From a hub, the operator registers a
  remote via token; remote sessions appear in the unified list with a host
  badge; opening one streams live events; sending a prompt reaches the remote
  agent; and creating a new session for a multi-host project prompts for the
  machine and starts it on the chosen host.
- The unified list never blocks on a slow/offline remote (NFR-1).
- A zero-remote install is behaviorally identical to today (NFR-6).
- Up to ~10 remotes remain responsive (NFR-2).

## Open Questions

- **OQ-1:** Exact gRPC proto/service definition, message shapes, and how the
  existing `Platform`-adapter operations map onto gRPC methods (Architect).
- **OQ-2:** TLS configuration surface (flags/env) and whether mutual TLS is
  offered in addition to the bearer token (Architect).
- **OQ-3:** Repo-identity normalization rules for matching origin URLs across
  hosts (ssh vs https, trailing `.git`, host aliases).
- **OQ-4:** How event ordering/replay is handled across a reconnect (do we
  re-fetch detail on reconnect, or replay buffered events?).
- **OQ-5:** Whether stale sessions from an offline remote are shown grayed-out
  vs. omitted, and the exact timeout budget value for FR-15/NFR-1.
- **OQ-6:** Follow-up ticket: cross-host metric/cost aggregation on the
  dashboard (deferred from v1).
- **OQ-7:** Whether the remote-access token can be rotated/regenerated from
  the settings page and how that propagates to hubs already holding the old
  token.
- **OQ-8 / follow-up ticket:** Move hub-stored remote tokens from app-local
  `state.db` protection to the OS keychain/keyring.
- **OQ-9 / follow-up ticket:** Persist last-known remote sessions in
  `state.db` so stale offline sessions survive hub restarts.
