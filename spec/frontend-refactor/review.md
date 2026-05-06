# Frontend Refactor — Post-Mortem Review

> **Status**: complete
> **Started**: 2026-05-06
> **Reviewer**: senior-dev agent
> **Anchors**:
> - Pre-refactor: `22f1a04` (refactor: harden backend per spec/backend-hardening)
> - Last refactor commit: `c4cb630` (Phase 4 — SessionDetail decomposition)
> - Current HEAD (working tree): `fa6a174`
> - Refactor span: 7 commits (`0708c6c..c4cb630`)
> - Diff: **+11 974 / −5 449** across 60 files
> - Post-refactor follow-up commits: **30+** (many `fix:` / `perf:` patches)

## Goal

Identify bugs introduced by the 7-phase frontend refactor that are
either still latent in the tree or were patched at the symptom level
without addressing the root cause. Specific UX bias: **session
navigation locking up / becoming unresponsive when switching
sessions**.

## Scoring legend

For every extracted module / hook:

- **✓ identical** — semantically equivalent to pre-refactor.
- **⚠ drift** — behaviour differs but the difference is benign or
  intentional.
- **✗ bug** — a regression. Either still present in the tree, or
  patched in a follow-up commit (the patch quality is then audited
  separately).

For every follow-up `fix:` commit, we ask:

- **(a) symptom-only** — band-aid; root cause survives.
- **(b) partial** — fixes the proximate issue but leaves a sibling
  bug class.
- **(c) root-cause** — the underlying flaw is gone.

---

## Phase inventory

| Phase | Commit    | Title                                                            | Files touched | Status                |
| ----- | --------- | ---------------------------------------------------------------- | ------------- | --------------------- |
| 1     | `0708c6c` | Extract shared frontend helpers                                  | 15            | shipped               |
| 2     | `b1085d9` | Extract pure helpers from frontend mega-files                    | 16            | shipped               |
| 3     | `f404773` | Split api.ts into types module                                   | 2             | shipped               |
| 4.1   | `4b21fd4` | Relocate SessionDetail into pages/session-detail/                | 4             | shipped               |
| 4     | `c4cb630` | Decompose SessionDetail into 10 hooks (delivered 9)              | 17            | shipped, partial      |
| 5     | —         | Decompose Composer.tsx into hooks                                | —             | **NEVER SHIPPED**     |
| 6     | `d2988c1` | Extract chart options + display helpers from Dashboard           | 5             | shipped, partial      |
| 7     | `d435d89` | Replace stub tests with real behavioural coverage                | 5             | shipped               |

### Spec deviations (already known)

1. **Phase 5 never landed.** The architecture document promises 7
   composer hooks (`useRecording`, `useImageAttachments`,
   `useComposerDraft`, `useTokenPopover`, `useSlashCommands`,
   `useComposerInputHandlers`, `useComposerEventBridge`).
   `Composer.tsx` only received the Phase 2 pure-function
   extractions; it remains a 1 270-line monolith. Spec FR-4 is
   unmet; success criterion #1 (no file > 500 lines) is unmet.
2. **Phase 4 missed its <500 line target.** `SessionDetail.tsx`
   shipped at **1 882 lines**, not the promised ~400. The Phase 4
   commit message acknowledges this. Architecture's composition-root
   target is unmet.
3. **NFR-3 (no new dependencies) was waived.** Phase 4 added
   `@testing-library/react`, `@testing-library/jest-dom`,
   `@testing-library/user-event`, and `jsdom`. The waiver is
   reasonable (testing infra) and is documented in the commit
   message, but the spec's hard `No` was not amended.
4. **Phase 6 partial.** Dashboard chart helpers were extracted, but
   the per-tab file split (FR-9: `StatsTab.tsx`, `UsageTab.tsx`,
   etc.) was deferred. `Dashboard.tsx` is still a single file.

---

## Step 1 — Phase 1 & 2: pure-function extraction audit

**Approach**: for each helper, diff the pre-refactor inline definition
against the post-refactor module, then check the new module is the
sole call site (no orphaned duplicates).

### Summary

| Module                       | Verdict       | Notes                                                                                |
| ---------------------------- | ------------- | ------------------------------------------------------------------------------------ |
| `lib/sseMessageHelpers.ts`   | ✓ identical   | All 4 inline duplicates replaced; semantics preserved.                               |
| `lib/sessionStatus.ts`       | ✓ identical¹  | Phase 1 extraction was faithful; later behaviour change in `ae03fa9` is unrelated.   |
| `lib/taskId.ts`              | **✗ bug**     | **Behaviour drift in `OcmanRuntimeProvider`'s call site** — see 1.3.                 |
| `lib/mutedTools.ts`          | ✓ identical   | All 3 inline literal lists replaced with `isMutedTool`/`isMutedLineTool` predicates. |
| `lib/sidebarHelpers.ts`      | ✓ identical²  | Forward-compatible `notice` field added to hash; `rollupGroupStatus` byte-identical. |
| `lib/threadHelpers.ts`       | ✓ identical   | 14 functions extracted faithfully (sampled).                                         |
| `lib/convertMessages.ts`     | **✗ bug**     | **Render-loop bug surfaced by extraction** — see 1.7.                                |
| `lib/sseHelpers.ts`          | ✓ identical   | All 12 functions extracted faithfully.                                               |
| `lib/audio/encodeWav.ts`     | ✓ identical   | Byte-identical.                                                                      |
| `lib/models/contextWindows.ts` | ✓ identical | Byte-identical.                                                                      |
| `lib/commands/builtinCommands.ts` | ✓ identical | Byte-identical.                                                                |
| `lib/chartConfig.ts`         | ⚠ spec drift  | No `buildChartOptions(type, overrides)` factory as promised; just relocated consts.  |

¹ `sessionStatus.ts` later evolved (`ae03fa9`, `130916a`) — those are
documented in Step 3 as non-refactor changes.

² `sidebarHelpers.ts` notice field is benign — it's a forward-compat
hook that became live when rate-limit notices were added in `28752d5`.

### 1.1 `lib/sseMessageHelpers.ts` ✓ identical

All four inline `setMessages`/`setParts` patterns in the pre-refactor
SessionDetail SSE block (lines 1615, 1654, 1890 etc.) match the new
`insertMessageByTime` / `mergeParts` byte-for-byte. The fourth pattern
in the part-delta branch correctly stayed inline because it mutates a
single field rather than upserting (line 1780 pre-refactor).

`truncatePartField`, `MAX_OUTPUT_LEN`, and `inferStatusFromMessage` are
also identical to the inline equivalents at lines 55–60 and the inline
status-derivation block at lines 1632–1640.

No orphaned literals remain: `grep "temp-|error-|part-temp-"` returns
zero hits in production code outside the helper module.

### 1.2 `lib/sessionStatus.ts` ✓ identical at extraction

The Phase 1 extraction faithfully preserved every inline behaviour:

- `deriveRawStatus` matches the inline IIFE at lines 2752–2760 byte-
  for-byte.
- `computeLiveTokens` matches the inline IIFE at lines 1989–2005;
  the `if/continue` early-out is just a stylistic refactor.
- `mergeTokenStats` matches lines 2009–2018 byte-for-byte.
- `deriveActiveModelAndAgent` is more efficient (single backwards
  loop with break) but produces identical results to the pre-refactor
  double `[...messages].reverse().map(...).find(Boolean)` pattern.
- `formatModelRef` is byte-identical.
- `isSessionRunning` initially preserved pre-refactor's
  `role === 'user' → running`; the later flip to "user means done"
  came from `ae03fa9` and is **not a refactor regression** — it's a
  deliberate behaviour change keyed to the server-side
  `InferSessionStatus`.

Subsequent commits (`130916a`, `cec5bab`, `65840db`) modified
`isSessionRunning`'s signature significantly. Those are
post-refactor regressions and are audited under Step 3 / `useSessionStatus`.

### 1.3 `lib/taskId.ts` ✗ behaviour drift in OcmanRuntimeProvider

Pre-refactor had **two divergent inline implementations** of task-id
extraction:

- **`SessionDetail.tsx`** (line 2666): preferred `inp.task_id`,
  fell through to regex on output, then `output.task_id`, then
  `state.metadata.{sessionId, taskId, task_id}`.
- **`OcmanRuntimeProvider.tsx`** (line 309): assigned `inp.task_id`
  first **then unconditionally overwrote it** with the regex match
  on `output`. Order: `regex(output) ▸ inp.task_id ▸ output.task_id ▸ metadata.{sessionId,taskId,task_id}`.

The Phase 1 extraction unified onto **SessionDetail's** ordering
(`inp.task_id` highest priority). This silently changed
`OcmanRuntimeProvider`'s behaviour: when `inp.task_id` and the
regex-extracted output id differ, the renderer now uses the resume
input id instead of the actual spawned subagent id.

**Impact**: low frequency (only triggers when an OpenCode subagent
is resumed and the server forks a new sub-session id). When it does
trigger, clicking the subagent link in the thread navigates to the
stale resume id rather than the running session — the live preview
container won't update because the polling key is wrong.

**Severity**: ⚠ Latent. Likely never noticed because OpenCode's
common resume path keeps the ids equal. Reproducer requires a fork.

**Recommendation**: pick one canonical ordering and document it. If
"regex wins" was the right behaviour pre-refactor (output is the
authoritative current id), restore it. If "input wins" was always
wrong in ORP, leave it but add a regression test for the resume-fork
case.

### 1.4 `lib/mutedTools.ts` ✓ identical

Three inline literal disjunctions in `AssistantThread.tsx` (lines
170, 219, 709) replaced by `isMutedTool` / `isMutedLineTool`
predicates (line 187, 223, 402 in current file). Set membership is
byte-identical to the disjunctions; the `__skill__` superset is
preserved correctly.

### 1.5 `lib/sidebarHelpers.ts` ✓ identical

Three inline copies of `computeSidebarHash` in `SessionDetail.tsx`
(lines 1199, 1281, 2811) consolidated. The new helper adds a
`notice` field to the hash key; this field did not exist on
`Session` pre-refactor (added later by `28752d5`) and rendering it
into the hash is forward-compatible — no observable difference at
the time of the refactor.

`rollupGroupStatus` matches the inline `rollup` lambda at lines
2940–2965 of the pre-refactor file byte-for-byte.

**Note**: `SessionDetail.tsx` still calls `computeSidebarHash`
directly at lines 819 and 1565 in the post-refactor code, in
addition to the call inside `useSidebarSessions`. Investigated
under Step 3 — this is the **double-source-of-truth** pattern that
contributes to the navigation freeze (the page mirrors sidebar
state in two places after the hook split).

### 1.6 `lib/threadHelpers.ts` (from `AssistantThread.tsx`) ✓ identical

Sampled `splitToolArgs`, `parseQuestionAnswers` (most complex —
bespoke prose parser with quoted-string state machine),
`summarizePatch`, `extractPatchPayload`, `parseQuestions`. All match
the pre-refactor inline definitions byte-for-byte. The 351 unit
tests added in the same commit (`threadHelpers.test.ts`) cover
edge cases.

### 1.7 `lib/convertMessages.ts` ✗ refactor regression

**The headline finding of Step 1.** `convertMessages` was extracted
verbatim from `OcmanRuntimeProvider.tsx` in `b1085d9`. The function
itself is byte-identical to its inline ancestor — but the
*surrounding ORP file* lost a hidden invariant during the refactor.

Specifically, `OcmanRuntimeProvider.tsx` post-refactor still passes
a fresh object literal to `useExternalStoreRuntime` every render:

```tsx
// Phase 2 ORP (and pre-refactor)
const runtime = useExternalStoreRuntime({
  messages: converted,
  isRunning,
  convertMessage: (m) => m,
  onNew: async (message) => { ... },
});
```

Pre-refactor this was tolerated because:

1. ORP was rendered far less frequently (every state change
   bubbled through the monolithic SessionDetail's reconciliation,
   which limited ORP's render rate via prop stability).
2. `@assistant-ui/react`'s `useExternalStoreRuntime` runs
   `runtime.setAdapter(store)` in a `useEffect` with **no
   dependency array**, so a fresh adapter on every render
   triggers a `setAdapter` → store-subscriber notify →
   re-render → loop.

The refactor split ORP's parents into smaller components/hooks
(`useSessionMessages`, `useSubagentTracking`, etc.), each with
their own setState surface. ORP's render rate increased and the
latent `setAdapter` loop became observable — manifesting as
"Maximum update depth exceeded" thread crashes.

**Two follow-up fixes were required**:

- `a1d1140` "memoize useExternalStoreRuntime adapter to break
  render loop" — added `useMemo`/`useCallback` around `store` and
  `onNew`.
- `4f28cab` "stabilize convertMessages result array for
  useSyncExternalStore" — added a module-level
  `lastConvertedResult` guard so the outer array reference is
  stable when every per-message cache hits.

**Root-cause assessment of those fixes**:

- `a1d1140` is a (c) **root-cause** fix at the
  `useExternalStoreRuntime` boundary. ✓ properly addresses the
  setAdapter-on-every-render bug.
- `4f28cab` is a (b) **partial** fix. It guards the *outer*
  array, but the underlying problem is that **every call to
  `convertMessages` rebuilds `partsByMsg`**:
  ```ts
  const partsByMsg: Record<string, Part[]> = {};
  parts.forEach((p) => { ... });
  ```
  When the parts reference is stable, this rebuild is wasted
  work — and the `partsEqual()` check inside the per-message
  cache exists *only* to compensate. A stronger fix would memoise
  `partsByMsg` keyed on the parts array reference. Not a bug, but
  the layered caching is fragile and the test that survives a
  cache miss in `partsByMsg` but a hit in the outer array guard
  is not exercised.

There is also a **module-level singleton cache** concern:
`lastConvertedResult` is shared across **all** sessions. When the
user navigates from session A to session B, the first call from B
sees A's result and (if the messages happen to compare equal —
unlikely but possible) returns A's stale array. In practice the
length check almost always rejects the cross-session match, but
this is a code smell that should be a per-instance cache.

**Severity**: 🔴 was a critical bug; both root-cause fixes are
correct in spirit but `4f28cab` leaves the `partsByMsg` rebuild
hot path and the cross-session singleton cache as latent risks.

### 1.8 `lib/sseHelpers.ts` ✓ identical

Sampled `extractMessageFromEvent`, `isSessionStatusIdle`,
`extractPendingPermission`, `normalizeQuestionItems`,
`extractPendingQuestionFromPart`. All match pre-refactor inline
definitions byte-for-byte.

### 1.9 `lib/audio/encodeWav.ts` ✓ identical

Byte-for-byte against `Composer.tsx` lines 16–46.

### 1.10 `lib/models/contextWindows.ts` ✓ identical

Byte-for-byte against `Composer.tsx` lines 69–124. Same model list,
same `getContextWindow` fallback ordering, same `formatTokenCount`
thresholds.

### 1.11 `lib/commands/builtinCommands.ts` ✓ identical

`KNOWN_AGENTS` and `BUILTIN_COMMANDS` byte-for-byte against pre-
refactor.

### 1.12 `lib/chartConfig.ts` ⚠ spec drift, no behaviour drift

The architecture document (AD-8 / FR-9) promised:

```ts
export function buildChartOptions(type, overrides?) { ... }
```

The shipped module instead exports per-chart const objects
(`BAR_OPTIONS_TOKS`, `BAR_OPTIONS_DURATION`, etc.) — exactly the
pre-refactor inline structure relocated wholesale. No factory, no
unification. This means future chart additions still require
copy-pasting the `responsive: true, maintainAspectRatio: false,
animation: false as const` boilerplate.

No behavioural impact — the relocated objects are byte-identical to
the pre-refactor inline versions. But the spec FR-9 success
criterion ("Chart configuration boilerplate is extracted into a
`lib/chartConfig.ts` with a `buildChartOptions(type, overrides)`
factory") is unmet.

### Step 1 conclusions

- **2 real bugs** found in Phase 1/2:
  1. `extractTaskId` priority change in `OcmanRuntimeProvider` —
     latent, low frequency.
  2. `convertMessages` render-loop class — already patched by
     `a1d1140` (root-cause) and `4f28cab` (partial); the outer-array
     singleton cache and the `partsByMsg` rebuild remain fragile.
- **9 modules** extracted faithfully with no behaviour drift.
- **1 spec drift** (`chartConfig.ts` missing factory).
- **No findings** that explain the **navigation lock-up** symptom.
  That symptom is structural (hooks + effect ordering) and is
  expected to surface in Step 3, not in pure-function extractions.

---

## Step 2 — Phase 3: api.ts type split ✓ identical

| Check                                           | Result |
| ----------------------------------------------- | ------ |
| All 50 pre-refactor exported types present     | ✓ (one extra, `SessionNotice`, added by `28752d5`) |
| All types re-exported from `api.ts`             | ✓      |
| `tsc -b` clean across 78 consumer files         | ✓      |
| Function signatures on `api` object identical   | ✓      |

`api.ts` shrank from 1 197 → 615 lines (the post-refactor file is
larger than the spec's "~450 lines" target because of the long
re-export block, which is expected). No behaviour change.

No findings.

## Step 3 — Phase 4: SessionDetail hook decomposition

The big one. Refactor extracted **9 hooks** (spec promised 10 — the
missing one was `useComposerActions`). Page shrank from 3 574 → 1 882
lines initially, has since grown back to 2 200 lines from
follow-up patches.

### Summary

| Hook                       | Verdict       | Most-impactful finding                                                                              |
| -------------------------- | ------------- | --------------------------------------------------------------------------------------------------- |
| `useSessionMessages`       | ✓ + patched  | Stale-closure bug fixed by `fa6a174`. Root cause OK.                                               |
| `useSessionSSE`            | **✗ live bug**| `deltaBuffer.flush()` in cleanup writes session-A deltas into session-B parts state. **Latent.**   |
| `useSidebarSessions`       | ⚠ minor      | Polling effect tears down + restarts on every nav. Inefficient but correct.                         |
| `useSessionCapabilities`   | ✓ identical  | Session-change effect properly aborts in-flight requests.                                           |
| `usePromptHandlers`        | ✓ identical  | Self-dependent useCallback (anti-pattern, also pre-refactor).                                       |
| `useSessionStatus`         | ⚠ patched   | Three rounds of fixes (`65840db` → `cec5bab` → `130916a`) suggest the design is fragile.           |
| `useSubagentTracking`      | **✗ live bug**| `setSubagentTokens` is a fresh wrapper function every render; downstream effect deps are unstable. |
| `useTmuxActions`           | ✓ identical  | Cleanly extracted.                                                                                  |
| `useSessionShortcuts`      | ✓ identical  | Uses `useSyncRef` correctly.                                                                        |

### 3.1 `useSessionMessages` — patched

**Pre-refactor monolith bug**: `load()`'s `setLoading(false)` and state
updates fired unconditionally on resolution, even when the user had
already navigated away. Combined with `setSession(sessionA_data)`, the
page would briefly render A's session metadata under B's URL. The
`abortControllerRef` guard caught the in-flight request, but only at
the `signal?.aborted` check at the top — once past that, state-mutation
landed on B.

**Why pre-refactor didn't visibly break**: monolith reconciliation
absorbed the corrupt frame; the next legitimate `load(B)` resolved
quickly enough that user perception was masked.

**Why post-refactor surfaced it**: hook split increased SessionDetail's
render count and effect-ordering churn. Stale `setLoading(false)` from
A landed on B with `loading: true` momentarily, freezing the spinner;
`setSession(A_data)` could land mid-mount of B's effects, causing
SSE/refreshModels to subscribe to wrong session. Net result: visible
"navigation lockup".

**Fix**: `fa6a174` added `activeSessionIdRef.current === id` guards
around every state-mutating branch. ✓ **Root-cause fix (c).**

**Quality of the fix**: solid, follows the established
session-still-active pattern. The `activeSessionIdRef` is updated
synchronously at the top of the page render (line 235), so by the
time the async `await getSession(...)` resumes, the ref reflects the
new id.

### 3.2 `useSessionSSE` — **LIVE BUG**

**Found while auditing**: cleanup invokes `deltaBuffer.flush()`
*after* `cancelled = true`. The flush calls the parent's stable
`setParts` setter with whatever deltas were buffered for the
*previous* session. The parent's `setParts` is now wired to the
*new* session's parts state, so:

```
flush() → setParts((prev_B) => applyDelta(prev_B, sessionA_delta))
        → applyDelta finds no part with sessionA's partId in B's array
        → falls through to "create on first delta" branch
        → APPENDS A PHANTOM PART carrying sessionA's id/messageId/data
          to session B's parts state
```

The phantom part:

- Has `sessionId = sessionA's id` and `messageId = sessionA's m.id` —
  no message in B matches.
- Has `data = { type: 'text', text: '<delta fragment>' }` shape.
- `convertMessages` filters parts by `messageId`; with no matching
  message, this orphan is ignored by the renderer.
- However, **the orphan persists in B's `parts` state forever**
  (until B itself navigates away, at which point the next phantom
  arrives in the next session). Each navigation can leave behind
  one orphan in the new session.

**Reproducer (probed and confirmed by isolated unit test)**:

```ts
const setParts = vi.fn();
const buffer = createSseDeltaBuffer(setParts);
buffer.enqueue({ partId: 'pA', messageId: 'mA', sessionId: 'sessionA', field: 'text', delta: 'leftover from A' });
expect(setParts).not.toHaveBeenCalled();
buffer.flush();
expect(setParts).toHaveBeenCalledTimes(1);  // ← bug: setter is invoked after teardown
```

**Severity**: 🟡 latent. Symptoms:

1. Memory: `parts` arrays grow by ~1 per cross-session navigation
   while the user has streaming sessions. Cleared on full reload.
2. `useSubagentTracking.subagentSessionIds` recomputes on every
   `parts` change. Phantom parts may not match `isTaskTool` so it's
   harmless on that front. But every `parts` mutation also triggers
   `runningTaskIds` recompute and the `useEffect` in
   `useSubagentTracking` may fire `api.sessionTasks` for a stale id.
3. `convertMessages.partsByMsg` builds an index on every call;
   phantoms are O(1) overhead per call.

**Fix**: change cleanup to `deltaBuffer.cancel()` only (drop
`flush()`). The user has navigated away; the deltas they would have
seen are lost regardless. Or make `flush()` a no-op after the
buffer's owning consumer is gone.

```ts
return () => {
  cancelled = true;
  evtSource?.close();
  ...
- deltaBuffer.flush();    // ← drop this
- deltaBuffer.cancel();
+ deltaBuffer.cancel();   // ← keep only the cancel
  ...
};
```

The comment defending `flush()` ("so the user sees the final tokens
of the previous session before we tear down") is **false reasoning**:
the user has *navigated away*, they will not see those tokens. The
old code was symptom-driven cargo-culting.

**Other pattern audit**:

- `fa6a174`'s `isCurrentSession()` guard is applied at every async
  setState call site — except the cleanup path. Fix should also
  add a guard inside `commit()` that bails when the consumer is
  no longer mounted.
- `setMessages` / `setSession` for `message.created` and
  `message.updated` events are NOT coalesced. During heavy bursts
  these can still produce ~10 setState/sec per event type. The
  `setParts` coalescing addresses the dominant case (deltas at
  ~30 Hz) but not the rest. Future improvement.

### 3.3 `useSidebarSessions` — minor

The `loadRecentSessions` callback depends on `[getSessions, id]`.
`id` changes on every nav, so:

1. The polling `useEffect` (line 154) depends on `loadRecentSessions`.
2. On nav, `loadRecentSessions` is a fresh function.
3. The polling effect tears down + restarts.
4. `start()` re-arms a fresh `setInterval`.
5. **The just-fired interval is dropped**, the new one fires
   3 seconds later — so the sidebar refresh latency on nav is
   actually 3 s, not the immediate poll the comment promises.

The mount effect (line 133) does call `loadRecentSessions` once,
which masks this. So the user sees the fresh sidebar within one
microtask of nav, then the next refresh happens 3 s later. Pre-
refactor was inline and had the same dep chain. ⚠ Inefficiency, not
a regression.

**Note about cross-reference with SD line 819 / 1565**: the page
*also* calls `computeSidebarHash` directly twice in mirror effects.
These mirror effects (status mirror, permission/question mirror,
seen mirror, SSE-derived sidebar updates) write through
`setRecentSessions` from outside the hook. The hook exposes
`setRecentSessions` and `lastSiblingsHashRef` for this — comment in
the hook (line 76-79) acknowledges the split. **Code smell** but
not a bug; refactor consciously deferred unifying the writers.

### 3.4 `useSessionCapabilities` — identical

Three effects:

- `liveConnection → setPortAvailable(true)`: byte-identical to
  pre-refactor.
- Agent-catalog fetch: byte-identical, with the abort-on-unmount
  pattern preserved.
- Models refresh-on-port: properly aborts on dep change.

`refreshModels` only checks `signal?.aborted`, not `activeSessionIdRef`.
But the only caller passes a session-scoped controller's signal that
gets aborted on session change, so this is sufficient.

### 3.5 `usePromptHandlers` — identical

`handlePermissionReply`, `handleQuestionReply`, `handleQuestionReject`
each have themselves-as-deps via `answeringPermission` /
`answeringQuestion`. Standard inefficient-but-correct pattern; same
as pre-refactor.

The persisted `pendingQuestion` sessionStorage helpers
(`storePendingQuestion`/`loadPendingQuestion`/`clearPendingQuestion`)
are exported from this hook but are **also imported by
`useSessionSSE`** (line 24 in useSessionSSE.ts). This creates an
implicit hook → hook dependency. Not a bug, but a layering smell —
the persistence helpers should live in a sibling module.

### 3.6 `useSessionStatus` — patched (multiple rounds)

This hook went through three rounds of fixes:

1. **`65840db` (feat)** introduced `lastSseEventAt` + a 500 ms
   freshness window that upgraded `waiting → busy` if SSE fired
   recently. Added a setState in an effect to track the freshness
   tick.
2. **`cec5bab` (fix)** renamed to "avoid setState in SSE-freshness
   effect to stop right-panel crash" — the freshness setState was
   causing render loops in the right-panel error boundary.
3. **`130916a` (fix)** removed the `lastSseEventAt` mechanism
   entirely and replaced it with reading `sessionStatus`
   (server-authoritative status mirrored into the hook).

The current implementation reads `sessionStatus`, `awaitingAssistantResponse`,
and `recentWorkEventAt` to compute `rawOptimisticStatus`. The
`recentWorkEventAt` lives in `useSessionSSE` and is debounced
(throttled 100 ms) to avoid the original setState-storm.

**Quality of the fix chain**: the final design (`130916a`) is the
right one — separate the "raw signal" (event arrival) from the
"derived status" (which the renderer reads). The intermediate
`cec5bab` was a band-aid — it tried to use refs instead of state,
which would have masked the derived signal from re-rendering
consumers.

**Acceptable post-refactor regression**: the original monolith had
no SSE-freshness mechanism at all. The feature was added (`65840db`)
on top of the refactored hook and immediately broke; refactor itself
didn't introduce the loop, the new feature did. But the hook split
made the loop noticeable — pre-refactor a similar feature would
have been one more inline `useEffect`, not an effect split across
two hooks crossing a setState boundary.

**Render-loop annotations**: the hook contains 4 explicit render-loop
defenses (lines 154-158, 182, 206-210, 154/206 again). Each is a
written-out comment explaining why a particular pattern was chosen
to avoid a specific loop. This is fragile — a future refactor could
easily reintroduce the loop by reverting one of these to "natural"
React patterns.

### 3.7 `useSubagentTracking` — **LIVE BUG**

**Issue C1: setSubagentTokens is a fresh function on every render.**

```ts
// useSubagentTracking.ts line 83
const setSubagentTokens: Dispatch<SetStateAction<SubagentTokenMap>> = (next) => {
  setSubagentTokensRaw((prev) => {
    const updated = typeof next === 'function' ? (next as (p: SubagentTokenMap) => SubagentTokenMap)(prev) : next;
    return trimSubagentTokens(updated);
  });
};
```

This wrapper is recreated every render. Pre-refactor was inline,
so no wrapper was needed.

**Downstream impact**: `useSessionStatus` lists `setSubagentTokens` in
the dep array of its TPS-interval effect (line 265):

```ts
}, [isRunning, messages, subagentTokens, setSubagentTokens, ...]);
```

The fresh wrapper on every render of `useSubagentTracking` invalidates
this dep on every render → the TPS interval is torn down + recreated
on every parent render. Combined with `messages` (which is also a
fresh array reference per SSE event), the effect was already
re-running frequently. `setSubagentTokens` makes it worse.

**Fix**: wrap the setter in `useCallback`:

```ts
const setSubagentTokens = useCallback<Dispatch<SetStateAction<SubagentTokenMap>>>(
  (next) => {
    setSubagentTokensRaw((prev) => {
      const updated = typeof next === 'function'
        ? (next as (p: SubagentTokenMap) => SubagentTokenMap)(prev)
        : next;
      return trimSubagentTokens(updated);
    });
  },
  [],
);
```

**Severity**: 🟡 performance. Each render of `useSubagentTracking`
costs one `setInterval` teardown + creation. While streaming, parent
renders 10–30 Hz; the interval timer churn isn't free but it's not
catastrophic.

**Issue C2 (pre-existing, not a refactor regression)**:
`setTaskLiveOutput` accumulates indefinitely:

```ts
setTaskLiveOutput((prev) => ({ ...prev, ...tasks }));
```

No eviction. Tasks that complete leave their last-seen output in
the map. Pre-refactor had the same issue.

### 3.8 `useTmuxActions` — identical

Cleanly extracted. No dep cycles, all callbacks have minimal deps.
The picker's `mousedown` listener is properly attached/detached.
✓ Identical.

### 3.9 `useSessionShortcuts` — identical

Uses `useSyncRef` to break the dep cycle between handler identity
and shortcut descriptor identity. The pattern is correct: refs are
stable, descriptors built around them stay stable, `useShortcut`
doesn't re-bind on every render.

`navNextShortcut` / `navPrevShortcut` deps include `[jumpToSession]`,
which is recreated whenever `id` or `navigateToSession` (which
itself depends on `location.search` via `useStickyNavigate`)
changes. So these descriptors do rebind on nav. Whether
`useShortcut` short-circuits is up to its implementation; not
inspected here. ⚠ minor inefficiency.

### 3.X Cross-reference: post-refactor fix commits

| Commit    | Subject                                                                | Verdict                      |
| --------- | ---------------------------------------------------------------------- | ---------------------------- |
| `fa6a174` | fix: ignore stale session work after navigation                        | (c) root-cause ✓             |
| `bb3300c` | fix: auto-recover transient session thread crashes                     | **(a) symptom-only ⚠**       |
| `130916a` | fix: remove SSE freshness re-renders from session detail               | (c) root-cause ✓             |
| `cec5bab` | fix: avoid setState in SSE-freshness effect to stop right-panel crash  | (a) symptom; superseded      |
| `64ec0ea` | revert: remove 1 Hz TPS throttle, keep rounding only                   | revert (no judgment)         |
| `65840db` | feat: keep status busy while SSE events are fresh                      | feature, introduced regression |
| `45d0415` | fix: drop since from sessions cache key to stop heap leak              | backend (out of scope)       |
| `8af51bd` | fix: keep navigation responsive while sessions stream                  | (b) partial, see below       |
| `ae03fa9` | fix: treat trailing user message as done, not running                  | feature behaviour change     |
| `7b661e5` | perf: throttle composer tok/s indicator to 1 Hz with stable rounding   | reverted by `64ec0ea`        |
| `2901133` | perf: stabilize unstable hook inputs across hot render paths           | (c) root-cause ✓             |
| `a1d1140` | fix: memoize useExternalStoreRuntime adapter to break render loop      | (c) root-cause ✓             |
| `4f28cab` | fix: stabilize convertMessages result array for useSyncExternalStore   | (b) partial, see Step 1.7    |
| `c4d9a92` | fix: memoize Radix Toast subtree to prevent composeRefs render loop    | (c) root-cause ✓             |
| `c354dbb` | fix: stabilize render cascades and resolve all eslint warnings         | (c) root-cause ✓             |
| `6f1d026` | fix: break render-depth loops in SessionDetail and useSessionStatus    | (c) root-cause ✓             |
| `51da1cb` | lint: add eslint-plugin-react and fix render-stability violations      | preventive; ✓                |
| `759b4cb` | test: add max-update-depth regression guards for mount + fresh objects | preventive; ✓                |
| `5236631` | fix: guard ghost-injection effect against re-entry loops               | (c) root-cause ✓             |

#### Detail: `8af51bd` (navigation responsiveness)

This commit introduced **two** changes bundled together:

1. **rAF-coalesced delta buffer** (`sseDeltaBuffer.ts`). This is the
   actual fix for SSE-saturating-the-scheduler. Solid engineering.
2. **`id` threaded as prop instead of `useParams()`** in
   `index.tsx`. Comment claims react-router param context
   propagation gets contended under SSE pressure.

**Skepticism on (2)**: `useParams()` is just `useContext()` plus a
selector. Threading the value as a prop does not bypass any React
mechanism — both paths trigger the same `forceUpdate` on context
change. The actual effect of (2) is to **add a wrapper component
between react-router and SessionDetail**, which adds one extra
reconciliation step on each render. That extra step gives React
slightly more time to process queued effects between the URL change
and the inner re-render — but it's a side effect, not a fix.

**Most likely root cause** for "URL changed but inner never
re-rendered": the `convertMessages` render loop (`a1d1140` /
`4f28cab`) was so aggressive that React's render queue was
permanently behind, including the param-driven update. Once the
queue was drained (browser context switch, garbage collection,
etc.), the update came through.

The deeper fix landed in `2901133` (stabilise hook inputs) and
`a1d1140` (memo adapter); `8af51bd`'s prop indirection became
redundant. **Not removed** — the comment claiming necessity is
likely stale, but removing the indirection would require care
(integration tests assume the inner can be unit-tested with `id`
as a prop).

#### Detail: `bb3300c` (thread-boundary recovery)

Wraps the inner SessionDetail thread in an error boundary that
catches `tapClientLookup:*` errors and reloads. The error comes
from `@assistant-ui/react`'s external-store view when its
internal index doesn't match the message tree. **This is a
symptom of the convertMessages race**: when the WeakMap cache
returns inconsistent state (e.g. one render's `messages` array but
last render's `parts` array — possible during a partial commit),
the external-store sees a non-self-consistent snapshot.

The auto-recovery is a useful belt-and-braces, but the underlying
race deserves a real fix:

- `partsByMsg` should be memoized (per Step 1.7 recommendation).
- The per-message cache should include a content-hash, not just
  reference identity, so a partial commit (one of `messages` /
  `parts` reused, the other fresh) doesn't cache-hit incorrectly.

## Step 4 — Phase 5 (Composer) and Phase 6 (Dashboard)

### 4.1 Phase 5 — Composer hook decomposition: **NEVER SHIPPED**

The architecture document promised 7 composer hooks
(`useRecording`, `useImageAttachments`, `useComposerDraft`,
`useTokenPopover`, `useSlashCommands`, `useComposerInputHandlers`,
`useComposerEventBridge`). None landed.

**Current state of `Composer.tsx`**:

| Metric                          | Pre-refactor | Current  | Spec target |
| ------------------------------- | ------------ | -------- | ----------- |
| Total lines                     | 1 374        | 1 274    | < 500       |
| `useEffect` count               | 22           | 35       | minimal     |
| `useState` count                | unknown      | 12       | minimal     |
| Pure helpers extracted          | n/a          | ✓ (Phase 2) | ✓        |

The drop from 1 374 → 1 274 (~100 lines) is entirely from the Phase 2
pure-function extraction (encodeWav, MODEL_CONTEXT_WINDOWS,
BUILTIN_COMMANDS / KNOWN_AGENTS). The composition root itself is
**untouched**. The `useEffect` count actually **grew** by 13 since
pre-refactor, presumably from rate-limit handling, ghost injection,
worktree commands, etc. — features added on top of the unchanged
foundation.

**Risk assessment**:

- **Low risk for refactor regressions** — the un-refactored code is
  identical to pre-refactor, so anything still working pre-refactor
  still works here.
- **High risk for future regressions** — the 35 effects + 12 useStates
  in one file are the same maintainability hazard the refactor was
  meant to address. Every new feature is layered on top.
- **NFR-3 already waived** — the test infra was added (Phase 4 commit
  message), so the integration tests now exist. They should be
  exercised when Phase 5 finally lands.

**Net verdict**: ⚠ spec deviation, no behavioural drift. Document as
incomplete and prioritise as a follow-up.

### 4.2 Phase 6 — Dashboard ✓ identical (with bonus stabilisations)

`d2988c1` extracted chart options + display helpers as promised. The
spec promised a `buildChartOptions(type, overrides)` factory; this
was not delivered (per Step 1.12). Dashboard.tsx shrank from
1 365 → 1 223 lines — partial reduction.

**Bonus changes** (post-Phase-6, in `c354dbb` and `51da1cb`) hardened
Dashboard against React-query identity churn:

```ts
const sessions = sessionsQ.data ?? EMPTY_SESSIONS;          // stable
const projects = projectsQ.data ?? EMPTY_PROJECTS;          // stable
const ctx: DashboardCtx = useMemo(() => ({ ... }), [...]);  // memoised
const loadSessions = useCallback(() => { ... }, [refetchSessions]);
```

Pre-refactor (and post-Phase-6) had these as fresh references on
every render, which propagated through the `<Outlet>` context to
every tab. The post-refactor stabilisation is a clear improvement —
not strictly part of the refactor but a beneficial side effect of
the lint plugin pass.

**Per-tab file split (FR-9)**: deferred. `Dashboard.tsx` is still
one file with all tabs inlined. ⚠ spec gap.

## Step 5 — Phase 7: stub-test replacement ✓ shipped

The 5 stub tests promised in FR-8 were replaced with substantial
behavioural tests:

| File                        | Pre-refactor | Current  | Real coverage? |
| --------------------------- | ------------ | -------- | -------------- |
| `useCapabilities.test.ts`   | stub         | 235 LOC  | ✓ — module-level cache, in-flight Promise, subscriber set, fallback branches |
| `useSessionChanges.test.ts` | stub         | 150 LOC  | ✓ — debounce, abort, cache hit/miss |
| `useSessionInfo.test.ts`    | stub         | 175 LOC  | ✓ — debounce, abort, cache hit/miss |
| `useInfiniteRows.test.ts`   | stub         | 149 LOC  | ✓ — pagination state machine |
| `useWorkingTreeDiff.test.ts`| stub         | 149 LOC  | ✓ — debounce, abort |

All five tests follow the established "test the underlying logic
without `@testing-library/react`" pattern from `apiStore.test.ts`.
The pattern uses `vi.doMock('react', ...)` with a captured `useState`
setter, then drives the hook via the public surface and reads back
the captured state. It's awkward but it works.

NFR-4 (test coverage targets) is **met** for the stub-replacement
goal. The total frontend test count grew from ~600 pre-refactor to
**784** today — well beyond the "+100" target.

---

## Step 6 — Synthesis: open bugs and recommendations

### Real bugs found in this review

**The bugs below are ranked by impact on the navigation-lockup symptom
the user flagged, with severity and recommended fix.**

#### 🔴 B1 — `deltaBuffer.flush()` in SSE cleanup writes phantom parts to next session

**File**: `frontend/src/pages/session-detail/useSessionSSE.ts` line 707
**Test status**: confirmed by isolated probe (cleaned up; not committed)
**Symptom**: each cross-session navigation can leave one orphan part
in the new session's `parts` state. Drives:
- gradual `parts` array growth across navigations.
- spurious `runningTaskIds` recompute and `api.sessionTasks` polls if
  the orphan happens to look like a task.
- inconsistent state snapshot for `convertMessages`, contributing to
  `tapClientLookup:*` errors that `bb3300c` band-aids.

**Recommended fix** (3-line patch):

```diff
   return () => {
     cancelled = true;
     evtSource?.close();
     ...
-    deltaBuffer.flush();
     deltaBuffer.cancel();
```

Drop the `flush()`; `cancel()` already drops pending deltas. The
comment claiming "user sees final tokens before teardown" is wrong
because the user has navigated away.

**Confidence**: high. Confirmed mechanistically and via isolated probe.

#### 🔴 B2 — `convertMessages` cross-session singleton cache + unmemoised `partsByMsg`

**File**: `frontend/src/lib/convertMessages.ts`
**Symptom**: `lastConvertedResult` is module-level, shared across
all sessions. Combined with `convertMessage`'s WeakMap on individual
messages, occasional cache hits across sessions are theoretically
possible (mostly rejected by length check). The bigger latent issue
is `partsByMsg` rebuild on every call, which is the workload
`partsEqual()` exists to compensate for.

**Recommended fix**:

1. Move `lastConvertedResult` into a closure / per-instance cache
   factory keyed on session id.
2. Memoise `partsByMsg` keyed on the parts array reference.

**Confidence**: medium. Cross-session collision is unlikely in
practice; the refactor here is more about robustness than a known
production crash. The unmemoised `partsByMsg` is a perf bug, not a
correctness bug.

#### 🟡 B3 — `useSubagentTracking.setSubagentTokens` is a fresh function every render

**File**: `frontend/src/pages/session-detail/useSubagentTracking.ts` line 83
**Symptom**: `useSessionStatus`'s TPS-interval effect lists
`setSubagentTokens` in deps → tears down + recreates the 1 s
interval on every parent render. While streaming (~10–30 renders/s),
this thrashes the timer.

**Recommended fix** (5-line patch):

```diff
-  const setSubagentTokens: Dispatch<SetStateAction<SubagentTokenMap>> = (next) => {
+  const setSubagentTokens = useCallback<Dispatch<SetStateAction<SubagentTokenMap>>>((next) => {
     setSubagentTokensRaw((prev) => {
       const updated = typeof next === 'function' ? (next as (p: SubagentTokenMap) => SubagentTokenMap)(prev) : next;
       return trimSubagentTokens(updated);
     });
-  };
+  }, []);
```

**Confidence**: high. The pattern is unambiguous.

#### 🟡 B4 — `extractTaskId` priority change broke OcmanRuntimeProvider

**File**: `frontend/src/lib/taskId.ts`
**Symptom**: Pre-refactor, ORP's task-id extraction ran the regex
on `state.output` *after* reading `inp.task_id` and let it overwrite.
Post-refactor, `inp.task_id` wins. When OpenCode resumes a task and
the server forks a new sub-session id, the renderer uses the input
id (stale) instead of the output id (current). Subagent click
navigates to the wrong session.

**Recommended fix**: pick one ordering as canonical and document.
The pre-refactor SessionDetail order ("input first") matches
post-refactor; the pre-refactor ORP order ("regex first") was the
outlier. Either:

- Keep the unified ordering (input first) and add a regression test
  for the resume-fork case.
- Restore "regex first" if the resume-fork case is the common one;
  add a unit test pinning the priority.

**Confidence**: medium. Real but low frequency; hard to repro without
a specific OpenCode subagent flow.

#### 🟡 B5 — `bb3300c` thread-boundary recovery is symptom-only

**File**: `frontend/src/pages/session-detail/threadBoundaryRecovery.ts`
**Symptom**: catches `tapClientLookup:*` errors from
`@assistant-ui/react` and reloads. Hides the underlying race in
`convertMessages` (B2).

**Recommended fix**: keep the recovery as a safety net, but address
B2 to make the underlying error rare. A test that triggers the
error in CI would be valuable.

**Confidence**: high (about the symptom-only nature). Whether the
underlying race is fully eliminated by B2 needs validation.

### Spec deviations

| Item                                                 | Status                |
| ---------------------------------------------------- | --------------------- |
| FR-1: `SessionDetail.tsx` < 500 lines                | ✗ — currently 2 200   |
| FR-4: `Composer.tsx` decomposition                   | ✗ — never shipped     |
| FR-9: per-tab Dashboard files                        | ✗ — single file still |
| FR-9: `buildChartOptions(type, overrides)` factory   | ✗ — relocated consts  |
| Success #1: no file > 500 lines                      | ✗                     |
| NFR-3: no new deps                                   | ⚠ — testing infra waived |

### Spec wins

| Item                                                 | Status                |
| ---------------------------------------------------- | --------------------- |
| FR-2: SSE handler decomposition with shared helpers  | ✓                     |
| FR-3: pure functions extracted from SessionDetail    | ✓                     |
| FR-5: `convertMessages` extracted + tested           | ✓                     |
| FR-6: pure functions extracted from AssistantThread  | ✓                     |
| FR-7: `api.types.ts` split with re-exports           | ✓                     |
| FR-8: stub tests replaced with real coverage         | ✓                     |
| FR-10: `useSyncRef` utility introduced               | ✓                     |
| Test count grew by ~180 (target was +100)            | ✓                     |

### Patches that landed correctly (root-cause-level)

`fa6a174`, `130916a`, `2901133`, `a1d1140`, `c4d9a92`, `c354dbb`,
`6f1d026`, `5236631`, `759b4cb`, `51da1cb` are all root-cause fixes
or preventive measures. The author was diligent in rolling forward
from each render-loop discovery to a stable design.

`8af51bd` is a (b) partial fix:
- The rAF-coalesced delta buffer is solid.
- The `id`-as-prop indirection in `index.tsx` is **probably
  unnecessary** — the actual fix was the delta buffer plus
  `2901133`'s memoisation. The comment claiming `useParams()`
  context propagation gets contended is unsupported by any test
  that proves the failure mode. Worth investigating whether the
  indirection can be dropped.

`4f28cab` is a (b) partial fix:
- The outer-array stability guard is correct.
- The `partsByMsg` rebuild + module-level cache (B2) remain.

### Symptom-only patches

`bb3300c` (thread-boundary recovery) — see B5.

### Net assessment

The frontend refactor was **structurally successful but
incompletely landed**. The pure-function extractions and api.ts
type split (Phases 1–3) are sound. The SessionDetail hook
decomposition (Phase 4) shipped but immediately surfaced a
cluster of render-loop bugs that took ~12 follow-up commits to
stabilise. The Composer decomposition (Phase 5) was abandoned. The
Dashboard split (Phase 6) was halfway delivered.

The user's primary symptom — **navigation lockup under streaming
load** — has been substantially addressed by `8af51bd` (rAF buffer)
and `fa6a174` (active-session guards). The remaining live bug
**B1** (the cross-session phantom-part flush) is a likely contributor
to lingering oddities (memory growth, sporadic right-panel crashes)
rather than the headline freeze, but it should be fixed.

Three latent risks (B1, B2, B3) are present in the current tree.
B1 is the most concrete and has the cheapest fix. B4 is a real but
rare bug that's worth fixing for correctness. B5 is a band-aid that
should stay as a belt-and-braces but its underlying cause (B2)
deserves a real fix.

### Recommended next steps (in order)

1. **Fix B1** (3-line patch). Add a regression test that asserts
   the cleanup path doesn't call `setParts`.
2. **Fix B3** (5-line patch). Confirm the TPS-interval no longer
   churns by adding a render-count assertion to the existing
   useSessionStatus test.
3. **Fix B4**. Pick canonical ordering and add a regression test.
4. **Fix B2**. Move the cache to a per-instance factory; memoise
   `partsByMsg`.
5. **Investigate B5's necessity** once B2 lands. Keep it for safety
   but verify the underlying error rate drops.
6. **Land Phase 5** (Composer decomposition) per the original spec.
   The 35-effect monolith is a maintenance hazard.
7. **Re-evaluate `index.tsx`'s id-as-prop indirection**. Either
   document a reproducer that proves it's necessary, or drop it.

---

## Follow-up commit ledger

For traceability. Every commit landed *after* `c4cb630` that may be
refactor fallout. Audited in Step 3.X.

| Commit    | Subject                                                                | Suspected refactor link    | Verdict                |
| --------- | ---------------------------------------------------------------------- | -------------------------- | ---------------------- |
| `fa6a174` | fix: ignore stale session work after navigation                        | useSessionSSE / Messages   | (c) root-cause ✓       |
| `bb3300c` | fix: auto-recover transient session thread crashes                     | thread boundary recovery   | (a) symptom; see B5    |
| `130916a` | fix: remove SSE freshness re-renders from session detail               | useSessionStatus           | (c) root-cause ✓       |
| `cec5bab` | fix: avoid setState in SSE-freshness effect to stop right-panel crash  | useSessionStatus           | (a) symptom; superseded |
| `64ec0ea` | revert: remove 1 Hz TPS throttle, keep rounding only                   | useSessionStatus           | revert (no judgment)   |
| `65840db` | feat: keep status busy while SSE events are fresh                      | useSessionStatus           | feature, caused regression |
| `45d0415` | fix: drop since from sessions cache key to stop heap leak              | backend (out of scope)     | n/a (Go)               |
| `8af51bd` | fix: keep navigation responsive while sessions stream                  | navigation lock-up         | (b) partial; see 3.X   |
| `ae03fa9` | fix: treat trailing user message as done, not running                  | sessionStatus              | feature behaviour change |
| `7b661e5` | perf: throttle composer tok/s indicator to 1 Hz with stable rounding   | useSessionStatus           | reverted by `64ec0ea`  |
| `2901133` | perf: stabilize unstable hook inputs across hot render paths           | hook decomposition         | (c) root-cause ✓       |
| `a1d1140` | fix: memoize useExternalStoreRuntime adapter to break render loop      | OcmanRuntimeProvider       | (c) root-cause ✓       |
| `4f28cab` | fix: stabilize convertMessages result array for useSyncExternalStore   | convertMessages            | (b) partial; see 1.7   |
| `c4d9a92` | fix: memoize Radix Toast subtree to prevent composeRefs render loop    | render cascade             | (c) root-cause ✓       |
| `c354dbb` | fix: stabilize render cascades and resolve all eslint warnings         | render cascade             | (c) root-cause ✓       |
| `6f1d026` | fix: break render-depth loops in SessionDetail and useSessionStatus    | useSessionStatus           | (c) root-cause ✓       |
| `51da1cb` | lint: add eslint-plugin-react and fix render-stability violations      | render cascade             | preventive ✓           |
| `759b4cb` | test: add max-update-depth regression guards for mount + fresh objects | render cascade             | preventive ✓           |
| `5236631` | fix: guard ghost-injection effect against re-entry loops               | composer / SessionDetail   | (c) root-cause ✓       |
