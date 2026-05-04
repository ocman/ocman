# Architecture: UI responsiveness under rapid clicking

This plan is staged into three waves so each wave delivers a
measurable win on its own and can land independently.

## Wave 1 — Quick wins (high impact, low risk)

Goal: eliminate the runaway hot loops and the four-fold notify polling.
Estimated effort: ~1 day.

### 1. Replace the `setInterval(0)` hot loop (P0, P6)

**Files:** `frontend/src/pages/SessionDetail.tsx:621–661`,
`frontend/src/lib/useTmux.ts:75`

- In `useTmux`, wrap the return value in `useMemo` keyed on
  `[available, isLocal, sessions, clients, switchSession, findSession,
  launchOpencode]` so consumers get a stable identity.
- Replace the `setInterval(..., 0)` with a Zustand subscription:

```tsx
const paletteCommand = useUiStore((s) => s.paletteCommand);
useEffect(() => {
  if (!paletteCommand || paletteCommand.kind !== 'scoped') return;
  useUiStore.getState().closePalette();
  // ... existing dispatch logic, taking values from refs
}, [paletteCommand]);
```

  The dispatch logic already uses `*Ref.current` for session, model,
  caps, etc., so the effect can stay independent of `tmux` /
  `archiveSession` / `createSession` / `navigate` (use refs or move
  these accesses inside the effect closure). The aim is a dep array
  that changes only when `paletteCommand` changes.

Validation:
- Profile SessionDetail in DevTools; the steady-state CPU should drop
  to near-zero between SSE events.
- Confirm scoped commands (`/model`, `/agent`, `/archive`, etc.) still
  fire from the command palette.

### 2. Tame the `MutationObserver` storm (P1, P9)

**File:** `frontend/src/components/AssistantThread.tsx:1085–1091` and
1012–1015 and 1058–1077.

- Drop `characterData: true` from the auto-scroll observer. Remove
  the observer entirely and run auto-scroll from a `useLayoutEffect`
  inside the thread component, keyed on `messages.length` and a
  monotonically-increasing `partsRevision` counter (incremented in
  `SessionDetail` whenever `parts` changes). This way the scroll
  happens on React's commit boundary instead of the DOM-mutation
  boundary.
- For the link-hardener observer (line 1012), replace with a
  `useEffect` keyed on `messages.length` that calls
  `hardenMessageLinks` once per render commit. If finer granularity is
  needed for tool-call output, schedule via `requestIdleCallback`
  (with a 200 ms timeout fallback).
- For the bottom-inset observer (line 1058), replace with a
  React-state-driven height read in a `useLayoutEffect` keyed on
  whether the composer / permission / question variant is mounted
  (these mounts are already tracked in store state).
- Wrap any remaining observer callback in a
  `requestAnimationFrame`-coalesced helper (single trailing-edge run
  per frame) so layout-forcing writes happen at most once per frame.

Validation:
- During a long streaming reply (say 30 s of generation on a 200-msg
  thread), the Performance tab should show no observer task >2 ms;
  scroll-to-bottom should still pin to the latest token; clicking
  internal Markdown links still gets the hardened behaviour
  (`MarkdownLink` already covers most cases — the observer is the
  fallback for dynamically-inserted HTML).

### 3. Coalesce the four `/api/sessions/notify` pollers (P2)

**Files:** `frontend/src/lib/useFaviconNotify.ts`,
`frontend/src/lib/useNotificationNotify.ts`,
`frontend/src/lib/useToastNotify.ts`,
`frontend/src/lib/useBellNotify.ts`

Add a new shared hook + Zustand slice:

```ts
// frontend/src/lib/useNotifyData.ts
export const useNotifyStore = create<NotifyState>((set, get) => ({
  data: null, lastFetched: 0, refCount: 0,
  start: () => { /* increment refCount, kick interval if first */ },
  stop:  () => { /* decrement, stop interval if zero */ },
}));
export function useNotifyData() {
  // bumps refCount on mount, polls every 10s while document.visible,
  // pauses while document.hidden, returns shared latest payload.
}
```

- Each existing hook becomes a thin selector over `useNotifyData()` and
  derives its own concern (favicon dot, OS notification, toast, bell).
- One in-flight request, one parse, four consumers.
- Pause polling on `document.hidden`; resume + immediate refetch on
  `visibilitychange`.

Validation:
- DevTools Network panel: at most one `/api/sessions/notify` per 10 s
  while the tab is visible; zero while hidden.
- All four UI cues still fire when expected.

### 4. Add `AbortSignal` to Dashboard / ProjectDetail polls (P4 — partial)

**Files:** `frontend/src/pages/Dashboard.tsx:128–140, 159`,
`frontend/src/pages/ProjectDetail.tsx:71–125`

- Hold an `AbortController` in a ref. On each new fetch (initial,
  filter change, or interval tick), abort the previous controller and
  create a new one. Pass `controller.signal` to `getSessions(...)`.
- Pause the interval when `document.hidden`.
- Apply the same pattern to the Stats and Usage tabs in `Dashboard`
  (`Dashboard.tsx:333–370, 896–930`).

This requires plumbing `signal` through the relevant store methods
(see Wave 2 for the full sweep). For Wave 1, only the methods used
by these polls need updating: `getSessions`, `getStats`,
`getProjects`, `getActivity`, `getModels`, `getHourly`,
`getHourlyTokens`. The remaining store methods can be done in Wave 2.

## Wave 2 — Cancellation plumbing & memoisation (medium effort, medium risk)

Goal: every GET in the app honours an `AbortSignal`; the streaming hot
path no longer rebuilds the whole thread per token.
Estimated effort: ~1–2 days.

### 5. Sweep `AbortSignal` through every store GET (P4)

**Files:** `frontend/src/lib/apiStore.ts:151–202`,
`frontend/src/lib/api.ts` (GET helpers without `signal`).

- Add an optional `signal?: AbortSignal` to every store getter that
  wraps a GET (`getStats`, `getProjects`, `getActivity`, `getModels`,
  `getHourly`, `getHourlyTokens`, `getCapabilities`, `getMetrics`,
  `getTmuxClients`, `getTmuxSessions`, `getSystemStats`).
- Update `runRequest` to detect `AbortError` and skip writing
  `requests[key]` for aborted calls (they aren't real failures).
- Update every call site to either pass a signal from a ref-controlled
  controller or to ignore the result on `AbortError`.

For mutating POSTs (`archiveSession`, `markSessionSeen`, etc.),
signals are optional — most should NOT be cancelled mid-flight to
avoid leaving the server in a half-applied state. Document this in a
comment on `api.ts`.

Validation:
- Click rapidly through 5 sessions in <1 s; only the last
  `/api/session/{id}` returns 200, the rest show as cancelled in the
  Network panel.
- Likewise for filter rapid-fire on Dashboard.

### 6. Per-message memoisation in `OcmanRuntimeProvider` (P3)

**File:** `frontend/src/components/OcmanRuntimeProvider.tsx:58–597`

- Introduce two `WeakMap` caches keyed on `Part` and `Message` object
  identity:
  - `parsedPartCache: WeakMap<Part, ParsedPart>` for `parsePart`
    output, keyed on the part's reference (immutable updates already
    create a fresh ref when content changes).
  - `convertedMessageCache: WeakMap<Message, ConvertedMessage>` for
    the per-message conversion output, keyed on `(message,
    partsForMessage)` reference identity.
- Refactor the `useMemo` to iterate `messages` and either reuse the
  cached `ConvertedMessage` (if the message reference and its parts
  array haven't changed) or recompute only that one entry.

This requires `SessionDetail` to update parts immutably *per message*
rather than rebuilding the whole `parts` array on every delta — i.e.
when an SSE delta updates message X's part Y, only X's parts array
should get a new identity. Inspect
`SessionDetail.tsx:1695–1751` (`message.part.delta`) and
adjust the parts-grouping logic to be per-message-id.

Validation:
- During a streaming reply on a 200-msg thread, the Profiler shows
  conversion work scaling with deltas-per-second × parts-of-current-
  message, not × total-parts.

### 7. Eliminate `cancelled` flags where signals replace them

Many effects use `let cancelled = true` to gate setState after the
component has unmounted but still let the network call complete.
With Wave 2 in place, the network call itself is cancelled — the
`cancelled` flag becomes redundant in those effects. Remove for
clarity (this is purely a tidy-up, not behaviour-changing).

## Wave 3 — Structural changes (larger effort, larger payoff)

Goal: stop reinventing data-fetching by hand, share state across
components that read the same data, and reduce backend pressure.
Estimated effort: ~2–3 days plus migration risk.

### 8. Adopt TanStack Query for GETs (P4, P5)

Add `@tanstack/react-query` and migrate GET-style data flows. We get
for free:
- Per-key dedup: only one in-flight request per `(key, args)`.
- Automatic cancellation when a query unmounts or its key changes.
- Stale-while-revalidate: instant render from cache, background
  refetch.
- Window-focus refetch and visibility pausing.
- Built-in retry with exponential backoff.

Migration approach (incremental, can land per-route):
1. Add the provider at the App root with sensible defaults
   (`staleTime: 10_000`, `refetchInterval` per query, no auto-retry
   on 4xx).
2. Replace `getSessions` polling on Dashboard / SessionDetail-sidebar
   / ProjectDetail with a single `useSessions(filters)` query. Each
   consumer chooses its own `refetchInterval`; TanStack still dedups
   if keys match. Where keys differ (e.g. directory filter), the
   server work is needed anyway.
3. Replace `getStats`, `getProjects`, `getActivity`, `getModels`,
   `getHourly`, `getHourlyTokens` similarly.
4. The session-detail LRU cache (`apiStore.ts:84–122`) becomes
   redundant once `useSession(id)` is a TanStack query — remove.
5. The `apiStore` Zustand store stays for mutations, command palette
   state, sidebar layout, etc.

This is the structural fix for P4 and P5; many of the Wave 1 / Wave 2
patches can be simplified once it lands.

### 9. Push pending-prompt state via SSE (P8)

**Files:** `internal/platforms/opencode/adapter.go:130–162`,
`internal/server/handlers.go:327–398`,
`frontend/src/pages/SessionDetail.tsx` SSE handler.

- Add a short TTL cache (~1 s) around the per-instance pending-prompt
  collection in the OpenCode adapter; this neutralises the cost of
  multiple frontend pollers without changing behaviour. (Quick fix.)
- Longer-term: extend the existing `/api/session/{id}/events` SSE
  stream to push `pendingPermission` / `pendingQuestion` deltas as
  they arrive, and make the sessions-list response include only a
  cached snapshot. The frontend already maintains an SSE connection
  per session-detail view; the dashboard can subscribe to a
  per-platform feed (`/api/events?platform=opencode`).

### 10. Batch task-output polling (P7)

**File:** `frontend/src/pages/SessionDetail.tsx:2664`

- Replace the per-task `setInterval` with a single endpoint
  `/api/session/{id}/tasks` that returns the latest output for all
  running tasks of a session in one response.
- Or piggyback task-output deltas on the existing SSE stream
  (preferred — it's already running).

## Rollout & verification

Each wave is independently shippable. Suggested order:

1. Land Wave 1 as a single PR. Manually verify the four user-visible
   scenarios from `requirements.md` and watch the DevTools Performance
   tab during streaming.
2. Land Wave 2 as one or two PRs (signal sweep + memoisation can
   split). Add a regression test or two:
   - A unit test that `convertMessages` reuses cached entries when
     message identity doesn't change.
   - An integration-ish test in `SessionDetail.test.tsx` that
     navigation aborts the previous fetch.
3. Wave 3 as a multi-PR migration: scaffold TanStack Query, then
   migrate one query family per PR (sessions list, session detail,
   stats, etc.). Backend SSE push is a separate PR per spec
   section 9.

## Out of scope

- Replacing Zustand wholesale.
- Server-side response-time work beyond the 1 s pending-prompt cache
  (tracked separately if needed).
- Persisted IndexedDB cache (covered by `spec/session-switch-cache/`).

## Risks

- **Breaking the command-palette dispatch (P0 fix)**: the existing
  `setInterval(0)` is a quirky imperative loop. Rewriting as a
  selector-driven effect must preserve the exact same set of
  scoped-command behaviours. Cover with a Vitest that simulates each
  scoped command via `useUiStore.setState({ paletteCommand: ... })`.
- **Auto-scroll regressions (P1 fix)**: switching from a DOM
  observer to React-commit-driven scroll must not lag behind the
  last token. Validate by streaming a long reply and confirming the
  viewport stays pinned to the bottom.
- **TanStack Query bundle size (Wave 3)**: ~13 kB gzipped. Acceptable
  given we replace a lot of bespoke code.
- **Per-message parts refactor (P3)**: this touches the SSE delta
  handler — the highest-traffic code path in the app. Ship behind a
  conservative test pass and double-check tool-call updates,
  permission requests, and reasoning blocks still render correctly.
