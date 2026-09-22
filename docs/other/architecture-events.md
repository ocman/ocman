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
```

- The local watcher broadcasts activity for identified message/part mutations
  on the shared `/api/events` stream. The sidebar updates known rows directly,
  fetching a single session only when it is missing. This avoids list refetches
  on each token. The broadcast hub keeps the latest activity per session when
  a subscriber falls behind.
- Activity timestamps stay exact, but sorting uses one-minute buckets with
  stable ties. Concurrent streams in the same minute do not continually swap
  places. Stale list responses cannot roll activity timestamps backwards.
- Sending a message updates sidebar activity optimistically. Queued messages
  wait until sent. Failed sends restore the old timestamp unless newer SSE
  activity has arrived. Reorders use a 180 ms native animation and respect
  reduced-motion preferences. Pinned ordering and manual project ordering remain.
- Reconnecting and the slow reconciliation poll recover missed events.

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
