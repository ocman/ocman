# Profiling

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
- `gitinfo` has a per-dir 30 s cache, an 8-worker bound, a 2 s
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

## Phase 3 — Suggested fixes (in priority order)

These are *suggestions* keyed off Phase 1 suspects. **Don't start any
of these until Phase 2 data confirms the relevant suspect.** Each
item links back to its suspect ID so the rationale is traceable.

### P1: Replace the `setInterval(0)` palette dispatcher (S1)

- File: `frontend/src/pages/SessionDetail.tsx` line ~579.
- Approach: subscribe to `useUiStore` directly via
  `useUiStore.subscribe(state => state.paletteCommand, ...)`, or fold
  the logic into a normal effect keyed on
  `useUiStore(s => s.paletteCommand)`.
- Verification: `__ocmanPerf` not relevant; check that the long-task
  counter in the footer drops sharply when sitting on a session.
- Risk: low. The interval body is a finite branch ladder; the only
  state read is `paletteCommand`.
- Estimated effort: 1–2 hours including a small unit test that
  verifies each scoped command still fires.

### P2: Pause Dashboard polling while hidden (S5)

- File: `frontend/src/pages/Dashboard.tsx` line ~130-134.
- Mirror the pattern already used in `SessionDetail` line ~1087:
  add a `visibilitychange` listener; pause the interval when
  `document.hidden`, fire once and resume on visible.
- Risk: very low.
- Estimated effort: 30 minutes.

### P3: Coalesce favicon + bell pollers (S4)

- Files: `frontend/src/lib/useFaviconNotify.ts`,
  `frontend/src/lib/useBellNotify.ts`.
- Approach: introduce a tiny `useNotifyState()` Zustand slice that
  owns the `/api/sessions/notify` poll, and have both hooks read
  from it. Halves backend pressure from these notifiers.
- Risk: low. The two hooks already process the same payload.
- Estimated effort: 2 hours.

### P4: Reduce `/api/sessions` fan-out cost (S2)

Only pursue if Phase 2 timing shows `/api/sessions` p95 > 150 ms.
Likely sub-tasks:

- Cache the `lsof` result for longer than 3 s, or push port
  discovery into a background goroutine that the handler reads
  from a snapshot — same pattern as `projects_index.go`.
- Consider a single in-process `sessionsCache` that the dashboard
  reads from, refreshed by a background loop, so handler latency
  becomes O(serialize) instead of O(lsof + N×git).
- Reuse the existing `gitinfo` cache more aggressively; per-request
  fan-out is only useful when the dashboard load is bursty (it
  isn't — it's a 5 s tick).
- Risk: medium. State-snapshot indirection has correctness traps
  around live-status flags (`PendingPermission`, `LiveConnection`).

### P5: Audit `SessionDetail` re-renders (S3)

Only pursue if the `lt:` counter stays high after P1 lands.

- Run React DevTools profiler against a busy session (long-running
  Task), capture which components render on each SSE event.
- Likely fixes: `React.memo` on heavy children, virtualize the parts
  list (`useInfiniteRows.ts` may already be primed for this), batch
  state updates from SSE handlers.
- Risk: medium-high. Touching SSE handler logic is easy to break.
- Estimated effort: a day or more, depending on what the profiler
  shows.

## Open questions / things to revisit

- **Is the lsof scan actually the cost we think it is?** Phase 2's
  backend log will say. If `/api/sessions` p95 is comfortably under
  100 ms, P4 is a no-op.
- **Does S1 actually correlate with the user's "stuck" feeling?**
  The longtask counter will say. If it sits at zero during a stall,
  we're chasing the wrong thing and need to widen Phase 2.
- **Do we need a `spec/performance/` document?** Probably not for P1
  / P2 / P3 (each is a small, local fix). P4 and P5 might deserve
  one if they grow into real refactors.

## Reference: relevant files

### Backend
- `internal/server/server.go` — mux assembly, middleware wiring.
- `internal/server/middleware.go` — request-timing logger.
- `internal/server/handlers.go` — `/api/sessions`, `/api/sessions/notify`,
  `applyGitInfo`.
- `internal/platforms/opencode/adapter.go` — Sessions(), port discovery.
- `internal/gitinfo/gitinfo.go` — git status cache (30 s TTL).

### Frontend
- `frontend/src/App.tsx` — top-level pollers and dev handles.
- `frontend/src/pages/Dashboard.tsx` — 5 s `/api/sessions` poller.
- `frontend/src/pages/SessionDetail.tsx` — the polling jungle.
- `frontend/src/lib/api.ts` — `fetchJSON` / `postJSON` (instrumented).
- `frontend/src/lib/perfRing.ts` — perf ring buffer.
- `frontend/src/lib/useLongTaskMonitor.ts` — long-task observer.
- `frontend/src/components/BackendStats.tsx` — footer display.
