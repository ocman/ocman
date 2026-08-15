# Architecture: Session read performance at large database scale

## Summary

Keep the current SQL as the correctness oracle, but stop running it per
directory and on every short poll. The local OpenCode adapter owns one
copy-on-write global session snapshot. Warm reads return the last successful
snapshot immediately; one background refresh updates it. Directory reads and
`/api/sessions/notify` filter/project that same snapshot and retain the current
overlay and ordering pipeline.

OpenCode events mark individual sessions dirty. After event-shape validation,
the adapter recomputes only those sessions with the current exact aggregate
expressions. A slow full scan remains for startup, reconnect, deletion/event
loss reconciliation, and as the fallback if incremental correctness cannot be
proved. SSE becomes the primary frontend invalidation path only after automated
local and remote coverage is complete.

No public REST or browser SSE shape changes, no persisted cache, no new
dependency, and no database-pool increase.

## Measurement-driven decisions

Live experiments on the representative large database measured:

| Experiment | Time | Decision |
|---|---:|---|
| Current full session aggregate | 4.33 s | Keep as reconciliation baseline, not a request-path operation |
| Grouped session SQL | 3.65 s | Only 16% faster; do not take rewrite risk for a small gain |
| Latest/per-session lookups | 0.13 s | Use exact per-session recomputation for event-driven updates if the event audit passes |
| Largest directory duplicate scan | 2.56 s | Eliminate directory cache entries; filter the global snapshot in memory |
| Grouped project rewrite | 7.11 s | Reject; slower than current project query |
| Current project query | 5.82 s | Keep SQL and reduce/constrain refreshes |

The first release therefore optimizes invocation count and blocking behavior,
not query syntax. Any later SQL change must beat these figures on the same 12
GB copy and pass byte-equivalence tests.

## Components and files

| Area | Existing files | Minimal change |
|---|---|---|
| Exact DB reads | `internal/db/sessions.go` | Keep `GetSessions("", 0)` unchanged; extract/reuse its projection as `GetSessionSummary(id)` only after equivalence and event audit tests |
| Local snapshot | `internal/platforms/opencode/adapter.go`, `models_cache.go` | Replace package-global directory-keyed entries/refresher with one cache owned by the adapter: last good snapshot, refresh state, dirty session IDs, debounce timer |
| Live event intake | `internal/autoapprove/watcher.go`, `tee.go` | Parse all proven list-affecting OpenCode events, not only first-seen `session.updated`; route qualified dirty IDs to the adapter and broadcasts |
| List/notify reads | `internal/server/handlers_sessions.go`, `fanout.go` | Preserve fan-out, merge, sort, limit, state overlay, notice, pinned, and notify projection behavior; both endpoints consume adapter snapshots |
| State mutations | `internal/server/handlers_state.go`, `handlers_project_archive.go`, session mutation hooks | Broadcast only after successful persistence/mutation; patch cheap live/state fields or invalidate the relevant snapshot/project |
| Project refresh | `internal/server/projects_index.go`, `server.go` | Serialize each owner's refresh and retain one dirty follow-up; keep `db.GetProjects` |
| Remote transport | `internal/remote/proto/remote.proto`, `server.go`, `manager.go`, `platform.go` | Keep `Sessions` RPC; add one server-streaming invalidation RPC and implement the already-declared `WatchProjects` stream |
| Browser invalidation | `frontend/src/lib/useGlobalEvents.ts`, `queries.ts`, `App.tsx`, `useNotifyData.ts` | Invalidate shared TanStack session/project queries and notify data; reconcile on SSE open/reopen |
| Polling/visibility | `Dashboard.tsx`, `ProjectDetail.tsx`, `useSidebarSessions.ts`, `useNotifyData.ts`, `BackendStats.tsx` | Move eligible fallback polls to 3 minutes after the event-coverage gate; pause while hidden and refresh on return |
| Forge previews | `frontend/src/lib/githubPreview.ts`, `GitHubLinkPreview.tsx` | Add a bounded per-normalized-URL success cache plus in-flight promise; retain visible-card 5 s revalidation |
| Observability | existing `srvtiming` and OTel metrics files | Count snapshot hits/stale hits, full/per-session refreshes, coalescing, durations, failures, and dirty follow-ups |

No new cross-platform interface is required. `platforms.Platform.Sessions` stays
the session-read seam and `hostsvc.Host.Projects` stays the owner-scoped project
seam.

## Session snapshot

The adapter cache stores only the DB-derived `[]db.Session` returned by the
current aggregate calculation, sorted as today, plus an ID index for
copy-on-write replacement. Live status, prompt flags, connection flags,
MCP-parent links, state.db fields, unread counts, notices, `since`, and
directory filtering remain read-time overlays. This avoids caching state owned
by other registries/databases and makes seen/archive/pin changes visible without
a 4.33 s scan.

### Read semantics

1. A cold read starts or joins one full refresh and waits. If it fails, return
   the current error because no successful snapshot exists.
2. A fresh warm read copies the snapshot reference under a read lock and
   returns without I/O.
3. A stale or dirty warm read returns the last good snapshot immediately and
   starts or joins background work. Failure retains the snapshot and dirty
   indication so a later event/reconciliation retries.
4. A successful rebuild swaps the complete slice and ID index under one lock.
   Readers never observe a partial merge.
5. Refresh completion emits the existing `ocman.session.changed` invalidation
   so clients that read during revalidation fetch the completed snapshot.

The refresh goroutine uses a server-lifetime context, not the HTTP request
context that happened to notice staleness. The cache never returns its mutable
slice to overlay code; `Adapter.Sessions` continues to shallow-copy rows before
settling status and prompts.

### Exact response pipeline

For each adapter, preserve this order:

1. Read the global DB snapshot.
2. Filter exact `directory == dir`, then apply `since`.
3. Settle live status and filter inactive OpenCode children.
4. Apply prompt bubbling, live-connection flags, platform identity, and MCP
   parent links.
5. On the hub, merge local and remote rows, force-include pinned misses exactly
   as today, sort by recency, and apply `limit`.
6. Apply archive/seen/pin/unread state and notices.
7. For notify, apply its current eligibility predicate and projection last.

An absent directory produces a non-nil empty slice, so JSON remains `[]`.
`since`/`limit` parsing and boundaries stay in the current handlers.

## Incremental aggregation gate

Before enabling per-session refresh, record real OpenCode mutations for create,
rename, move, share, delete, message create/update/delete, part
create/update/delta/delete, shell synthesized-terminal state, status, and
prompt lifecycle. For every event verify:

- the payload identifies the affected session;
- row replacement/deletion behavior and stable IDs are known;
- whether `session.time_updated` advances;
- reconnect snapshots expose changes missed while disconnected;
- one-session recomputation matches the corresponding row from a clean full
  scan for every aggregate/status input.

When proven, `GetSessionSummary(id)` uses the same expressions as
`GetSessions`; a missing row deletes the cached session. Dirty IDs are keyed by
platform plus session ID. Events that cannot be assigned safely mark the full
snapshot dirty. The periodic/reconnect full scan remains mandatory to catch
missed events and removals, so correctness never depends solely on timestamps.

If the audit cannot guarantee exact incremental updates, do not approximate.
Ship global SWR plus coalesced full reconciliation, then evaluate a measured
indexed/batched alternative. The 3.65 s grouped query and 7.11 s project query
are explicitly rejected as defaults.

## Event flow and coalescing

```mermaid
sequenceDiagram
    participant OC as OpenCode /global/event
    participant W as existing watcher/Tee
    participant C as adapter snapshot
    participant H as hub /api/events
    participant Q as browser queries

    OC-->>W: session/message/part/status/prompt event
    W->>C: dirty(platform, sessionID)
    W-->>H: ocman.session.changed
    C->>C: debounce; exact per-session refresh
    C-->>H: ocman.session.changed after atomic swap
    H-->>Q: existing browser SSE event
    Q->>Q: patch safe fields; invalidate session/notify queries
```

Dirty session IDs use a trailing debounce of at most 100 ms and a 1 s maximum
wait. A timer firing drains the set once; arrivals during refresh populate a
new set and cause one follow-up drain. Thus 100 updates for one session run at
most one refresh concurrently, the last event starts work within 100 ms after
the burst, and a continuous stream cannot postpone work indefinitely.

Status and complete prompt/state snapshots may be patched immediately because
they are last-write-wins. Notification edges still trigger the existing notify
consumer before coalescing; they are not represented only by a lossy dirty bit.
The broadcast hub's internal coalescing key becomes
`event + platform + sessionID` (or owner + project root) without changing its
JSON/SSE wire. A full subscriber buffer parks the latest invalidation per key;
slow clients reconcile on reconnect/fallback.

### Required event coverage

- OpenCode: session create/update/delete, title/directory/share/timestamps,
  message and part mutations, status/idle, permission and question ask/resolve.
- Ocman: create/fork/move/rename, successful seen/archive/unarchive/pin/unpin,
  project archive/unarchive, automatic unarchive, and prompt/queue transitions
  that affect list/notify presentation.
- Lifecycle: OpenCode stream connect/reconnect/port disappearance, remote gRPC
  reconnect, and browser `/api/events` open/reopen.

Every successful mutation broadcasts after persistence/upstream success;
failed mutations do not. Existing mutation response bodies/statuses remain
unchanged.

## Local and remote protocol

Local events enter through the existing one-per-OpenCode-instance watcher. The
owning ocman updates its own adapter cache first; the hub never reads a remote's
SQLite database.

For attached remotes:

1. Add internal gRPC `WatchInvalidations(PlatformRef) returns (stream
   Invalidation)`. `Invalidation` carries a base platform, kind, session ID or
   project root, and no user data. It is advisory, not a durable event log.
2. The remote streams the same invalidations its local browser broadcast path
   receives. It emits both dirty and refresh-complete signals.
3. The manager keeps one stream per connected remote, stamps the compound
   platform/remote identity, invalidates only that `remotePlatform`, and
   rebroadcasts the existing browser event shape. Browser code remains
   host-agnostic and invalidates shared queries rather than matching bare IDs.
4. Implement existing `WatchProjects` to stream refreshed project inventory
   snapshots. The hub atomically replaces only that owner's inventory.
5. On either stream reconnect, trigger remote session and project
   reconciliation before treating the connection as current. One unavailable
   remote remains bounded by existing connection/fan-out timeouts and cannot
   delay local or other owners.

`Sessions`, public REST, and browser SSE formats do not change. Protocol version
is bumped because old peers lack the new stream; compatibility follows the
existing Hello version check rather than adding dual behavior.

## Project refresh singleflight and dirty bit

Keep `db.GetProjects` because the grouped rewrite regressed 5.82 s to 7.11 s.
Add refresh state per owner: `running`, `dirty`, and one completion channel.

```text
request refresh:
  lock
  dirty = true
  if running: join current completion
  else running = true; start worker

worker:
  lock; dirty = false; unlock
  run GetProjects/remote Projects
  on success atomically replace that owner's snapshot
  lock
  if dirty: unlock and run exactly one follow-up iteration
  else running = false; close completion; unlock; stop
```

Events during a follow-up apply the same rule. On failure, stop, retain
`dirty=true`, close current waiters with the error, and let the next event/tick
retry; never spin indefinitely. At most one query runs per owner, ten callers
join one cycle, and completion of an old query cannot clear a newer event.
Local state lives in `projectsIndexState`; remote owner state lives in the
existing manager inventory map. No generic scheduler package is introduced.

## Reconnect, reconciliation, and fallback

- Cold startup performs one full session and project load.
- OpenCode watcher reconnect starts a coalesced full session/project
  reconciliation; completion broadcasts invalidation.
- Remote stream reconnect refreshes only that remote's sessions/projects.
- Browser `/api/events` `onopen` invalidates session, project, and notify
  queries. Concurrent reconnect/visibility signals use TanStack's existing
  per-key in-flight dedup.
- A slow backend reconciliation runs at most once per 3-minute fallback period
  and is singleflighted. This catches event loss even if no browser is open and
  ensures the next visible fallback read sees a completed snapshot.
- Only after the event-coverage gate passes, dashboard, project, sidebar, and
  notify intervals become 3 minutes. Hidden tabs issue none; visibility return
  and SSE reopen invalidate immediately. TanStack's normal focus/background
  behavior is reused rather than adding another polling framework.
- `/api/system/stats` and frontend memory sampling stop while hidden, retain the
  last values, load immediately on visibility, then resume 5 s polling.
- Beads, MCP, conversation, subagent preview, workflow, relay, and other
  excluded polling remain untouched.

## Forge preview requests

Use one module-level map in `githubPreview.ts`, keyed by normalized
`URL.origin + URL.pathname` after validating the resource. Each entry holds the
last success, expiry, and optional in-flight promise. GitHub and Forgejo hosts
therefore cannot collide. Concurrent loads/refreshes join the promise; failures
clear it and do not replace a prior success. Expired success data renders
immediately while a visible card revalidates. Keep the current 5 s visible-card
cadence and immediate intersection refresh. Cap the map at 100 entries with
oldest-success eviction; no dependency is needed.

## Test plan

### Backend

- Golden JSON: old versus snapshot-backed `/api/sessions` and notify for local,
  remote, duplicate IDs, prompts/descendants, status, state, pinning, archive,
  `dir`, `since`, and `limit`; assert `[]` and `omitempty` behavior.
- Cache: cold wait/error, warm hit, stale non-blocking hit, atomic replacement,
  failed refresh retention/retry, concurrent stale readers, and zero additional
  scans for many directories.
- Incremental: failing-first fixtures for every audited mutation; compare each
  updated row and final snapshot against a clean full scan, including deletion,
  move, synthesized terminal, totals, duration, error fields, and child filter.
- Events: table-test every OpenCode type through `Tee` to qualified dirty ID and
  browser broadcast; 100-event burst, independent identities, max-wait, event
  during refresh, and notification edges.
- Projects: ten joiners, event-during-run dirty follow-up, follow-up-during-run,
  failure retention, and independent owners.
- Remote: invalidation and `WatchProjects` round trips, duplicate bare IDs,
  remote reconnect reconciliation, unavailable remote isolation, and protocol
  mismatch.
- Run cache/coalescing/project/reconnect tests under `go test -race`.

### Frontend and end-to-end

- Query invalidation on session/state/project events and SSE `onopen`; verify
  reconnect and visibility signals deduplicate.
- Notify timing/dedup/dismissal remains unchanged for the same source events.
- Fake timers prove one visible 3-minute fallback per endpoint, zero hidden
  polls, immediate visible refresh, and unchanged excluded polling.
- Two tabs converge after state mutation; hub/remote converges after both link
  reconnects; deliberately drop one event and converge by fallback.
- Preview tests cover normalized keys, host isolation, one in-flight request,
  stale success during revalidation, retry after error, and 100-entry bound.

Existing dashboard, project, sidebar, favicon, bell, toast, OS notification,
archive, pin, prompt, multi-remote, and e2e suites remain behavior assertions;
only old interval-specific timing changes.

## Rollout and commit plan

Each commit is independently testable and preserves current polling until the
coverage gate:

1. Add metrics/benchmark harness and golden response fixtures; no behavior
   change.
2. Replace directory caches with one adapter-owned global SWR snapshot; retain
   current SQL, refresh cadence, REST/SSE, and frontend polling.
3. Make notify consume the same snapshot and add project refresh
   singleflight/dirty follow-up.
4. Complete the event audit/parser and enable exact per-session recomputation
   only when full-scan equivalence passes; otherwise retain coalesced full
   refresh.
5. Add post-success state mutation broadcasts and qualified coalescing.
6. Add remote invalidation/`WatchProjects` streams, protocol bump, and reconnect
   reconciliation.
7. Add frontend reconnect/visibility reconciliation. After automated coverage
   proves every FR-5 source, change eligible polls to 3 minutes and pause stats
   while hidden.
8. Add the minimal forge preview in-flight/bounded cache.
9. Run the 12 GB release benchmark and behavior suite; keep any stage that
   meets equivalence independently, and do not land polling reduction if event
   coverage fails.

## Measurable validation

Using the same warmed 12 GB copy for at least 10 minutes, record request count,
p50/p95/max, payload bytes, full/per-session/project scan count and duration,
cache hit/stale/error counts, coalesced events, dirty follow-ups, and CPU.
Compare one local platform and 10 remotes with one unavailable against the
current baseline.

Release gates are the requirements' targets: warm sessions p95 <=100 ms and
max <=250 ms; local notify p95 <=50 ms and max <=150 ms; directory overhead
<=10 ms with zero scans; at least 90% fewer full scans and burst refresh starts;
no overlap or sustained 50% scan duty cycle; proportional work for one-session
updates when enabled; at most one visible fallback per endpoint per 3 minutes;
zero hidden eligible polls; payload growth <=1%; and exact golden/e2e behavior.

If these numbers are missed, inspect invocation/phase metrics first. Do not
replace the SQL with either measured grouped rewrite unless a new benchmark on
the same data demonstrates a material win and exact output.
