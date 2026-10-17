# Telemetry-driven audit

Date: 2026-10-08. Code compared: `origin/main` at `f8cc0c454`.

## Summary

The live telemetry shows that most of ocman's waiting and load comes from background work that the user never asked for. The sidebar's 3–4 second session-list stalls are mostly self-inflicted. Deleted internal sessions force full rescans of the session list. An OpenTelemetry gauge recomputes lifetime statistics every 30 seconds. Two sidebar polls download about 6.6 GB a day between them.

Most fixes are small and local. The first five items below remove the bulk of the measured cost.

## Method and limits

Sources: Prometheus metrics, Tempo traces, Loki logs from the local Grafana LGTM stack, plus read-only inspection of `state.db` and `origin/main` code. Three read-only investigations covered HTTP and polling, database/cache/runtime, and logs/error spans. The strongest claims were re-checked against code and queries in the main pass.

Limits:

- Only one process reports: `service_name="ocman-dev"`, the `make dev` instance at `:8229`. Restarts from air rebuilds (about 15 in 7 days) are expected and excluded.
- Loki only holds data from 2026-10-07. Tempo retained about 40 minutes of spans, and its counts are about 40% of Prometheus counts, so Tempo is used for individual traces and ratios only.
- Pyroscope has no ocman profiles. CPU and allocation attributions are inferred from query counts and allocation rates.
- The forge HTTP client emits no spans and the forge handlers log nothing, so the PR 502s could not be diagnosed.
- Two clients were active during the day, desktop Chrome and an iPad. Overnight rates match exactly one visible client per poll interval.

Detailed notes per area:

- HTTP and polling: `ocman-telemetry-http.md`
- Database, caches, runtime: `ocman-telemetry-db-runtime.md`
- Logs and error spans: `ocman-telemetry-logs.md`

## Ranked findings

| # | Finding | Measured cost | Fix size |
|---|---|---|---|
| 1 | Deleted internal sessions force full session-list scans | 513 warnings/day; 3–4 s `/api/sessions` stalls | Small |
| 2 | The stats metric gauge syncs the analytics mirror every 30 s | ~2.6 h of SQLite per day for one query shape | Small |
| 3 | `/api/sessions` is slow and refetched in undebounced bursts | p50 0.6–1.0 s, p95 3.3–4.6 s, 2.9 GB/day | Small/medium |
| 4 | The sidebar polls the full Factory epics list every 15 s | 506 KB per call, 3.7 GB/day, ~129 queries per request | Small/medium |
| 5 | OpenCode calls that wait for an LLM hit a 10 s client timeout | ~40% of worktrees keep a provisional branch name | Small |
| 6 | Projects index refresh runs ~400 serial `git remote -v` | Mean 4.8 s, 518 runs/day, 2,500 s/day | Small/medium |
| 7 | Routine tick scans `routine_run` every 5 s on the single state.db connection | 133k pool waits/day, 1,428 s waiting | Small |
| 8 | The frontend cancels its own requests (499s) | ~1,200 cancelled requests/day | Small |
| 9 | PR checks without CI never settle and poll every 5 s | 0.2–0.8 rps from one client; 502s invisible | Small |
| 10 | Browser SSE reconnect storm against a dead OpenCode port | 45 refused-connection bursts, 0.65 s apart | Small |
| 11 | Every streamed token copies and re-sorts the session snapshot | 12.9 summary queries/s; 55–74 MB/s allocations when active | Small |
| 12 | Smaller work-after-the-fact items | See below | Small each |
| 13 | Log and span hygiene | See below | Small each |
| 14 | Development runs use the live state.db | ~20 migration backups of the live database | Small |

## 1. Deleted internal sessions force full session-list scans

ocman creates short-lived OpenCode sessions for the auto-approve judge and worktree naming, then deletes them. When the event stream reports one, the watcher refreshes that session's row. The row query returns `ErrSessionNotFound` on purpose, because parentless `… subagent)` sessions are hidden from the list. The watcher treats that as a failure. It logs a warning, forgets that it has seen the session, and calls `InvalidateSessionsCache()`. The next `/api/sessions` request then blocks on a full scan, and the forgotten session repeats the cycle on its next event.

**Evidence:**

- 513 `failed to refresh new|renamed session` warnings in 24h, all "session not found", across about 150 session IDs.
- A warning at 1791412308.733 was followed 5–70 ms later by six `/api/sessions` requests from both clients, each about 3.7 s. In trace `ec8b989c…`, 3,234 ms of 3,712 ms is waiting on that scan. Another case: trace `f5c41bb5…`, scan started 4 ms after the warning.
- Full-scan query `84d184d9` ran about 500 times a day, against an intended ~12 per hour after PR #801.

**Code:** `internal/autoapprove/watcher_session_events.go:55-66` (handler), `internal/db/sessions.go:181` (subagent rows hidden), `GetSessionSummary` returns `ErrSessionNotFound`.

**Fix:** treat `ErrSessionNotFound` as final. Keep the session marked seen, drop any snapshot row for it, skip the invalidation and broadcast, and log at Debug. Apply the same rule in the rename path.

**Validation:** a regression test with a refresh returning `ErrSessionNotFound` asserts no invalidation and no repeat refresh. In telemetry, the warning count should drop to near zero and full scans to the reconciliation rate.

## 2. The stats metric gauge syncs the analytics mirror every 30 seconds

The OTel callback for the six `ocman.stats.*` gauges calls `s.db.GetStats(ctx)` on every export. `GetStats` first syncs the analytics mirror, whose staleness bound is 5 seconds, so every export triggers a sync. Each sync copies tool timings with a query whose window can span about 14 hours, because unfinished (interrupted) turns hold the window start open.

**Evidence:**

- Exactly 120 runs per hour, overnight too.
- Query `e2a45b18` (`copyToolTimings`) used 9,508 seconds of database time in 24h, the largest single shape. Mean 3.5 s, p95 7.4 s. The whole cycle is about 54% of all opencode.db time.
- 18 `stats metrics: failed to query stats` deadline errors and 18 mirror sync cancellations in 24h. When a sync fails, `analytics()` falls back to a full count over OpenCode's database on an already-expired context.

**Code:** `internal/server/metrics_stats.go:72-84`, `internal/db/stats.go:83`, `internal/db/analytics_mirror.go:53` (5 s staleness), `:63` and `:242` (window), `:165-171` (fallback), `internal/db/throughput.go:106`.

**Impact:** affects installs with OpenTelemetry enabled. It competes for the database with user-facing session scans (finding 1) and occupies about 16% of a core.

**Fix:** serve the gauges from the last computed stats, refreshed on a slower cadence (for example every few minutes) outside the export callback. Cap the unfinished-turn look-back so an interrupted turn cannot keep the copy window open. On sync failure, serve the stale mirror instead of the full fallback.

## 3. `/api/sessions` is consistently slow and refetched in bursts

Beyond the spikes from finding 1, the endpoint is slow on every call. Only 26 of 6,770 requests finished under 100 ms. It used 7,785 handler-seconds a day and returns about 410 KB per call.

Two causes:

- **Pinned sessions outside the time window load a full transcript** to read summary fields. In trace `2e0f0f11…`, about 415 ms of 520 ms is that loop. Code: `internal/server/handlers_sessions.go:38-57` calls `adapter.Session(ctx, id, 0, 0)` per pinned session.
- **Refetches are not debounced.** Overnight the rate matches the 3-minute sidebar poll exactly (0.0056 rps). During the day it is about 35 times higher, driven by `session.changed` events, arriving in bursts of 2–3 identical requests per client within the same millisecond. Code: `frontend/src/App.tsx:348`, `frontend/src/pages/session-detail/useSidebarSessions.ts:210-235`.

**Fix:** read pinned sessions through the existing single-row summary query instead of the transcript. Coalesce `session.changed` refetches (for example a short trailing debounce, and a single in-flight request per key).

## 4. The sidebar polls the full Factory epics list every 15 seconds

The session sidebar fetches `/api/factory/epics` every 15 seconds only to collect attempt session IDs. Each response is about 506 KB, totalling 3.7 GB a day, the largest byte producer. Each request runs about 129 queries (5 per epic).

**Code:** `frontend/src/pages/session-detail/SessionSidebar.tsx:131`; backend per-epic loads around `internal/server/factory_epic.go:148`.

**Fix:** a small endpoint or query parameter that returns only the attempt session IDs, and batch the per-epic loads with `IN (…)`. The factory dispatch tick (`native_dispatch_events.go:13`) makes the same per-epic round trips every 15 seconds and benefits from the same batching.

## 5. OpenCode calls that wait for an LLM hit a 10-second timeout

`openCodeClient` has a fixed 10 s timeout. Calls that wait for a model reply go through it, so their own larger context budgets never apply.

**Evidence:** 25 worktree-naming failures out of 63 worktree starts; 21 are the 10 s client timeout. Those worktrees keep the provisional `session-<id8>` branch. `POST /session/{id}/command` uses the same client; three slash commands showed "Failed to execute command" in the UI while the command was probably still running.

**Code:** `internal/platforms/opencode/client.go:25-26`, `internal/platforms/opencode/http.go:208-215` (`postJSONReturning`), `internal/platforms/opencode/operations.go:507`.

**Fix:** use a client without a total timeout for these calls and let the request context bound them. Keep the 10 s client for ordinary reads.

## 6. Projects index refresh shells out ~400 times serially

The periodic projects index runs `git remote -v` for each known directory, one after another. 119 of the 408 directories no longer exist and are retried on every run. The cache TTL equals the 5-minute tick, so every tick rebuilds.

**Evidence:** 518 refreshes a day, mean 4.8 s, 2,500 s/day. Trace `de76bfc5…`: 11.3 s total, 0.94 s of it SQL; the rest is subprocesses, with no span covering them.

**Code:** `internal/remote/inventory.go:62-79,104`.

**Fix:** cache missing directories, make the TTL longer than the tick, run lookups with bounded parallelism, and add a span around the git calls.

## 7. Routine tick holds the single state.db connection every 5 seconds

`ListRunningRoutineRuns` runs `WHERE state = 'running'` every 5 seconds. Existing partial indexes add `session_mode <> 'new'`, so SQLite cannot use them for this query. It scans all 686 rows and reads about 12 MB of stored prompt text to return nothing. state.db has one connection, so every other state read waits.

**Evidence:** fingerprint `3390c5bc` is 68% of state.db query time. 133k connection waits in 24h, 1,428 s total waiting.

**Code:** `internal/state/routine_runs.go:149`, called from `internal/routines/service.go:223`.

**Fix:** a partial index on `routine_run(created_at, id) WHERE state = 'running'`, added through a migration.

## 8. The frontend cancels its own requests

About 1,200 requests a day end as 499 (client cancelled). They are not caused by server slowness:

- `/api/inbox` (about 456/day; median latency 37 ms): `ocman.inbox.changed` fires on every permission ask and resolve, often milliseconds apart under auto-approve. Each event calls `invalidateQueries`, which aborts the request in flight.
- `/api/sessions/notify` (about 323/day): `fetchNotify` aborts its previous request on each resolve event.
- On the iPad, Safari drops fetches when the tab is backgrounded; these are logged as `BackendUnavailableError`.

The session-list scan intentionally continues after a cancel (`WithoutCancel`), so the waste is duplicate requests rather than runaway work.

Related: `/api/inbox` loads a full live session just to obtain its title (`internal/server/inbox_sessions.go:33`), plus a 138 ms defaults query (trace `dcdb67df…`). This runs every 10 seconds per client and on every inbox event.

**Fix:** coalesce event-driven invalidations, using `cancelRefetch: false` where an in-flight request is already current. Read the inbox title from the session summary.

## 9. PR checks without CI poll forever, and 502s are invisible

`isSettled` requires at least one check, so a PR with no CI never settles and is re-polled every 5 seconds while visible. Errors also retry every 5 seconds without backoff. All 0.2–0.8 rps of this traffic came from one client.

The forge handlers turn every error into 502, including client cancellations, and log nothing. Failed responses complete in about 100 ms versus 240 ms for successes, which suggests many are aborts. This is an inference; the cause could not be confirmed.

**Code:** `frontend/src/lib/prChecksCache.ts:20`; forge handlers in `internal/server`.

**Fix:** treat "no checks" as settled after one successful read, back off on errors, map cancellations to 499, and log or trace forge failures with the upstream status.

## 10. Browser SSE reconnect storm against a dead OpenCode port

When OpenCode restarts, the per-session SSE proxy's dial error is not wrapped as `ErrPlatformUnreachable`. The handler ends the stream with an empty 200 instead of a 503, and the browser reconnects immediately.

**Evidence:** 45 `connection refused` warnings in bursts of 3–16, about 0.65 s apart.

**Code:** `internal/platforms/opencode/events.go:50`.

**Fix:** wrap dial failures as `ErrPlatformUnreachable` so the existing 503 path and client backoff apply.

## 11. Every streamed token refreshes and re-sorts the session snapshot

Each message delta marks the session dirty and wakes the incremental refresher. Its pacing budget is about 1 ms, so `GetSessionSummary` runs about 12.9 times a second during activity, and each merge copies and re-sorts the roughly 7,700-row snapshot.

**Evidence:** 46k summary queries an hour while active. Allocation rate is 55–74 MB/s while active versus 4.9 MB/s idle; the link between the two is inferred, since no profiles exist.

**Code:** `internal/autoapprove/tee.go:665`, `internal/platforms/opencode/models_cache.go:519` (budget), `:950` (`mergeSessionRows`).

**Fix:** a 1–2 second floor on the incremental refresh interval during streaming. The sidebar does not need per-token freshness.

## 12. Smaller work that is done but never used

- `publishCompletedTurn` fetches the whole transcript on every idle edge before checking whether the session has any share links. That causes 75 `platforms: not found` warnings for the deleted internal sessions in finding 1. Return early when nothing is shared (`internal/server/share_relay.go:161-169`).
- The live-path upstream fetches take no request context, so abandoned requests still do the full work (`internal/platforms/opencode/client.go:249-254`). 190 of 220 session-defaults warnings are such cancels.
- The `session_defaults` cache never removes expired entries; it grew from 15 to 176 entries within one process (`models_cache.go:249`). Delete expired entries on write.
- Goroutines rose from 94 to 215 over 5 hours of activity while SSE connections stayed at 1. An idle process stayed flat. This is a watch item; a goroutine dump is needed before calling it a leak.

## 13. Log and span hygiene

- Span names use raw request paths except for `/api/session/`. Names therefore include `/api/file/<base64 path>.<signed token>`, artifact IDs and epic IDs (`internal/server/otel.go:67-101`). Using the route pattern fixes cardinality and keeps signed tokens out of trace storage. Dashboard auth still protects the data, so severity is low.
- Promote nothing, demote several: broken-pipe encode errors, per-row scan errors on cancel, stale tmux stop failures (`host_managed.go:139`), and permission replies for already-answered prompts are logged at Warn or Error and should be Debug.
- Auto-approve judge failures (10 of 91 runs) log no session ID.
- The auto-approve tee drops events over 4 MiB (24 drops in one 40-minute window) without logging the port, session or size.
- The frontend reports aborts (27) and backend restarts (89) as errors, and refetches deleted sessions (13 "session not found").

No secrets were found in log lines. Log volume is about 300 KB a day. About 99% of spans are SQL `reset_session`, `rows` and `query` spans; dropping those would cut trace volume sharply without losing request-level detail.

## 14. Development runs use the live state.db

The state database path is fixed at `~/.local/share/ocman/state.db` (`internal/state/db.go:41-43`) with no flag or environment override, and the analytics cache sits beside it. About 20 `state.db.backup-vNN-to-vMM` files show development builds migrating the live database. This contradicts the repository's database-isolation rule, and agents working in worktrees currently have no way to comply with it.

**Fix:** add a state-directory override (flag plus environment variable) and use a per-checkout directory in the development scripts. This also isolates `analytics-cache.db`.

## Checked and healthy

- OpenTelemetry collector: no refused, failed or dropped data; export queue peaked at 4.
- Heap: no steady growth within any process lifetime.
- `sessions_list` cache: 90% hit rate. `session_http`: 39%, expected with its 5 s TTL.
- No panics, nil-pointer errors, `database is locked`, decode errors or queue failures.
- Remote connections: no reconnect storm.
- Analytics mirror incremental syncs write only changed rows; large insert counts come from the 6-hourly and on-restart rebuilds.
- `invalid reference: main` worktree errors stopped after the base-verification fixes merged.

## Observability gaps worth closing

- No CPU or allocation profiles. Pushing Go profiles to the existing Pyroscope datasource would turn findings 2, 6 and 11 from inference into measurement.
- The forge client has no spans and its handlers log nothing.
- The projects-index git calls have no span.
- Tempo retention is too short for daily comparisons.

## Suggested order

1. Finding 1 (one condition in the watcher) and finding 7 (one index). Both are small with large measured effect.
2. Finding 2 (stats gauge cadence), then 11 (refresh floor). Together these remove most background database load.
3. Findings 3 and 4 (sidebar fetch shape and debounce). These cut about 6.6 GB a day of transfer.
4. Finding 5 (LLM-call timeout), then 6, 8, 9 and 10.
5. Finding 14 before the next round of agent-driven development, so parallel worktrees stop migrating the live database.

Success measures: `failed to refresh … session` warnings near zero; full-scan fingerprint `84d184d9` near the reconciliation rate; `e2a45b18` no longer on a 30-second cadence; `/api/sessions` p95 under 1 s; state.db pool waits reduced by an order of magnitude; 499s mostly gone outside backgrounded tabs.
