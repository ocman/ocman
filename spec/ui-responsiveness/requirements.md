# Requirements: UI responsiveness under rapid clicking

## Problem

Clicking around the ocman UI quickly — switching sessions, flipping tabs,
changing filters, navigating between Dashboard / SessionDetail / ProjectDetail
— makes the app feel unresponsive. The browser tab pegs CPU, scroll
stutters, and previously-issued requests pile up in the network queue
(eventually all settling and triggering setState fan-outs into already-
unmounted components).

The frontend has no shared data-fetching library; instead, it uses
hand-rolled Zustand store methods around `fetch()`. `AbortController` is
used in only five files, most store getters do not accept an `AbortSignal`
at all, and several components run polling intervals that never cancel
their in-flight requests.

On top of that, a hot-loop bug on the SessionDetail page re-installs a
`setInterval(fn, 0)` on every render and reads Zustand state + queries
the DOM at ~250 Hz. Together with three `MutationObserver`s on the
assistant thread (one with `subtree: true, characterData: true`), every
SSE token delta during streaming forces a synchronous layout pass and
re-converts the entire message list.

## Goals

1. Rapid navigation (clicking through 5+ sessions or tabs in under a
   second) does not freeze the UI; clicks remain responsive within a
   single animation frame.
2. In-flight HTTP requests are cancelled when their result is no longer
   needed (component unmounted, filter changed, navigation away).
3. Steady-state CPU on a SessionDetail view (no streaming) is dominated
   by the SSE deltas themselves, not by background timers or DOM
   observers.
4. Streaming a long assistant message no longer triggers an O(M·P)
   re-conversion of the entire thread on every token.
5. The same logical data is not re-fetched by multiple components in
   parallel (notify endpoint, sessions list).

## Non-goals

- Reducing server-side response times beyond the small TTL caches noted
  in Wave 3.
- Replacing the assistant runtime / thread renderer.
- Adding offline / persisted client cache (out of scope; covered by
  the existing session-switch-cache work).
- Changing the SSE protocol.

## User-visible behaviour

### Rapid session switching

**Given** I open the dashboard with several sessions listed,
**When** I click through 5 different sessions in under a second,
**Then** each click navigates immediately, no click is dropped, and the
UI never freezes for more than one frame.

### Filter changes do not pile up

**Given** I am on the Dashboard or ProjectDetail page,
**When** I rapidly change the time-range or directory filter,
**Then** only the most recent filter's request actually completes;
earlier requests are aborted at the network layer.

### Streaming remains smooth on long threads

**Given** a session with 200+ messages is actively streaming an
assistant reply,
**When** the assistant emits hundreds of token deltas per second,
**Then** the page scrolls smoothly, the composer stays responsive, and
input events are not delayed by more than one frame.

### Notify polling is shared

**Given** the four notify hooks (favicon, notification, toast, bell) are
all active,
**When** they refresh,
**Then** only one HTTP request to `/api/sessions/notify` is in flight
at a time, and the result fans out to all four consumers.

## Findings (root causes)

### P0 — `setInterval(0)` re-installed every render
**File:** `frontend/src/pages/SessionDetail.tsx:621–661`

A `setInterval(..., 0)` polls Zustand state and queries the DOM (~250
Hz under browser clamping). Its dep array includes `tmux`, which
`useTmux()` returns as a fresh object literal on every render
(`frontend/src/lib/useTmux.ts:75`). Therefore the interval is torn down
and recreated on every render of SessionDetail. SessionDetail
re-renders on every SSE token during streaming, every store update,
and every typed character in the composer.

### P1 — MutationObserver storm during streaming
**Files:** `frontend/src/components/AssistantThread.tsx:1012–1015,
1058–1077, 1085–1091`

Three observers are attached to the thread/viewport with `subtree:
true`, one of them with `characterData: true`. During SSE streaming
the assistant text mutates character-by-character (each token delta
triggers a React re-render and a DOM text-node update). On each
mutation the observers fire and:
- force synchronous layout via `el.scrollTop = el.scrollHeight`
- walk the DOM via `hardenMessageLinks(thread)`
- re-query `offsetHeight` and re-observe the resize observer

### P2 — Four redundant polls of `/api/sessions/notify`
**Files:** `frontend/src/lib/useFaviconNotify.ts:177`,
`frontend/src/lib/useNotificationNotify.ts:247`,
`frontend/src/lib/useToastNotify.ts:283`,
`frontend/src/lib/useBellNotify.ts:118`

Each hook independently polls the same endpoint every 10 s with
disjoint timers, yielding 4× the network traffic and 4 setState
fan-outs per cycle. Three of the four also keep polling while the
tab is hidden.

### P3 — `convertMessages` rebuilds on every SSE delta
**File:** `frontend/src/components/OcmanRuntimeProvider.tsx:58–516,
594–597`

The conversion runs through every message and every part on every
change. For a session with 200 messages and a few hundred parts,
every SSE delta runs the whole pipeline — including
`JSON.parse(p.data)` for every part and a `new Date(...)` per
message.

### P4 — Most fetches lack `AbortSignal` plumbing
**Files:** `frontend/src/lib/apiStore.ts:151–202` (most store getters);
`frontend/src/lib/api.ts` (most POST helpers, plus a few GET
fallbacks); `frontend/src/pages/Dashboard.tsx:128–140`,
`frontend/src/pages/ProjectDetail.tsx:71–125`

When the user clicks rapidly between routes / tabs / filters,
in-flight requests pile up: they are not cancelled, they all
eventually settle, and each one queues a setState into the React
render loop — even when the component has unmounted (the `cancelled`
local boolean only prevents the setState, not the network call or
JSON parse).

### P5 — Three components polling `/api/sessions` independently
**Files:** `frontend/src/pages/SessionDetail.tsx:1200`,
`frontend/src/pages/Dashboard.tsx:159`,
`frontend/src/pages/ProjectDetail.tsx:123`

`/api/sessions` is the single most expensive read on the backend for
OpenCode (`lsof` + per-instance HTTP probes — see
`internal/platforms/opencode/adapter.go:130–162`). Three components
each run their own poller with intervals of 3 s / 5 s / 5 s. Backend
work is duplicated and the frontend re-parses the same JSON multiple
times per cycle.

### P6 — `useTmux` returns a fresh object per render
**File:** `frontend/src/lib/useTmux.ts:75`

Causes downstream effect dependency churn. Direct enabler of P0's
re-install loop.

### P7 — Per-task 2 s polling fan-out
**File:** `frontend/src/pages/SessionDetail.tsx:2664`

Each running subagent task issues its own `/api/session/{taskId}`
request every 2 s. With several concurrent subagents this is a
steady stream of small requests.

### P8 — Backend `/api/sessions` recomputes pending prompts per call
**File:** `internal/platforms/opencode/adapter.go:130–162`

Lsof discovery is cached, but per-instance pending-prompt HTTP probes
run on every `/api/sessions` request. Compounded by the frontend's
multiple pollers (P5).

### P9 — Layout-forcing writes inside MutationObserver callbacks
**File:** `frontend/src/components/AssistantThread.tsx:1087`

Each callback issues a layout-forcing read+write
(`scrollTop = scrollHeight`). Combined with subtree observation
during streaming, this guarantees layout thrash.

## Acceptance signals

- The Performance tab in DevTools shows no recurring task longer than
  ~16 ms during steady-state SessionDetail browsing (no streaming).
- During an active assistant stream, scripting time per frame is
  bounded so the page maintains ≥30 FPS scrolling on a long thread.
- The Network panel shows at most one `/api/sessions/notify` and one
  `/api/sessions` request in flight at a time across all routes.
- Rapidly changing the Dashboard time-range filter results in only the
  final request returning a 200; earlier requests show as cancelled.
- Navigating away from SessionDetail mid-load cancels the in-flight
  `/api/session/{id}` request.
