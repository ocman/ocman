---
title: Profiling
weight: 8
---

## Cross-platform ingestion wakeups, November 7, 2026

On Linux, the live backend idled at 1–5% of one core, with spikes up to 36%.
It had written 16 GB in 12 hours. Its log showed up to 315
`conversation history repair failed` warnings per minute while
`~/.claude/projects` was briefly missing. The binary was stripped and ptrace
was blocked, so no stacks were captured. The cause was found by reading the
code and confirmed with regression tests.

- The OpenCode watcher broadcast `ocman.session.activity` for every message
  or part event without a `platform`. Ingestion treats a missing platform as
  "wake every source", so each streamed OpenCode part also woke the Claude Code
  journal worker.
- The journal worker did not know that OpenCode session ID. That made it run a
  full pass, loading every stored session and listing every Claude transcript.
  Each wake also reset the failure backoff, so a missing source retried on every
  token.

Fixes: the activity event now carries `platform: opencode`. The journal worker
drops unforced hints for sessions it has never stored, because hook records
announce new sessions. Only forced repairs reset its backoff. Tests: 50 foreign
hints caused 50 inventories before the fix and none after. 200 hints against a
failing source caused 199 failed repairs before and at most 6 after.

Each misrouted hint cost at least one Claude transcript inventory. The
benchmark below excludes `store.Sessions` and the comparison loop, so it is a
lower bound. On an i7-12700K, with three two-second runs per size:

| Transcripts | Per inventory | Allocations |
|---:|---:|---:|
| 100 | 2.3–3.1 ms | 3,775 |
| 1,000 | 23–26 ms | 32,798 |
| 10,000 | 132–177 ms | 321,025 |

The live log recorded up to 315 failed passes per minute, about 5 per second,
during OpenCode streaming. At that rate, the removed work is roughly 1% of one
core at 100 transcripts and 12% at 1,000. At 10,000 it is 65–90%, until the
coalesced wake channel caps it at about one core. This machine had a single
transcript, so its live saving is negligible. These are projections, not
before/after process measurements.

```sh
go test ./internal/conversationingest -run 'TestJournalMode' -count=1
go test ./internal/autoapprove -run TestSessionDataBroadcastsActivity -count=1
go test ./internal/platforms/claude/provider -run '^$' -bench '^BenchmarkHistoryInventory$' -benchtime=2s -count=3
```

## Follow-up CPU and spike captures, November 7, 2026

Read-only sampling of the local backend confirmed that high CPU remains after
the sidebar query fixes below. The running binary reported clean revision
`0c0bcf71576310fc994d74776a54b25722d9f135`, which includes those fixes.
No backend was restarted, no development server was started against live data,
and no live database was changed by this investigation.

CPU was calculated from differences in the backend PID's cumulative CPU time,
not `ps %cpu`, which is a decaying average. Agent and plugin subprocesses are
excluded. On macOS, 100% is one core.

| Existing dashboard activity | Duration | Average CPU | 95th percentile | Highest interval |
|---|---:|---:|---:|---:|
| Initial observation | 91.16 s | 59.44% | 145.07% | 161.80% |
| Spike-triggered observation | about 122 s | 51.75% | not calculated | 190.64% |

Intervals were approximately one second. The second average is the arithmetic
mean of interval percentages. These are observed, uncontrolled workloads, not
no-client idle measurements or a before/after comparison. The initial window
overlapped OS stack sampling, which has overhead. The second window also
overlapped local tests and benchmarks, so machine contention can affect it.
Halving the initial average would mean 29.72%, but that target has **not** been
demonstrated.

### What was captured during spikes

A 15-second OS stack sample and a denser 30-second sample were followed by
three 10-second samples triggered when an interval exceeded 100% CPU. Their
trigger intervals measured 106.39%, 186.84% and 103.44% CPU. Captures start after
the trigger interval, so they may miss the exact highest instant. These samples
repeatedly included:

- Sidebar HTTP reads through `sidebar.Reader.SessionsFor`.
- `state.DB.SidebarFlags`, including reads from the PR-watch overlay.
- Conversation ingestion through `importSession`, `statev2.DB.Commit` and
  `applyContent`, plus inventory and binding reads.
- Background snapshots through `sessioncatalog.Store.BackgroundLifecycles`.

Several kinds of work overlap in the spike windows. OS sampling includes
sleeping and blocked threads and does not reliably unwind every Go stack, so
stack counts are not percentages of CPU. These captures identify work to
reproduce, not an exact allocation of each peak's CPU or proof that one query
caused the entire peak. Open PR #1367 already addresses shared live session
reads; this follow-up does not duplicate that work.

### Reproduced background lifecycle scan

The production query plan started at the owner/source catalogue index and
looked up lifecycle overrides for every retained session. This happened even
when the override table was empty. Runtime background snapshots call this query.

The fix pins the sparse override table as the driving table using `CROSS JOIN`,
then looks up each catalogue row by identity. Owner, platform, source and purge
predicates are unchanged. There is no cache, delayed invalidation or migration.

Apple M4 Max, production Go SQLite driver, disposable migrated databases, two
one-second benchmark runs per case:

| Retained sessions | Overrides | Before | After |
|---|---:|---:|---:|
| 1,000 | 0 | 219–221 µs | 15–16 µs |
| 1,000 | 37 | 259–260 µs | 53–54 µs |
| 20,000 | 0 | 4.68–4.75 ms | 14.6–14.7 µs |
| 20,000 | 37 | 5.15–5.36 ms | 55.8–57.2 µs |

The 20,000-session query is about 99% faster, and no longer scales with retained
history when overrides stay sparse. This is **not** a 99% backend CPU reduction.
The query-plan regression test failed before the fix and passed afterward.
Behavior tests cover multiple tasks, replacement, source/owner/platform
isolation, purged sessions, missing sources and closed-store errors.

```sh
go test ./internal/sessioncatalog -run '^TestBackgroundLifecycle' -count=1
go test ./internal/sessioncatalog -run '^$' -bench '^BenchmarkBackgroundLifecycles$' -benchtime=1s -count=3
# Optional isolated Go CPU profile; setup is included, so inspect call stacks too.
go test ./internal/sessioncatalog -run '^$' -bench '^BenchmarkBackgroundLifecycles/history20000/overrides37$' -benchtime=5s -cpuprofile=cpu.pprof -o catalog.test
go tool pprof -top catalog.test cpu.pprof
```

### Remaining work for the 50% target

Deploy the fix, then repeat whole-process CPU captures under matching client
counts, retained-history size and ingestion event rates. Measure no-client idle,
quiet dashboards and active streaming separately. Keep benchmark/test runs out
of the measurement window. Do not compare uncontrolled activity before and
after and attribute all differences to this change.

The remaining measured candidates are repeated sidebar/display-flag reads and
ingestion bursts. Reproduce their costs independently before changing them.
If OS stacks cannot separate them, add opt-in, loopback-only Go profiling for
a future instrumented run. No profiling HTTP endpoint is introduced here.

## Earlier backend CPU investigation, November 2026

The running backend reproduced 45–47% CPU in two-second windows and averaged
31% over a separate ten-second window. These are ocman's own process costs,
not its agent subprocesses. On macOS, 100% means one CPU core. The captures did
not reproduce the reported 150% peak or establish a no-client idle baseline.

Read-only process samples repeatedly reached `sidebarSessionRows` and
`sidebar.Reader.SessionsFor`, through sidebar and notification HTTP requests.
Aggregate-only, read-only database inspection found 16,816 catalogue rows,
20,577 sidebar rows, 308 halt settings, 4,771 seen flags and 37 live observations.
No development server or migration ran against those live databases.

Two query plans did unnecessary history-sized work:

- Bounded pages joined the catalogue inventory and sorted all eligible rows
  before applying `LIMIT`. A global recency index and fixed driving join order
  let pages stop after enough eligible rows. Filters still precede the limit.
- Halt propagation visited every catalogue row to discover sparse halt settings.
  It now starts at an indexed setting-prefix range, then looks up canonical or
  legacy native identities before walking their source-scoped ancestors.

Unbounded inventory reads retain planner-selected join order. The new recency
index is an appended history migration; it does not rewrite conversation data.

### Repeatable query measurements

Apple M4 Max, production Go SQLite driver, disposable migrated databases,
10,000 sessions. The polling fixture has 37 live observations, 308 halt settings
and 4,771 seen flags. Paired reads alternate order on the same fixture.

| Workload | Before | After |
|---|---:|---:|
| 50-row page, no observations or halts, three runs | 34.7–35.6 ms | 1.70–1.71 ms |
| 50-row polling page with sparse live state, three paired runs | 61.3–61.5 ms | 4.23–4.31 ms |
| 500-row polling page with sparse live state, three paired runs | 68.8–69.4 ms | 6.93–7.07 ms |

The first workload includes `SessionsFor` decoding. Paired polling workloads
use identical generic row draining on both queries. These are query timings,
not a claimed 90–95% reduction in total backend CPU. The baseline page CPU
profile attributed about 90% of samples cumulatively to SQLite's VM.

```sh
go test ./internal/sidebar -run 'TestSidebar(PageStreamsRecency|HaltSeedUsesSparseLookups)|TestSparseHaltsMatchCatalogueWalk'
go test ./internal/sidebar -run '^$' -bench '^BenchmarkSidebar(PageRead|Polling)$' -benchtime=2s -count=3
```

The query-plan regression tests were observed failing before their fixes.
Behavior comparisons cover publication/purge fences, archived/deleted/child
visibility, sparse pins and explicit inclusions, filtering, pagination, legacy
halt keys and recursive cycles. An upgrade test checks index creation without
losing stored sidebar history.

### Expected operating profile and remaining validation

Treat these as provisional acceptance budgets, not measurements already met:

- Settled backend with no clients, ingestion or foreground work should be near
  idle, targeting less than 1% of one core over a minute.
- One quiet dashboard over roughly 20,000 retained sessions should target less
  than 5% of one core over a minute. Historical session count should not multiply
  every small page read's work.
- Active streaming must be measured separately at a recorded event rate and
  concurrency. Record CPU per event and allocations, not just short peaks, and
  exclude agent/model inference processes from ocman's budget.

The live process was not restarted or replaced during this investigation.
Re-measure its CPU after deploying the change, with the same dashboard and
session activity, then separately measure no-client idle and active streaming.
Further captured work included historical display flags and background lifecycle
snapshots; their residual contribution has not been isolated or fixed here.
Do not declare the reported peak resolved from query benchmarks alone.

## Earlier OpenCode-only investigation

The remainder is a historical performance investigation, including measurements
and a backlog from an earlier OpenCode-only implementation. Polling cadences, source
locations and lsof-based discovery below describe that snapshot, not the current
runtime. Current ownership and data flows are documented in
[Architecture](architecture.md), including compact stored sidebar queries,
retained history and managed-inventory runtime lookup.

The starting symptom: the frontend occasionally feels stuck, and it wasn't
clear whether slow API calls or the UI itself caused it.

## Phase 1: reconnaissance findings (OpenCode-only)

The investigated deployment ran only the OpenCode adapter
(`-platforms opencode`). Findings below assume that configuration. Current
defaults enable OpenCode and Claude Code; Codex is opt-in.

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
| 11| `SessionDetail` SSE                    | streaming          | `GET /api/session/{id}/stream`                     |

On the dashboard:

| # | Source                  | Cadence | Notes                                                       |
|---|-------------------------|---------|-------------------------------------------------------------|
| 12| `Dashboard.tsx` line 132| 5 s     | `GET /api/sessions` (full fan-out + git status, all the time) |
|   | + 1-5 above             |         |                                                             |

### Top suspects

The list is ordered by likelihood × impact for an OpenCode-only
deployment. The numeric IDs (S1, S2, …) are referenced from the
Phase 3 task list further down.

#### S1: `setInterval(..., 0)` in `SessionDetail.tsx:579`

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

This is the strongest candidate for the "stuck" feeling. It can
saturate the event loop on a busy machine and fight against React
scheduling. Suggested fix: convert to a `useUiStore.subscribe`
selector or a normal `useEffect` keyed on `paletteCommand`.

#### S2: `/api/sessions` cost on a 5 s loop

Per request, `handleSessions` does:

1. `db.GetSessions`: one SQLite query, indexed reads, fast.
2. `discoverOpenCodePorts`: runs `lsof` on macOS/Linux. **3 s TTL
   cache**, so the dashboard's 5 s poll cadence means roughly every
   *other* request shells out. macOS `lsof` invocations are
   non-trivial (~30-150 ms) and fork a process.
3. `collectPendingPromptsByDir(ports)`: HTTP probes to every running
   OpenCode instance.
4. `applyGitInfo`: up to 8 parallel `git status` per unique dir,
   30 s TTL. With many sessions across many repos, every 30 s = burst
   of fork/exec.
5. SQLite reads in `applySessionState` (archived/seen).

The dominant cost on the steady-state path is **lsof + per-port HTTP
probes + git**, none of which are about the database. Worth
verifying with the timing middleware before optimising.

#### S3: `SessionDetail.tsx` size and render fan-out

3102 lines, many `useEffect` chains, no obvious virtualization on
`parts`. SSE event firehose during a busy turn could be triggering
large re-renders. `useInfiniteRows.ts` exists but I haven't traced
where it's wired.

#### S4: duplicate `/api/sessions/notify` pollers (favicon + bell)

`useFaviconNotify` and `useBellNotify` independently call
`api.sessionsNotify` every 10 s with identical params. Free
request-coalescing if combined into one shared store subscription.

#### S5: dashboard polls while hidden

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
- `useMemoryMonitor` + `usePerformanceCleanup` already exist, and
  their comments document past memory-pressure history.

## Phase 2: instrumentation (landed)

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
- Skips SSE (`/api/session/{id}/stream`) and the recursive
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
  (createSession, sendMessage, archiveSession, etc.).
  Those are user-initiated POSTs; `perfRing` focuses on the
  polling-driven GETs that we suspect drive the stuckness.
- No persistence. Closing the tab loses the ring.
- No alerting; the footer color tiers are the only UI signal.

## Phase 2.5: measurement results

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
| `/api/projects`                       | 2     | 11    | 12    | 12    | 12    | 0   |

The frontend `lt:` counter did *not* turn red during this session.

### What the data overturned

- **S1 was wrong.** Long-task counter stayed quiet, so the
  `setInterval(0)` palette poller is not the cause of perceived
  stalls. Still worth fixing for cleanliness, but it's not the
  bottleneck.
- **The bottleneck is the OpenCode HTTP proxy.** Every per-session
  call that fans out to the running OpenCode instance lands in the
  750 ms to 2.95 s range. `/api/system/stats` (38 ms) and
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
2. **`SessionInfo` makes 4 upstream HTTP calls per request:**
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

## Phase 2.6: diagnostic patterns (lessons learned)

These are heuristics for diagnosing future regressions. They came
out of chasing the wrong thing once and not wanting to do it again.

### `/api/system/stats` is the canary

`/api/system/stats` does pure local work. It reads
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

## Phase 3: backend optimization plan

Tasks are keyed `B1`…`B7`. Each is independent; any subset can be
shipped. The order below is impact-first within reasonable risk.
**Re-run `__ocmanPerf.summary()` after each landed task** to confirm
the next one is still worth doing.

### Status

| Task | Status        | Commit                                                              |
|------|---------------|----------------------------------------------------------------------|
| B5   | landed | `feat: Log per-operation timing for SessionChanges DB calls`        |
| B1   | landed | `feat: Cache OpenCode catalog endpoints with 30s TTL and singleflight` |
| B3   | landed | `feat: Singleflight OpenCode port discovery to coalesce concurrent lsof` |
| B2   | landed | `perf: Run liveTokensAndCost in the SessionInfo parallel fan-out`   |
| B6   | landed (+timeout) | `fix: Cap pending-prompt fetch at 500ms…` + `perf: Cache pending-prompt fetches per port with 3s TTL` |
| B4   | deferred | Needs DB-vs-live freshness verification (see open questions)        |
| B7   | deferred | Only if measurements still show unexplained Go-side latency         |

The B6 work landed in two commits because the canary pattern above
turned up something else: a hung OpenCode instance was dragging every
dashboard request past 10 s through the shared 10 s HTTP timeout. The
fix is a tight 500 ms per-call timeout *plus* the planned 3 s TTL
cache. Together, one slow instance no longer blocks the dashboard
fan-out, and the cache window amortises the steady-state poll cost.

### B5: instrument `/api/session/:id/changes` (do first, lowest risk)

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

### B1: cache `/agent`, `/command`, `/provider` per (port, endpoint)

These are configuration data; OpenCode itself rebuilds them rarely.
A 30-second TTL cache eliminates the bulk of the per-session-page
load cost.

- File: `internal/platforms/opencode/provider/client.go`, where a
  `getJSONCached(port, path, ttl)` helper goes.
- Wire into `AgentCatalog`, `SlashCommands`, and `fetchOpenCodeProviders`.
  The latter is shared by `SessionModels` and `SessionInfo`, so
  both endpoints benefit.
- TTL: start at 30 s; expose as a constant for easy tuning.
- Acceptance: re-running the same `__ocmanPerf` flow shows
  `/api/session/:id/{models,agents,commands}` p95 < 100 ms after
  the first cache miss.
- Risk: low. Stale config for ≤ TTL. Worst case: user changes
  agents.json and waits 30 s to see it.
- Effort: 2-3 hours including TTL/race tests.

### B3: singleflight the lsof port discovery

When five endpoints fire on a SessionDetail mount, only the first
should pay for lsof. Today they all serialize on a `sync.Mutex` and
take turns observing the same answer.

- File: `internal/platforms/opencode/provider/client.go`
- Add `golang.org/x/sync/singleflight.Group` around the uncached
  path. Keep the TTL cache as the fast path.
- Acceptance: a synthetic test that fires N concurrent
  `discoverOpenCodePorts` from a cold cache observes exactly one
  uncached call.
- Risk: low. Standard library pattern.
- Effort: 1 h including the test.

### B2: fold `liveTokensAndCost` into the SessionInfo parallel fan-out

`SessionInfo` currently runs three calls in parallel, then a fourth
sequentially. Move all four into the same wait group.

- File: `internal/platforms/opencode/provider/info.go`
- Acceptance: `/api/session/:id/info` p95 drops to roughly
  `max(individual upstream call)` instead of
  `sum(parallel block) + liveTokensAndCost`.
- Risk: trivial. The four calls are independent; nothing in
  `liveTokensAndCost` depends on `mcp` / `lsp` / `provider` results.
- Effort: 30 min.

### B4: stop preferring live over DB for `/api/session/:id` (proposal, needs verification)

`fetchSessionFromOpenCode` proxies two upstream calls (`/session/{id}`
and `/session/{id}/message`) on every refresh, even though the DB
has the same data and SSE delivers the live deltas. Cost: 1.34 s
p95 per refresh.

**Open question before implementing:** how stale is the DB during
an active turn? OpenCode's persistence cadence is one INSERT per
streamed delta, so it should be ≤ 1 s behind. Need to confirm by
diffing the DB rollup against `liveTokensAndCost` while a session
is generating.

- Files: `internal/platforms/opencode/provider/operations.go`, possibly a
  query-flag plumbed through `platforms.Platform.Session`.
- Acceptance: `/api/session/:id` p95 < 100 ms in normal use; explicit
  refresh button still hits the live path; tokens/cost in the
  Composer don't visibly lag during a turn (manual verification).
- Risk: medium. The reason live-by-default exists is composer
  freshness; we'd need to confirm SSE-driven updates fully replace
  it.
- Effort: 2-3 h, mostly verification.

### B6: cache pending prompts per port

`/api/sessions` and `/api/sessions/notify` both fan out to every
running OpenCode for `/permission` and `/question` on every call.
A 3-second TTL (matching the lsof cache) eliminates ~60-80% of
those calls.

- File: `internal/platforms/opencode/provider/client.go`
  (`fetchPendingPrompts`).
- Acceptance: `/api/sessions` p95 drops below 200 ms; max latency
  no longer spikes at TTL boundaries the way it does today.
- Risk: low. Stale prompt state for ≤ 3 s; the SSE stream still
  pushes real-time updates to any connected SessionDetail.
- Effort: 1 h.

### B7: add backend pprof endpoint (only if needed after B1-B6)

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

- **F1 (= old S5).** Pause Dashboard polling on `document.hidden`.
  30 min, very low risk. Reduces idle traffic on background tabs.
- **F2 (= old S4).** Coalesce favicon + bell pollers into one
  shared `/api/sessions/notify` poll. 2 h, low risk. Halves the
  dashboard-adjacent backend pressure.
- **F3 (= old S1).** Replace the `setInterval(0)` palette
  dispatcher with `useUiStore.subscribe`. 1-2 h, low risk.
  Cleanliness fix; not on the perf hot path.

These are deliberately not in the B-numbered list because the
backend wins are larger and unblock more user pain.

## Interaction measurement (frontend)

Opt-in client metrics for interaction latency, which matters more than
initial load. Off by default; when off, each hook point does one cached
boolean check.

**Enable:** open any page with `?debug`, or run
`localStorage.setItem('ocman:perf', '1')` and reload. Use the
localStorage flag when measuring a session page: `?debug` also turns on
the SSE debug overlay, which adds its own cost.

**Read** from the devtools console:

```js
__ocmanPerf.metrics()       // p50/p95/max/count per metric and label
__ocmanPerf.resetMetrics()  // start fresh before reproducing something
console.table(__ocmanPerf.entries())  // API calls, now with serverTiming
```

`metrics()` reports:

- `interaction`: Event Timing durations (16 ms threshold), keyed by
  `pointer|keyboard:<label>`. The label is the nearest `data-perf`
  attribute: `session-row`, `tool-call`, `composer`, `composer-send`,
  `palette`, `panel-tab`, or `palette-open` for the palette hotkeys.
- `loaf`: long-animation-frame duration and blocking time. `topLoafScripts`
  lists the scripts that took the most time.
- `sse.batch`: reducer time, events, messages and parts per SSE batch.
  `sse.commit` is the time from flush to React commit.
- `render`: React Profiler `actualDuration` for `SessionPage`, `Thread`,
  `SessionSidebar` and `RightPanel`. Production bundles report it only
  when built with `OCMAN_PROFILE=1`.
- `switch`: time from a session switch to the first frame after the new
  thread renders, labelled `hit` or `miss` depending on the session cache.
- `counters`: commit counts and `countRender` counts per component.

When the tab is hidden, the summary is sent to `POST /api/debug/log` and
written to the backend log as `[ocman:perf] summary`. Entries come from
PerformanceObservers, so `usePerformanceCleanup` clearing the performance
buffer does not lose them.

**Bench** (`frontend/perf/`, separate from the e2e suite):

```sh
cd frontend
pnpm perf                                  # profiling build + all scenarios
PERF_SCENARIOS=stream,switch PERF_LABEL=x pnpm exec playwright test -c playwright.perf.config.ts
PERF_DIST=perf/dist-base ...               # serve another build, e.g. a baseline
PERF_CPUPROFILE=1 ...                      # also save a .cpuprofile per scenario
node perf/compare.mjs ../tmp/perf/a1.json,../tmp/perf/a2.json ../tmp/perf/b1.json,../tmp/perf/b2.json
```

It serves the bundle on port 8338 with every `/api` route mocked and no
backend. It replaces `EventSource` with an in-page replay at a fixed rate
and applies a 4x CPU throttle. The fixtures are large: 150 messages, 300
tool calls including 75 edit diffs, 200 sidebar sessions, a 9 kB streamed
markdown answer and a running subagent. The scenarios are stream, type
while streaming, expand tool calls, scroll, switch sessions (cache hit and
miss), idle, and stream with a subagent. Results are written to
`tmp/perf/<label>.json`.

On a loaded machine, compare interleaved A/B runs: pass comma-separated
runs to `compare.mjs`, and it prints the median of each cell.

Findings that are easy to miss:

- assistant-ui drops its conversion cache whenever the `convertMessage`
  function changes identity. Keep it module-level.
- `rehype-highlight` builds a lowlight instance with ~37 languages every
  time it is attached, and react-markdown attaches plugins on every
  render. Keep one shared transformer.
- `content-visibility: auto` on message rows cut streaming blocking time
  by ~70%. It was not adopted because jump-to-message computes offsets from
  estimated row heights and missed its target by ~3600 px. Revisit only
  with a scroll-to-target that re-aligns after rows render.

## Open questions / things to revisit

- **Does B4 actually break composer freshness?** Needs a controlled
  test: open a session, send a prompt, watch the cost number.
  Verify that the DB-only path stays within ≤1 s of the live path.
- **What's the actual story on `/api/session/:id/changes`?** B5's
  instrumentation now logs `parts_ms` and `session_ms` separately
  whenever the operation exceeds 200 ms. Next time the endpoint
  shows up slow in `__ocmanPerf`, grep the server log for
  `op=session_changes` and look at the field breakdown. That
  tells us whether it's the parts query, the session metadata
  fetch, or the in-memory aggregation.
- **Connection reuse to OpenCode:** the upstream `http.Client` has
  no custom transport. Default `http.DefaultTransport` should reuse
  loopback connections, but the latencies were too high for warm
  reuse during the original measurements. After B1/B2/B3/B6 most
  per-call latency is now upstream-bounded, so this is less
  pressing. Revisit only if measurements show OpenCode itself
  responding instantly while ocman still reports proxy latency.

## Reference: relevant files

### Backend
- `internal/server/server.go`: mux assembly, middleware wiring.
- `internal/server/middleware.go`: request-timing logger.
- `internal/server/handlers.go`: `/api/sessions`, `/api/sessions/notify`,
  `applyGitInfo`.
- `internal/platforms/opencode/provider/adapter.go`: Sessions(), port discovery.
- `internal/platforms/opencode/provider/client.go`: upstream HTTP helpers
  (`getJSON`, lsof discovery, prompt fetch). Where B1/B3/B6 land.
- `internal/platforms/opencode/provider/operations.go`: per-session catalog
  methods (`AgentCatalog`, `SlashCommands`, `SessionModels`,
  `Session`). Where B4 lands.
- `internal/platforms/opencode/provider/info.go`: `SessionInfo` parallel
  fan-out. Where B2 lands.
- `internal/git/info.go`: git status cache (30 s TTL).

### Frontend
- `frontend/src/App.tsx`: top-level pollers and dev handles.
- `frontend/src/pages/Dashboard.tsx`: 5 s `/api/sessions` poller.
- `frontend/src/pages/SessionDetail.tsx`: the polling jungle.
- `frontend/src/lib/api.ts`: `fetchJSON` / `postJSON` (instrumented).
- `frontend/src/lib/perfRing.ts`: perf ring buffer.
- `frontend/src/lib/perfMonitor.ts`: interaction metrics (`__ocmanPerf.metrics()`).
- `frontend/perf/`: interaction bench (`pnpm perf`).
- `frontend/src/lib/useLongTaskMonitor.ts`: long-task observer.
- `frontend/src/components/BackendStats.tsx`: footer display.
