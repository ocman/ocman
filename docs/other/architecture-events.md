---
title: Session and event data flow
weight: 11
---

Part of the [architecture overview](../architecture/).

```mermaid
sequenceDiagram
    participant B as Browser
    participant S as server handlers
    participant Q as routines.Service
    participant R as Registry/Router
    participant O as Remote owner
    participant A as opencode adapter
    participant D as opencode.db / OC HTTP
    participant E as SSE broadcast
    participant I as state.db

    B->>S: GET /api/sessions
    S->>R: resolve platform/host
    R->>A: ListSessions()
    D-->>A: background /global/event prompts + terminal parts
    A->>D: SQL json_extract
    A->>A: overlay pending prompt registry
    D-->>B: JSON (status settled at query time)
    D-->>E: watcher observes message/part mutation
    E-->>B: /api/events: ocman.session.activity (id, timestamp)
    B->>B: patch activity; stable minute-bucket sort; animate moved rows
    D-->>S: settled bash part containing git commit/push
    S->>E: ocman.git.command (session, owner, project, action)
    E-->>B: debounce matching PR/Issue refresh
    B->>S: GET /api/projects
    I-->>S: cached local projects snapshot
    S-->>B: cached projects JSON
    S->>D: background project aggregate refresh
    S->>I: persist refreshed snapshot
    E-->>B: SSE (ocman.projects.changed)
    R->>O: gRPC Session / SessionInfo / StreamEvents (remote only)
    O->>A: Session / SessionInfo / ProxyEvents
    O->>I: read owner-local approvals and commit observations
    O->>O: inject persisted approvals,<br/>tee synthetic approval events
    O-->>S: owner-enriched JSON / framed SSE
    Note over S,Q: every 5 s: claim due routines
    Q->>R: ensure project instance and create fresh session
    R->>A: create session and send saved prompt
    Note over Q,A: poll linked session until it settles
    E-->>B: SSE (session.updated)
    A->>I: persist owner-local commit observations / MCP inbox send
    A->>I: create permission Inbox item; archive on resolution
    Q->>I: commit run outcome and Routine Inbox notification
    I-->>S: local state or owner-routed remote RPC
    B->>S: REST Inbox list/read/archive and session permission reply
    S-->>B: categorized Inbox JSON with permission actions
    E-->>B: SSE (ocman.inbox.changed); polling fallback
    B->>S: POST /api/sessions/start (startId)
    S->>E: step progress via hostsvc.WithProgress
    E-->>B: SSE (ocman.session.start.progress)
```

- A new conversation's first submission sends a random `startId`. The server
  reports each start step (OpenCode, worktree, session, prompt) through the
  request context's progress callback and broadcasts it as
  `ocman.session.start.progress`; only the tab holding that `startId` renders
  it, beneath the pending prompt. The callback does not cross gRPC, so for a
  remote worktree start the hub reports OpenCode and the worktree around the
  whole call.

- The local watcher broadcasts activity for identified message/part mutations
  on the shared `/api/events` stream. The sidebar updates known rows directly,
  fetching a single session only when it is missing. This avoids list refetches
  on each token. The broadcast hub keeps the latest activity per session when
  a subscriber falls behind.
- Settled bash tools containing `git commit` or `git push` emit best-effort
  `ocman.git.command` hints on `/api/events`, including failed commands and
  commands without output. The payload carries `sessionID`, `remoteId`,
  `projectId`, `directory`, and `action`, never command arguments or output.
  The local headless watcher observes these without a browser or enabled
  permission judge. Remote hints are observed by the hub while proxying a
  remote session stream, using only the remote adapter's last cached session
  identity. Unknown identities are skipped; no transcript read or RPC delays
  the stream. The visible PR/Issue tab refreshes its lists and CI
  checks after a 750 ms debounce, scoped by owner and project, including sibling
  worktrees and session-loading transitions. Automatic CI invalidation targets
  only that project's upstream repositories; unrelated cached checks and rich
  previews stay intact. This is a command-string heuristic, not proof that git succeeded;
  commands run outside agent bash tools are not observed.
- Activity timestamps stay exact, but sorting uses one-minute buckets with
  stable ties. Concurrent streams in the same minute do not continually swap
  places. Stale list responses cannot roll activity timestamps backwards.
- Sending a message updates sidebar activity optimistically. Queued messages
  wait until sent. Failed sends restore the old timestamp unless newer SSE
  activity has arrived. Reorders use a 180 ms native animation and respect
  reduced-motion preferences. Pinned ordering and manual project ordering remain.
- A changed session title (OpenCode auto-title, a TUI rename, another
  client) refreshes that session's list row, then broadcasts
  `ocman.session.changed` with `patch: {title}`. The sidebar and the open
  session's header apply it in place. For a subscriber that has fallen behind,
  the hub merges pending patches for a session so a later status patch keeps
  the title. If an identity-only change is involved, the pending event stays
  identity-only: it asks for a refetch, which a later patch cannot cancel. The
  open session re-reads its title on such an event and drops a response that a
  newer title event or fetch has overtaken. The hub keeps each session's events
  in publish order: once one is parked, later ones are parked behind it, and
  the writer takes buffered then parked events as one batch under the same
  lock producers use to decide between buffering and parking. The watcher publishes
  titles one at a time, so an older title is never sent after a newer one.
- Reconnecting and the slow reconciliation poll recover missed events.
  Sleep or a network change can leave a stream half-open, so it never
  errors and never reconnects. The browser therefore replaces both the
  global and the session stream when the user returns: the tab becomes
  visible after 30 s hidden, the browser comes back `online`, or the clock
  jumps while visible (the machine slept). The new stream's open runs the
  usual reconnect reconciliation and skips any pending backoff.
- A permission reply that lands while the stream is down is never replayed.
  While a permission prompt is open, the page polls
  `GET /api/session/{id}/permissions?refresh=1&platform=…`. The `platform`
  parameter pins the owner, because a bare session id can resolve to another
  machine. `refresh=1` calls `Platform.RefreshPermissions`, which re-reads
  OpenCode's live list instead of ocman's observed-prompt cache, which the
  session stream can bypass. Remotes serve it over the `RefreshPermissions`
  RPC, and an owner without a live list fails instead of returning its
  cache. The prompt is dismissed only after two consecutive successful
  reads that omit it, and stays up whenever a read fails.

Ocman never persists session status. The live turn signal from the running
OpenCode instance decides whether a session is busy; the last stored message
row only settles which terminal state a finished session is in. Nothing is
written back to OpenCode's database.

The local projects index uses stale-while-refresh persistence. On startup the
server hydrates the last snapshot from `state.db`; a request receives that
snapshot immediately while the expensive `opencode.db` aggregate refreshes in
the background. A changed result is persisted and announced over global SSE so
TanStack Query refetches the authoritative list. Remote inventories and archive
overlays remain live and are not stored in this cache.

Ocman owns routine state. The Routines page creates, edits, soft-deletes, and
starts routines over REST. Manual and scheduled paths first claim an immutable
run snapshot in `state.db`, then create and prompt a fresh session through the
shared session service. The run stays active until that session settles. The
same row stores success or failure, any error, and the session link shown in
history.

Remote commit observations stay on the owner. The hub neither writes raw remote
tool output to its state database nor appends hub-local observations to a remote
Session Info response. While connected, the existing proxied session event
stream triggers the browser's debounced Session Info refresh. While
disconnected, the owner can continue capturing independently; the hub follows
its normal unavailable behavior and reads the persisted owner records after
reconnect. Neither side reconstructs missed observations from transcripts.
