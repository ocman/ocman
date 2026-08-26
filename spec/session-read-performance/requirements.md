# Requirements: Session read performance at large database scale

## Problem

Ocman's session-list read path does not scale to a 12 GB OpenCode database.
`GetSessions` derives every session row with repeated correlated aggregates over
the message and part tables. The global dashboard, project views, and
`/api/sessions/notify` all depend on this result, while global and
directory-scoped cache entries can each trigger another full aggregate scan.

Observed baselines show the scaling problem:

- A full session aggregate scan on the 12 GB database takes approximately 5 s.
- The existing refresher can spend up to 50% of its time repeating that scan.
- On the earlier representative database, the aggregate cost 250-290 ms;
  `/api/sessions` measured 198 ms average, 522 ms p95, and 1,340 ms maximum.
- `/api/sessions/notify` measured 293 ms average, 310 ms p95, and 1,854 ms
  maximum despite returning only a small projection.

The application already receives live events, but event coverage is not yet
complete enough across local state changes, remotes, reconnects, and event loss
to replace frequent polling safely. This work must reduce database and network
load without changing anything a user can observe.

## Goals

1. Make cached session-list reads non-blocking while fresh data is rebuilt in
   the background.
2. Derive directory-scoped lists from the global session cache instead of
   issuing directory-specific full aggregate scans.
3. Make `/api/sessions/notify` a lightweight read while preserving its exact
   request, response, filtering, ordering, and notification semantics.
4. Avoid repeated full aggregate scans by maintaining per-session or
   incremental aggregates when the OpenCode schema and event stream provide a
   correct implementation path.
5. Make local and remote SSE the primary invalidation path, with reconciliation
   after reconnect and a slow visible-tab poll only as an event-loss fallback.
6. Coalesce bursts without losing the final state, and bound concurrent refresh
   work.
7. Stop unnecessary hidden-tab work and deduplicate forge preview requests
   without making previews visibly stale.

## Non-goals

- Any user-visible change to session rows, project grouping, status, ordering,
  filters, archive/seen/pin behavior, prompts, notifications, or freshness.
- Changes to OpenCode's database schema, HTTP API, or event wire format.
- Changes to ocman's public REST or SSE wire formats.
- Changes to Beads availability/status polling.
- Reworking per-session conversation SSE, message pagination, subagent preview
  polling, workflow polling, or relay polling unless required to emit a missing
  session-list invalidation.
- Persisting the session read cache across ocman restarts.
- Introducing approximate token, cost, message-count, status, prompt, or unread
  values.
- Raising database connection-pool limits to hide repeated scans.

## Functional Requirements

### FR-1: One global session snapshot

The local OpenCode adapter shall maintain one authoritative in-memory global
session snapshot. Directory-scoped reads shall filter that snapshot by exact
directory equality and then apply the existing `since`, sort, limit, child
filtering, live-status, prompt, state, and notice rules in their current order.
They shall not create or refresh separate database aggregate caches per
directory.

Acceptance criteria:

- After the global snapshot is available, requests for any number of distinct
  directories cause zero additional full session aggregate scans.
- Global and directory responses contain the same rows and field values, in the
  same order, as the current implementation for an unchanged database and live
  state.
- A directory absent from the snapshot returns the current empty JSON shape,
  not `null`, an error, or a synthetic project.

### FR-2: Stale-while-revalidate reads

Once a successful snapshot exists, `/api/sessions`, project-scoped session
reads, and `/api/sessions/notify` shall serve the last good snapshot immediately
when it is stale and start or join a background refresh. A slow, busy, or failed
refresh shall not evict the last good snapshot or turn an otherwise serviceable
read into an error. Cold startup may wait for the first snapshot and shall keep
the current error behavior if no successful data exists.

Acceptance criteria:

- An expired warm cache never waits for the approximately 5 s aggregate scan.
- All concurrent stale reads join one refresh and receive responses from the
  same last good snapshot.
- A failed refresh preserves the previous snapshot and remains retryable.
- A successful refresh replaces the snapshot atomically; no response can
  observe a partially rebuilt list.

### FR-3: Lightweight notify projection

`GET /api/sessions/notify` shall read from session summary/cache state that does
not require a full session aggregate scan. Its wire and notification semantics
shall remain exact.

Behavior-equivalence criteria:

- `since` and `limit` retain their current defaults, parsing, and boundary
  behavior.
- Local and remote rows are merged, sorted by recency, and limited before the
  notification eligibility filter, exactly as today.
- A row is returned exactly when it has a pending permission/question or is an
  unseen `waiting`/`error` session.
- Every returned object has the same JSON field names, values, and `omitempty`
  behavior for `id`, `status`, `seen`, `pendingPermission`, `pendingQuestion`,
  `title`, and `directory`.
- Empty results serialize as `[]`; status settlement, top-level bubbling of
  descendant prompts, state overlays, and remote platform identity are
  unchanged.
- Favicon, title, bell, toast, and OS notification timing, deduplication, and
  dismissal behavior remain unchanged when the same source events occur.

### FR-4: Incremental or per-session aggregation

The implementation shall first verify whether OpenCode's available row keys,
timestamps, update behavior, and events can identify every session whose
aggregate may have changed. If they can, aggregate refreshes shall recompute
only changed/new/deleted sessions and merge them into the global snapshot. A
periodic or reconnect reconciliation shall detect missed changes and removals.

If correctness cannot be guaranteed from the available architecture, this
requirement may use the minimum measured alternative, such as indexed batched
aggregation, but it must still meet the performance criteria below. It shall
not infer or approximate user-visible values.

Acceptance criteria:

- Creating, updating, deleting, sharing, or changing messages/parts for one
  session updates that session without recomputing aggregates for unchanged
  sessions when incremental invalidation is supported.
- Token totals, cost, message count, last-message error/finish fields,
  synthesized-terminal detection, duration, and settled status match a clean
  full calculation after each tested mutation.
- Startup and reconciliation produce the same snapshot as a clean full
  calculation.
- The chosen strategy and benchmark evidence demonstrate the performance
  targets against a representative copy of the 12 GB database.

### FR-5: Complete SSE invalidation before poll reduction

The 5 s dashboard/project polling and 10 s notify polling shall not be changed
to the 3-minute fallback until all session-list and notification-affecting
changes have a live invalidation path. Coverage includes OpenCode-originated
session/message/part/status/prompt changes and ocman-originated state mutations.

For local changes, the owning ocman shall invalidate/patch its snapshot and
broadcast to every connected browser. For remote changes, the remote shall
stream equivalent invalidations to the hub; the hub shall update the correct
compound platform/host entry and rebroadcast through its existing browser SSE
connection. The frontend shall remain host-agnostic.

Acceptance criteria:

- New sessions and changes to title, directory, timestamps, status, share
  state, prompts, and notification eligibility appear in dashboard, project,
  sidebar, and notify consumers without waiting for a fallback poll.
- Local and remote session IDs cannot invalidate each other's rows, including
  when their unqualified IDs are identical.
- One slow or disconnected remote does not delay local or other remote reads.
- No poll interval is reduced until automated coverage proves all listed event
  sources and state mutations reach local and remote browser clients.

### FR-6: State mutation broadcasts

Successful mutations that alter a session/project presentation shall update
cached state and emit an SSE state invalidation after persistence succeeds.
This includes marking seen, archiving/unarchiving a session, pinning/unpinning,
archiving/unarchiving a project, automatic unarchive, and session creation or
movement. Failed mutations shall not broadcast success.

Acceptance criteria:

- The initiating tab and a second connected tab converge without polling.
- The same behavior works when the mutation is owned by an attached remote.
- Seen/unseen notification changes update all notify consumers without a
  second user action.
- Existing mutation HTTP status codes and response bodies are unchanged.

### FR-7: Event coalescing and debounce

Bursts of invalidations shall be coalesced by fully-qualified session or
project identity. Coalescing may delay refresh briefly, but shall be
last-write-wins only for state snapshots; edge-triggered behavior required for
notifications shall not be lost. The final state after a burst must always be
processed.

Acceptance criteria:

- A burst of at least 100 updates for one session starts no more than one
  concurrent refresh and results in the final authoritative state.
- Independent session/remote identities are not incorrectly collapsed.
- The configured debounce adds no more than 250 ms from the final event to the
  start of refresh while the tab and connection are active.
- A continuously busy stream cannot postpone reconciliation indefinitely.

### FR-8: Project refresh singleflight with dirty follow-up

Project inventory refreshes shall be singleflighted. If one or more project-
affecting events arrive while a refresh is running, the refresh shall be marked
dirty and exactly one follow-up refresh shall run after the current one. Events
arriving during the follow-up apply the same rule.

Acceptance criteria:

- At most one project refresh runs at a time per owner.
- Ten concurrent callers share one result.
- Any event received after a refresh starts is represented by that result or by
  one dirty follow-up; it is never silently cleared by completion of the older
  refresh.
- Failures leave refresh retryable and do not discard the dirty indication.

### FR-9: Reconnect, event loss, and fallback polling

Opening or reopening the global SSE stream shall trigger reconciliation of
sessions, projects, and notify state because events may have been missed before
or during the gap. After FR-5 is satisfied, dashboard, project, and notify
polling shall become a 3-minute fallback while the document is visible. The
fallback shall pause while hidden and run immediately when visibility returns
or SSE reconnects.

Acceptance criteria:

- A local event missed during disconnect is reflected within one reconciliation
  round-trip after reconnect.
- A remote event missed during either the remote-to-hub or hub-to-browser gap
  is reflected after that link reconnects and reconciliation completes.
- If an SSE event is deliberately dropped while all connections remain open,
  visible clients converge within 3 minutes.
- Hidden tabs issue no dashboard, project, or notify fallback polls and refresh
  immediately on becoming visible.
- Multiple reconnect/visibility signals coalesce rather than fan out duplicate
  reads.

### FR-10: Pause system statistics while hidden

The `/api/system/stats` timer and associated frontend memory sampling shall
pause while `document.hidden`. It shall refresh immediately when the document
becomes visible and then resume its existing visible interval. Displayed values
shall remain at their last successful values while hidden.

### FR-11: Preview request deduplication and bounded freshness

Concurrent requests for the same normalized GitHub or Forgejo preview URL shall
share one in-flight request and one bounded in-memory cache entry. Visible
preview cards shall continue to revalidate on their existing 5 s cadence and
immediately when entering the viewport; stale data may remain visible while
revalidation runs. Errors shall not be cached indefinitely.

Acceptance criteria:

- N simultaneous cards for one URL cause one backend request per refresh cycle.
- A successful cached preview renders immediately and is silently revalidated
  when visible.
- A transient failed request can recover on a later refresh without reload.
- Cache keys do not mix distinct forge hosts/resources, and cache growth is
  bounded by an explicit entry limit or expiry policy.

## Performance Requirements

Measurements shall use the same 12 GB OpenCode database copy, warmed OS page
cache, one local platform, and the documented dashboard/project/notify request
mix. Results shall report request count, p50, p95, maximum, aggregate scan
count/duration, and CPU over at least 10 minutes; remote tests shall additionally
cover 10 attached remotes with one unavailable.

- Warm `/api/sessions` p95 shall be at most 100 ms and maximum at most 250 ms,
  excluding the existing bounded remote fan-out timeout.
- Warm `/api/sessions/notify` p95 shall be at most 50 ms and maximum at most
  150 ms locally.
- A directory-scoped warm read shall be at most 10 ms slower than the equivalent
  global cached read and cause no aggregate scan.
- Stale warm reads shall meet the same response targets while revalidation runs.
- Full aggregate scan count during a 10-minute visible idle dashboard run shall
  fall by at least 90% from the current refresher behavior, with no overlapping
  scans and no sustained 50% scan duty cycle.
- One session update shall process work proportional to that session's rows,
  not the 12 GB database, when incremental aggregation is supported.
- A 100-event burst shall produce at least a 90% reduction in aggregate/project
  refresh starts versus one-refresh-per-event behavior.
- The visible steady state shall make at most one fallback request per endpoint
  per 3-minute interval, except explicit user refreshes, reconnect
  reconciliation, and event-triggered invalidation.
- Hidden steady state shall make zero dashboard, project, notify, and system
  stats polling requests.
- Response payload sizes shall not increase by more than 1% for identical data.

## Behavior-Equivalence and Test Requirements

- Golden tests shall compare old and optimized `/api/sessions` and
  `/api/sessions/notify` JSON for identical local, remote, state, prompt,
  status, archive, pin, seen, `since`, `dir`, and `limit` fixtures.
- Tests shall cover cold start, warm hit, stale hit, refresh success/failure,
  concurrent reads, creation, update, deletion, directory movement, and restart.
- End-to-end tests shall cover two local browser tabs, a hub plus remote, remote
  disconnect/reconnect, browser SSE reconnect, deliberately dropped events,
  hidden/visible transitions, and an unavailable remote.
- Existing dashboard, project, sidebar, favicon, bell, toast, OS notification,
  archive, seen, pin, prompt, and multi-remote tests shall pass unchanged unless
  timing-only assertions are updated from the old intervals.
- Race-enabled tests shall show no data races in cache refresh, dirty follow-up,
  coalescing, or reconnect paths.
- No optimization is accepted solely from a synthetic small database; the 12 GB
  benchmark and behavior comparison are release gates.

## Release Criteria

The work is complete only when live invalidation is proven end-to-end before
the polling reduction, the performance targets are met on the 12 GB database,
all behavior-equivalence tests pass, and Beads/MCP polling is demonstrably
unchanged.
