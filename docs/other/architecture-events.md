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

    B->>S: navigation: GET /api/session/id
    S->>I: clear local session and project archive markers
    B->>S: GET /api/sessions
    S->>R: resolve platform/host
    R->>A: ListSessions()
    D-->>A: background /global/event prompts + terminal parts
    A->>D: SQL json_extract
    A->>A: overlay pending prompt registry
    D-->>B: JSON (status settled at query time)
    D-->>E: watcher observes message/part mutation
    E-->>B: /api/events: ocman.session.activity (id, timestamp)
    B->>B: patch activity label; keep completion order
    E-->>B: ocman.session.changed (id, platform, terminal status)
    B->>S: GET /api/session/id?platform=owner&peek=1
    S-->>B: JSON with durable lastTurnCompletedAt
    B->>B: sort by completion; animate moved rows
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
    O->>I: read owner-local approvals, interruptions and commit observations
    O->>O: inject persisted history notices,<br/>tee synthetic approval events
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
    S-->>B: categorized Inbox JSON with owner-local metadata titles
    E-->>B: SSE (ocman.inbox.changed); polling fallback
    Note over B,S: inbox/notify event bursts coalesce;<br/>in-flight requests finish before one follow-up
    B->>S: POST /api/sessions/start (startId)
    S->>E: step progress via hostsvc.WithProgress
    E-->>B: SSE (ocman.session.start.progress)
    R->>O: replace managed OpenCode server
    O->>I: prepare a hidden interruption batch
    O->>O: stop the owned server
    O->>I: confirm history after successful stop
    O->>E: ocman.session.changed for affected sessions
```

- Inbox titles are indexed session-metadata reads on the owning machine,
  including native children. Remote owners enrich the existing Inbox RPC;
  the hub does not fetch a session list or transcript for a title. Notify
  snapshots carry owner-qualified prompt request IDs so a resolution received
  mid-fetch clears only that request, including a child's prompt surfaced on
  an ancestor. Periodic polling pauses in hidden tabs, but event refreshes
  remain active for background notifications.
- A new conversation's first submission sends a random `startId`. The server
  reports each start step (OpenCode, worktree, session, prompt) through the
  request context's progress callback and broadcasts it as
  `ocman.session.start.progress`; only the tab holding that `startId` renders
  it, beneath the pending prompt. The callback does not cross gRPC, so for a
  remote worktree start the hub reports OpenCode and the worktree around the
  whole call.

- Only the first successful session-detail fetch after navigation opens a local
  session and clears its archive markers. Reconnects, refreshes, pagination and
  background inspections use `peek=1`, preserving archives made in another tab
  unless newer activity meets the configured resurfacing policy. Read-watermark
  updates and tab visibility changes do not clear the browser's archived flag.
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
  previews stay intact. Matching PR previews retain repository identity while
  metadata reloads. An overlapping hint cancels the obsolete request and starts
  a fresh lookup, so its late response cannot restore an old head SHA. Canonical
  repository links can also match before the initial metadata response arrives.
  This is a command-string heuristic, not proof that git succeeded;
  commands run outside agent bash tools are not observed.
- Rows sort by `lastTurnCompletedAt`, including terminal errors, with creation
  time as fallback and compound session identity as a deterministic tie-breaker.
  Tool-call steps, compaction and streaming do not advance that key. Worktree
  groups use the same order, with the main checkout first. Stale list responses
  cannot roll activity or completion timestamps backwards or merge different owners.
- Terminal status patches carry their platform. The browser fetches the exact
  owner-qualified session and validates its identity before applying completion
  metadata, bypassing a potentially stale global list snapshot. Older events
  with ambiguous owners trigger a list refresh without patching a guessed row.
- Sending a message updates sidebar activity optimistically. Queued messages
  wait until sent. Failed sends restore the old timestamp unless newer SSE
  activity has arrived. Sending and reading do not change the completion key.
  Reorders use a 180 ms native animation and respect
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

Before replacing a managed server, its owner persists an interruption notice for
each active turn in `state.db`. Candidates come directly from the owner's session
table, including children hidden by the display list, and fresh lifecycle reads
select the affected turns. On v1, preparation resolves canonical managed roots
through the owner's local Host and persists membership by directory. Confirmation
reuses that membership and resolves only directories first admitted during the
scan or shutdown, saving those results too. External linked
worktrees belong to their main checkout; nested independent repositories do not.
Positively missing historical directories are excluded; permission, cancellation,
and inconclusive Git errors still block replacement.
On v2, every owner-local project belongs to the machine server.
The complete preparation batch commits before the
server is stopped, but stays hidden until Stop succeeds. An attempt-scoped
baseline records every settled status and latest message
identity. Failed-stop retries refresh that baseline after validating the original
runtime is still running; a durably stopped attempt
keeps its original evidence until confirmation recovers. Unchanged settled
history and manual aborts before the current stop are excluded. Immediately
before Stop, the owner revalidates lifecycle evidence and persists a separate
stop baseline, scan-start admission window, entry timestamp and original runtime handle. A crash between
shutdown and acknowledgment retains this evidence: recovery verifies closure
with the original handle before confirmation, even without an inventory row.
An inconclusive recovery probe blocks replacement. An explicit restart can retry
a failed Stop despite authentication mismatch only when the exact saved cleanup
handle has a matching warm authorization or managed inventory row, with a runtime
ID and the expected root. This never authorizes the unauthenticated endpoint;
automatic recovery stays blocked. Already-persisted aborts are excluded even
when the live status lags. An active turn may advance from its user message or
assistant step to an aborted envelope. New turns and sessions admitted after
the pre-stop scan starts may have envelopes older than the later Stop-entry
timestamp. Their creation is checked against the saved admission window, while
abort completion must still be at or after Stop entry. Manual aborts completed
before Stop remain excluded.
Confirmation commits before notices are broadcast, reading final owner-local turn evidence
after the old endpoint has closed, dropping normally completed turns and including
turns submitted during preparation. The new server is not launched or published
until that reconciliation commits; the stopped endpoint is the submission barrier.
Reconciliation has a durable candidate queue. Completed reads and directory
membership are checkpointed with short independent write budgets, including
when the callback's deadline expires immediately after a successful read.
Completed reads commit in pages of at most 64 candidates, flushing a partial
page on read failure or cancellation, to avoid a disk sync per session. Each
candidate carries its original baseline, so retries read pending pages instead
of decoding the entire historical snapshot again. Newly resolved membership is
saved before the lifecycle read, so a timeout in that read does not repeat git
resolution on the next attempt. The callback drains pages under its bounded
context deadline, with no session-count cutoff or forced extra request for the
final empty check. Deadline retries
resume the remaining queue after an owner restart without re-enumerating history,
repeating completed reads, or stopping the runtime again. Results remain hidden
until one SQL publication transaction commits the entire completed queue and
retires the stopped managed runtime row. Retirement happens before the same
transaction clears the original handle, so a crash or failure in the host's
later inventory cleanup cannot make a cold owner reuse the stopped endpoint.
A persisted handle must match the inventory row; a newer runtime or another
root is preserved. Legacy confirmation retires its pending root under the
same owner-root serialization and does nothing on an already-confirmed replay.
A failed stop preserves the old instance and
aborts replacement; a missing managed tmux session is considered stopped only
when its endpoint is also gone or positively belongs to another instance. The
durable runtime handle and pending evidence remain available until confirmation
succeeds, so a failed confirmation can be retried after an owner restart.
History reads restore confirmed notices, including
after a follow-up or an owner reconnect. An unfinished turn whose process has
already disappeared receives a durable notice when its history is read. These
are history entries, not persisted session status. A health-check timeout,
cancelled probe, reset/EOF transport failure, HTTP 429 or HTTP 5xx fails the ensure request without stopping
the shared server. A definite failure or an explicit restart logs the reason
at warning level before replacement. Follow-ups are never replayed automatically.

Cleanup handles and routing authorization are separate. Only a successful probe
authorizes a machine endpoint, and that proof is process-local. An inconclusive
probe preserves an endpoint only if this owner process previously validated it;
a cold durable handle alone authorizes nothing. Identity rejection or successful
Stop revokes authorization even when the cleanup handle remains for recovery.

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
