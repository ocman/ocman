---
title: Profiling
weight: 8
---

Status snapshot and roadmap for ocman's frontend/backend performance work.
This document records (a) the suspects we identified during recon,
(b) the instrumentation we added so we can measure them, and (c) the
prioritised list of fixes that *should* follow once we have data.

The starting symptom was: **the frontend occasionally feels "stuck"**,
and it wasn't clear whether the cause was slow API calls or the UI.

## Phase 1 — Reconnaissance findings (OpenCode-only)

ocman's default deployment runs only the OpenCode adapter
(`-platforms opencode`). Findings below assume that configuration.
Claude Code-specific concerns (jsonl scanner, 20-entry parse cache)
are documented separately in `spec/multi-agent-support/` and are
*not* in scope for this profiling effort.

### Polling map (steady state, idle session)

When a user is on the `SessionDetail` page, these all run
simultaneously:

| # | Source                                 | Cadence            | Notes                                              |
|---|----------------------------------------|--------------------|----------------------------------------------------|
| 1 | `App.tsx` → `useFaviconNotify`         | 10 s               | `GET /api/sessions/notify`                         |
| 2 | `App.tsx` → `useBellNotify`            | 10 s               | Independent poller hitting the same endpoint       |
| 3 | `App.tsx` → `BackendStats`             | 5 s                | `GET /api/system/stats`                            |
| 4 | `App.tsx` → `useMemoryMonitor`         | 30 s               | Local-only, fine                                   |
| 5 | `App.tsx` → `usePerformanceCleanup`    | 60 s (dev)         | Local-only, fine                                   |
| 6 | `SessionDetail` line ~1078             | 5 s                | Sidebar `recentSessions` refresh                   |
| 7 | `SessionDetail` line ~1752             | 10 s               | Fallback `GET /api/session/{id}` when SSE drops    |
| 8 | `SessionDetail` line ~2416             | 2 s                | While a Task tool is running                       |
| 9 | `SessionDetail` line ~2523             | 1 s                | Recompute live tokens/sec                          |
| 10| `SessionDetail` line ~579              | **0 ms** (≈250 Hz) | Polls Zustand for the palette command              |
| 11| `SessionDetail` SSE                    | streaming          | `GET /api/session/{id}/events`                     |

On the dashboard:

| # | Source                  | Cadence | Notes                                                       |
|---|-------------------------|---------|-------------------------------------------------------------|
| 12| `Dashboard.tsx` line 132| 5 s     | `GET /api/sessions` (full fan-out + git status, all the time) |
|   | + 1–5 above             |         |                                                             |

### Top suspects

The list is ordered by likelihood × impact for an OpenCode-only
deployment. The numeric IDs (S1, S2, …) are referenced from the
Phase 3 task list further down.

#### S1 — `setInterval(..., 0)` in `SessionDetail.tsx:579`

```ts
useEffect(() => {
  const interval = setInterval(() => {
    const cmd = useUiStore.getState().paletteCommand;
    if (!cmd || cmd.kind !== 'scoped') return;
    ...
  }, 0);
  return () => clearInterval(interval);
}, [tmux, archiveSession, createSession, navigate]);
```

`setInterval(0)` clamps to ≈4 ms per the HTML spec, so this fires
~250 times per second forever, just to read a Zustand store. The
dependency array also includes `tmux` (which probably changes on
parent re-renders), so the interval is also re-created frequently.

This is the strongest candidate for the "stuck" feeling — it can
saturate the event loop on a busy machine and fight against React
scheduling. Suggested fix: convert to a `useUiStore.subscribe`
selector or a normal `useEffect` keyed on `paletteCommand`.

#### S2 — `/api/sessions` cost on a 5 s loop

Per request, `handleSessions` does:

1. `db.GetSessions` — one SQLite query, indexed reads, fast.
2. `discoverOpenCodePorts` — runs `lsof` on macOS/Linux. **3 s TTL
   cache**, so the dashboard's 5 s poll cadence means roughly every
   *other* request shells out. macOS `lsof` invocations are
   non-trivial (~30–150 ms) and fork a process.
3. `collectPendingPromptsByDir(ports)` — HTTP probes to every running
   OpenCode instance.
4. `applyGitInfo` — up to 8 parallel `git status` per unique dir,
   30 s TTL. With many sessions across many repos, every 30 s = burst
   of fork/exec.
5. SQLite reads in `applySessionState` (archived/seen).

The dominant cost on the steady-state path is **lsof + per-port HTTP
probes + git** — none of which are about the database. Worth
verifying with the timing middleware before optimising.

#### S3 — `SessionDetail.tsx` size + render fan-out

3102 lines, many `useEffect` chains, no obvious virtualization on
`parts`. SSE event firehose during a busy turn could be triggering
large re-renders. `useInfiniteRows.ts` exists but I haven't traced
where it's wired.

#### S4 — Duplicate `/api/sessions/notify` pollers (favicon + bell)

`useFaviconNotify` and `useBellNotify` independently call
`api.sessionsNotify` every 10 s with identical params. Free
request-coalescing if combined into one shared store subscription.

#### S5 — Dashboard polls while hidden

`SessionDetail`'s sidebar refresh pauses on `document.hidden`, but
`Dashboard.tsx:130-134` keeps polling regardless. Background tabs
keep flogging the backend.

### Things that are already good

- `apiStore` request-status tracking is well-structured (single
  source of truth).
- `internal/git` (status lookup) has a per-dir 30 s cache, an 8-worker bound, a 2 s
  timeout, and dedups by dir within a request.
- `SessionDetail`'s sidebar refresh pauses on `document.hidden`.
- SSE path exists; the 10 s fallback only fires when SSE is broken.
- Session-detail cache in `apiStore` (3-entry LRU) makes back-nav
  instant.
- `useMemoryMonitor` + `usePerformanceCleanup` already exist —
  comments document past memory-pressure history.

## Phase 2 — Instrumentation (landed)

Three independent, low-risk additions, each on its own commit:

| Commit  | Subject                                                              |
|---------|----------------------------------------------------------------------|
| dc57a65 | `feat: Log per-request HTTP timing as structured logrus events`     |
| 7bca3ae | `feat: Track main-thread long tasks and surface in footer stats`    |
| 93db4f6 | `feat: Buffer recent API call timings under window.__ocmanPerf`     |

### What landed

**A. Backend request-timing middleware**
(`internal/server/middleware.go`)

- Wraps the mux. Logs `method`, `path`, `status`, `duration_ms`.
- INFO when `duration ≥ 250 ms`, DEBUG otherwise.
- Skips SSE (`/api/session/{id}/events`) and the recursive
  `/api/debug/log` sink.

**B. Frontend long-task observer**
(`frontend/src/lib/useLongTaskMonitor.ts`)

- `PerformanceObserver({ entryTypes: ['longtask'] })`, counts
  main-thread blocks > 50 ms.
- Footer shows `lt: N / Xms`; amber at `maxMs ≥ 100 ms`, red at
  `maxMs ≥ 250 ms`.
- Safari (no longtask support) is a clean no-op.

**C. Frontend fetch timing ring buffer**
(`frontend/src/lib/perfRing.ts` + `api.ts` wrap)

- Last 100 calls kept in memory with normalized URL templates
  (so all `/api/session/abc.../info` aggregate as
  `/api/session/:id/info`).
- Devtools handles:
  ```js
  __ocmanPerf.summary()              // pathTemplate × percentiles
  __ocmanPerf.entries()              // chronological list
  __ocmanPerf.clear()                // reset before reproducing a stall
  console.table(__ocmanPerf.summary())
  ```

### How to use it

When the UI next feels stuck:

1. **Watch the footer.** A red `lt: N / Xms` value confirms the
   problem is main-thread blocking, not API latency. Strong signal
   for S1 / S3.
2. **Open devtools and run**
   `console.table(__ocmanPerf.summary())`. The summary is sorted by
   max latency descending. Look for endpoints with high `p95Ms` or
   `maxMs`. `/api/sessions` consistently > 200 ms ⇒ S2 confirmed.
   Everything < 50 ms ⇒ the frontend is the problem.
3. **Tail server logs** (e.g. `tmp/air.log`) for
   `level=info msg="http request"` lines. The `duration_ms` field
   tells you what the *server* was doing during a perceived stall.

### What's intentionally not measured

- Inline `fetch()` calls in `api.ts` aren't instrumented
  (createSession, sendMessage, archiveSession, transcribe, etc.).
  Those are user-initiated POSTs; `perfRing` focuses on the
  polling-driven GETs that we suspect drive the stuckness.
- No persistence — closing the tab loses the ring.
- No alerting; the footer color tiers are the only UI signal.

## Phase 2.5 — Measurement results

A representative `__ocmanPerf.summary()` snapshot, taken after ≈3
minutes of "sit on dashboard, click a session, look at it":

| pathTemplate                          | count | avgMs | p50Ms | p95Ms | maxMs | err |
|---------------------------------------|-------|-------|-------|-------|-------|-----|
| `/api/session/:id/models`             | 3     | 2287  | 2948  | 2948  | 2948  | 0   |
| `/api/session/:id/info`               | 1     | 1876  | 1876  | 1876  | 1876  | 0   |
| `/api/git/diff`                       | 1     | 1875  | 1875  | 1875  | 1875  | 0   |
| `/api/session/:id/changes`            | 1     | 1872  | 1872  | 1872  | 1872  | 0   |
| `/api/sessions/notify`                | 21    | 293   | 266   | 310   | 1854  | 0   |
| `/api/session/:id/commands`           | 2     | 1341  | 1345  | 1345  | 1345  | 0   |
| `/api/session/:id`                    | 3     | 842   | 1157  | 1345  | 1345  | 1   |
| `/api/sessions`                       | 37    | 198   | 128   | 522   | 1340  | 0   |
| `/api/session/:id/agents`             | 1     | 965   | 965   | 965   | 965   | 0   |
| `/api/session/:id/permissions`        | 1     | 758   | 758   | 758   | 758   | 0   |
| `/api/session/:id/questions`          | 1     | 758   | 758   | 758   | 758   | 0   |
| `/api/tmux/sessions`                  | 4     | 30    | 45    | 47    | 47    | 0   |
| `/api/system/stats`                   | 20    | 11    | 8     | 38    | 38    | 0   |
| `/api/whisper/status`                 | 2     | 35    | 36    | 36    | 36    | 0   |
| `/api/projects`                       | 2     | 11    | 12    | 12    | 12    | 0   |

The frontend `lt:` counter did *not* turn red during this session.

### What the data overturned

- **S1 was wrong.** Long-task counter stayed quiet, so the
  `setInterval(0)` palette poller is not the cause of perceived
  stalls. Still worth fixing for cleanliness, but it's not the
  bottleneck.
- **The bottleneck is the OpenCode HTTP proxy.** Every per-session
  call that fans out to the running OpenCode instance lands in the
  750 ms – 2.95 s range. `/api/system/stats` (38 ms) and
  `/api/projects` (12 ms) confirm the Go process is fine when it
  doesn't talk upstream.
- **`/api/sessions` p95 522 ms / max 1340 ms is real** and matches S2:
  the lsof + per-port pending-prompts fan-out path. Average is fine
  because the 3 s lsof cache hides most calls; the tail is ugly
  because cache expiry forces a synchronous fan-out.
- **`/api/session/:id/changes` at 1.87 s is a mystery.** That handler
  is a pure DB walk over parts; no HTTP. Either contention with a
  sibling endpoint, or a missing index, or just a single noisy sample
  (count = 1).

### Code-side findings (independent of timing)

These came out of reading the proxy code paths after seeing the
numbers. Each one is referenced from a B-task below.

1. **Catalog endpoints (`/agent`, `/command`, `/provider`) are
   uncached.** Configuration data that changes rarely is re-fetched
   on every call. **B1.**
2. **`SessionInfo` makes 4 upstream HTTP calls per request** —
   `/mcp`, `/lsp`, `/provider` in parallel, then `/session/{id}/message`
   sequentially after the parallel block. The 4th can join the wait
   group. **B2.**
3. **Lsof discovery has TTL but no singleflight.** A cold mount fires
   5+ endpoints simultaneously; the first does lsof while the others
   block on the mutex. `golang.org/x/sync/singleflight` would coalesce
   them. **B3.**
4. **`/api/session/:id` proxies to OpenCode by default.** The DB has
   the same data and SSE pushes the live deltas. The live HTTP path
   exists for token/cost freshness during an active turn, but it
   costs 1.34 s p95 on every session refresh. **B4.**
5. **`/api/sessions` re-queries `/permission` and `/question` per
   running OpenCode every 5 s.** No cache, even though the SSE stream
   already pushes these for the connected session. **B6.**
6. **`/api/session/:id/changes` cost is unexplained.** Single sample,
   pure DB walk. Needs a server-side timing split before we know
   whether to fix anything. **B5.**

## Phase 2.6 — Diagnostic patterns (lessons learned)

These are heuristics for diagnosing future regressions. They came
out of chasing the wrong thing once and not wanting to do it again.

### `/api/system/stats` is the canary

`/api/system/stats` does pure local work — it reads
`runtime.MemStats` and a few uptime values. There is no path in
that handler that can take more than single-digit milliseconds
under any realistic load.

If `__ocmanPerf.summary()` shows `/api/system/stats` slow
**alongside** other endpoints, the **Go process itself is being
blocked**. Causes that fit this pattern:

- Upstream OpenCode is hung, and ocman has many goroutines piled up
  waiting on it (network resource pressure, GC roots).
- A goroutine is spinning at 100% CPU, starving the scheduler.
- The OS is paging hard (memory pressure), so even pure-Go work
  takes seconds.

**Do not start optimising ocman code based on this signal.** Check
upstream and the system first:

```sh
# Is OpenCode itself slow? (Replace 4096 with your actual port.)
time curl -s http://127.0.0.1:4096/permission > /dev/null

# How many running OpenCode instances?
lsof -iTCP -sTCP:LISTEN -P -n | grep opencode

# Is one ocman goroutine pegging a CPU?
ps -o pid,pcpu,pmem,etime,command -p "$(pgrep -f 'tmp/ocman' | head -1)"
```

If `/api/system/stats` is fast (single-digit ms) but other
endpoints are slow, *that's* when ocman code is at fault and
optimisation work makes sense.

### Idle vs. active comparison

The most reliable comparison is **two `__ocmanPerf` snapshots from
the same dashboard layout**: one with the tab idle for ≥ 30 seconds,
one after a few minutes of normal use. The idle snapshot isolates
ocman's polling cost; the active snapshot adds the per-session
catalog and proxy paths. Comparing them tells you which side
regressions live on.

## Phase 3 — Backend optimization plan

Tasks are keyed `B1`…`B7`. Each is independent; any subset can be
shipped. The order below is impact-first within reasonable risk.
**Re-run `__ocmanPerf.summary()` after each landed task** to confirm
the next one is still worth doing.

### Status

| Task | Status        | Commit                                                              |
|------|---------------|----------------------------------------------------------------------|
| B5   | ✅ landed      | `feat: Log per-operation timing for SessionChanges DB calls`        |
| B1   | ✅ landed      | `feat: Cache OpenCode catalog endpoints with 30s TTL and singleflight` |
| B3   | ✅ landed      | `feat: Singleflight OpenCode port discovery to coalesce concurrent lsof` |
| B2   | ✅ landed      | `perf: Run liveTokensAndCost in the SessionInfo parallel fan-out`   |
| B6   | ✅ landed (+timeout) | `fix: Cap pending-prompt fetch at 500ms…` + `perf: Cache pending-prompt fetches per port with 3s TTL` |
| B4   | ⏸ deferred    | Needs DB-vs-live freshness verification (see open questions)        |
| B7   | ⏸ deferred    | Only if measurements still show unexplained Go-side latency         |

The B6 work landed in two commits because we discovered (via the
canary pattern above) that a hung OpenCode instance was dragging
every dashboard request to 10s+ via the shared 10s HTTP timeout.
The fix is a tight 500 ms per-call timeout *plus* the planned 3s
TTL cache; together they ensure (a) one slow instance never blocks
the dashboard fan-out, and (b) the steady-state poll cost is
amortised over the cache window.

### B5 — Instrument `/api/session/:id/changes` (do first, lowest risk)

The 1.87 s sample is unexplained. Before touching anything, split
that endpoint's timing into "DB" vs "everything else" so we know
whether it's a real bottleneck or a single noisy sample.

- File: `internal/server/handlers.go` (handler) and a thin
  `time.Since` wrapper around the DB call inside the OpenCode
  adapter's `SessionChanges` method.
- Acceptance: log line includes `db_ms`, `total_ms` for that
  endpoint at INFO level when total exceeds the slow threshold.
- Risk: zero (instrumentation only).
- Effort: 30 min.

### B1 — Cache `/agent`, `/command`, `/provider` per (port, endpoint)

These are configuration data; OpenCode itself rebuilds them rarely.
A 30-second TTL cache eliminates the bulk of the per-session-page
load cost.

- File: `internal/platforms/opencode/client.go` — add a
  `getJSONCached(port, path, ttl)` helper.
- Wire into `AgentCatalog`, `SlashCommands`, and `fetchOpenCodeProviders`.
  The latter is shared by `SessionModels` and `SessionInfo`, so
  both endpoints benefit.
- TTL: start at 30 s; expose as a constant for easy tuning.
- Acceptance: re-running the same `__ocmanPerf` flow shows
  `/api/session/:id/{models,agents,commands}` p95 < 100 ms after
  the first cache miss.
- Risk: low. Stale config for ≤ TTL. Worst case: user changes
  agents.json and waits 30 s to see it.
- Effort: 2–3 hours including TTL/race tests.

### B3 — Singleflight the lsof port discovery

When five endpoints fire on a SessionDetail mount, only the first
should pay for lsof. Today they all serialize on a `sync.Mutex` and
take turns observing the same answer.

- File: `internal/platforms/opencode/client.go`
- Add `golang.org/x/sync/singleflight.Group` around the uncached
  path. Keep the TTL cache as the fast path.
- Acceptance: a synthetic test that fires N concurrent
  `discoverOpenCodePorts` from a cold cache observes exactly one
  uncached call.
- Risk: low. Standard library pattern.
- Effort: 1 h including the test.

### B2 — Fold `liveTokensAndCost` into the SessionInfo parallel fan-out

`SessionInfo` currently runs three calls in parallel, then a fourth
sequentially. Move all four into the same wait group.

- File: `internal/platforms/opencode/info.go`
- Acceptance: `/api/session/:id/info` p95 drops to roughly
  `max(individual upstream call)` instead of
  `sum(parallel block) + liveTokensAndCost`.
- Risk: trivial. The four calls are independent; nothing in
  `liveTokensAndCost` depends on `mcp` / `lsp` / `provider` results.
- Effort: 30 min.

### B4 — Stop preferring live over DB for `/api/session/:id` (proposal, needs verification)

`fetchSessionFromOpenCode` proxies two upstream calls (`/session/{id}`
and `/session/{id}/message`) on every refresh, even though the DB
has the same data and SSE delivers the live deltas. Cost: 1.34 s
p95 per refresh.

**Open question before implementing:** how stale is the DB during
an active turn? OpenCode's persistence cadence is one INSERT per
streamed delta, so it should be ≤ 1 s behind. Need to confirm by
diffing the DB rollup against `liveTokensAndCost` while a session
is generating.

- Files: `internal/platforms/opencode/operations.go`, possibly a
  query-flag plumbed through `platforms.Platform.Session`.
- Acceptance: `/api/session/:id` p95 < 100 ms in normal use; explicit
  refresh button still hits the live path; tokens/cost in the
  Composer don't visibly lag during a turn (manual verification).
- Risk: medium. The reason live-by-default exists is composer
  freshness; we'd need to confirm SSE-driven updates fully replace
  it.
- Effort: 2–3 h, mostly verification.

### B6 — Cache pending prompts per port

`/api/sessions` and `/api/sessions/notify` both fan out to every
running OpenCode for `/permission` and `/question` on every call.
A 3-second TTL (matching the lsof cache) eliminates ~60–80% of
those calls.

- File: `internal/platforms/opencode/client.go`
  (`fetchPendingPrompts`).
- Acceptance: `/api/sessions` p95 drops below 200 ms; max latency
  no longer spikes at TTL boundaries the way it does today.
- Risk: low. Stale prompt state for ≤ 3 s; the SSE stream still
  pushes real-time updates to any connected SessionDetail.
- Effort: 1 h.

### B7 — Add backend pprof endpoint (only if needed after B1–B6)

If post-B1 measurements still show unexplained Go-side latency, add
`net/http/pprof` behind a build flag or a dev-only env var so we can
profile lock contention, GC, or syscalls.

- File: `internal/server/server.go`.
- Risk: zero (gated, off by default in production).
- Effort: 15 min.

## Frontend cleanup (lower priority)

The original Phase 1 frontend suspects (S1, S4, S5) are not the
cause of perceived stalls per the measurement, but they're still
worth fixing for cleanliness and to reduce backend load. Suggested
order if/when we come back to them:

- **F1 (= old S5)** — Pause Dashboard polling on `document.hidden`.
  30 min, very low risk. Reduces idle traffic on background tabs.
- **F2 (= old S4)** — Coalesce favicon + bell pollers into one
  shared `/api/sessions/notify` poll. 2 h, low risk. Halves the
  dashboard-adjacent backend pressure.
- **F3 (= old S1)** — Replace the `setInterval(0)` palette
  dispatcher with `useUiStore.subscribe`. 1–2 h, low risk.
  Cleanliness fix; not on the perf hot path.

These are deliberately not in the B-numbered list because the
backend wins are larger and unblock more user pain.

## Open questions / things to revisit

- **Does B4 actually break composer freshness?** Needs a controlled
  test: open a session, send a prompt, watch the cost number.
  Verify that the DB-only path stays within ≤1 s of the live path.
- **What's the actual story on `/api/session/:id/changes`?** B5's
  instrumentation now logs `parts_ms` and `session_ms` separately
  whenever the operation exceeds 200 ms. Next time the endpoint
  shows up slow in `__ocmanPerf`, grep the server log for
  `op=session_changes` and look at the field breakdown — that
  tells us whether it's the parts query, the session metadata
  fetch, or the in-memory aggregation.
- **Connection reuse to OpenCode:** the upstream `http.Client` has
  no custom transport. Default `http.DefaultTransport` should reuse
  loopback connections, but the latencies were too high for warm
  reuse during the original measurements. After B1/B2/B3/B6 most
  per-call latency is now upstream-bounded, so this is less
  pressing — revisit only if measurements show OpenCode itself
  responding instantly while ocman still reports proxy latency.

## Reference: relevant files

### Backend
- `internal/server/server.go` — mux assembly, middleware wiring.
- `internal/server/middleware.go` — request-timing logger.
- `internal/server/handlers.go` — `/api/sessions`, `/api/sessions/notify`,
  `applyGitInfo`.
- `internal/platforms/opencode/adapter.go` — Sessions(), port discovery.
- `internal/platforms/opencode/client.go` — upstream HTTP helpers
  (`getJSON`, lsof discovery, prompt fetch). Where B1/B3/B6 land.
- `internal/platforms/opencode/operations.go` — per-session catalog
  methods (`AgentCatalog`, `SlashCommands`, `SessionModels`,
  `Session`). Where B4 lands.
- `internal/platforms/opencode/info.go` — `SessionInfo` parallel
  fan-out. Where B2 lands.
- `internal/git/info.go` — git status cache (30 s TTL).

### Frontend
- `frontend/src/App.tsx` — top-level pollers and dev handles.
- `frontend/src/pages/Dashboard.tsx` — 5 s `/api/sessions` poller.
- `frontend/src/pages/SessionDetail.tsx` — the polling jungle.
- `frontend/src/lib/api.ts` — `fetchJSON` / `postJSON` (instrumented).
- `frontend/src/lib/perfRing.ts` — perf ring buffer.
- `frontend/src/lib/useLongTaskMonitor.ts` — long-task observer.
- `frontend/src/components/BackendStats.tsx` — footer display.
