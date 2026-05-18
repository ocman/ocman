# Architecture: Simpler live session pipeline

## The whole thing in one paragraph

The SessionDetail page owns one piece of state: `SessionView`. On
mount it fetches `/api/session/{id}` and `setView(rest)`. It opens
`/api/session/{id}/events` and runs every event through one pure
reducer that returns the next `SessionView`. On disconnect or page
unmount the EventSource closes; on reconnect we refetch and replace
the view wholesale. Optimistic user messages live outside the view
in a small "pending" slot. That's it.

## The view shape

```ts
interface SessionView {
  session: SessionMetadata;       // header info, status, agent, etc.
  messages: Message[];            // chronological, no temp IDs
  parts: Part[];                  // chronological per message
  // Live-only side channels — not derived from messages/parts:
  pendingPermission: Permission | null;
  pendingQuestion: Question | null;
}
```

`Message` and `Part` keep their wire shape (`Part.data` is the raw
OpenCode part object). `parts[]` is a flat list, not a Record keyed
by message id — the converter (renderer) buckets at render time.

## The reducer

```ts
type Action =
  | { type: 'load'; view: SessionView }            // REST result
  | { type: 'sse'; event: SseEvent }               // any SSE event
  | { type: 'clearPrompt'; kind: 'permission' | 'question'; id: string };

function reduce(state: SessionView, action: Action): SessionView;
```

One function. Pure. Lives in `frontend/src/lib/sessionReducer.ts`.
Tested directly without React.

### SSE event handling — exhaustive table

| Event                        | Effect on view                                                                 |
|------------------------------|--------------------------------------------------------------------------------|
| `message.created`            | Upsert `info` into `messages` (sorted by `timeCreated`). Append embedded `parts[]` to `parts` (id-deduped, replace on collision). |
| `message.updated`            | Same as `message.created`. The two events differ only on the server side. |
| `message.part.updated`       | Upsert the single part into `parts`. Snapshot wins on fields that have not received streaming deltas; delta-owned streaming fields keep the local accumulated value. |
| `message.part.delta`         | Append `delta` to `parts[partId].data[field]` (dotted path resolved). If the part doesn't exist, synthesise a stub. Mark that `(partId, field)` as delta-owned. |
| `session.status`             | Set `session.status` from `properties.status`. |
| `session.idle`               | Set `session.status = 'done'`, then refetch `/api/session/{id}` and dispatch `load` to reconcile any final content the stream missed. |
| `permission.asked`           | Set `pendingPermission`. |
| `permission.replied`         | Clear `pendingPermission` if the id matches. |
| `question.asked`             | Set `pendingQuestion`. |
| `question.replied` / `.rejected` | Clear `pendingQuestion` if the id matches. |
| anything else for our session | Ignored only after the event-shape audit below confirms it is not a live content event; counted in a debug metric. |
| any event whose `sessionID` ≠ ours | Ignored (subagent bubbling is a separate concern — see below). |

### Why delta-owned fields are special

Snapshots are authoritative for most fields because they carry the
latest status, timing, tool metadata, inputs, and completion state.
Streaming content is different: once we have observed a
`message.part.delta` for a field, that field's local value is the
only value the user has already seen grow token-by-token. A later
snapshot can be stale relative to the delta stream, even when SSE
delivery itself is ordered, because the snapshot may have been
serialised from older server state.

The reducer therefore tracks `(partId, field)` pairs that have ever
received a delta. For those fields (`text` and `state.output` today),
deltas remain authoritative for the life of the part and snapshots
must not overwrite the local accumulated value. This is not the old
length-based heuristic; it is a deterministic ownership rule.

### Why buffering is not part of correctness

React 18 batches setState calls within the same event-loop tick.
EventSource fires events one at a time on the microtask queue;
they get batched naturally. Buffering may still be useful as a
performance optimisation to cap commits at paint rate, but it must
not define correctness. The reducer's behaviour must be identical
whether deltas are applied immediately or flushed once per animation
frame.

If profiling shows a hot stream causes jank, first prefer render-
boundary fixes such as `useDeferredValue`. Keep any rAF coalescing
small, local, and covered by reducer tests that prove snapshots and
deltas reconcile the same way with or without buffering.

## Event-shape audit before deleting catch-all handling

Before the legacy catch-all branch is removed, capture a representative
set of OpenCode SSE events from live sessions and tests:

- assistant text streaming,
- bash/tool execution,
- edit/write diffs,
- permission prompts,
- question prompts and replies,
- task/subagent events,
- reconnect after a dropped stream.

The audit must list every observed event `type`, named EventSource
channel, and payload shape that carries message or part content. Only
after the table above covers that corpus can unknown events be safely
ignored.

## The hook

```ts
function useSession(id: string): SessionView & {
  reload: () => void;
  status: 'loading' | 'live' | 'reconnecting' | 'error';
}
```

Internals:

- One `useReducer(reduce, initialView)`.
- One `useEffect` keyed on `id` that:
  - fetches `/api/session/{id}` once and dispatches `{type: 'load'}`,
  - opens the EventSource and dispatches `{type: 'sse', event}` for
    each message,
  - on `onerror`, closes the source, sets `status: 'reconnecting'`,
    and schedules a reconnect via existing `sseBackoff.ts`,
  - on reconnect (`onopen` after a failure), re-fetches and
    dispatches `load` again so the gap is healed in one shot.
  - on `session.idle`, dispatches the incremental status update and
    then re-fetches once so the final view is authoritative.
- One `reload()` function exposed so the user can force a refresh
  (used by the existing "Retry now" affordance).

Total: about 80 lines of hook code.

## The composer / optimistic send

Today: optimistic message gets `id = 'temp-' + Date.now()`, inserted
into `messages[]` and later reconciled via id-prefix filtering plus
the new `reparentTempParts` dance.

New: the composer page owns a `pending` slot.

```ts
const [pending, setPending] = useState<PendingSend | null>(null);

async function send(text, images) {
  setPending({ text, images, startedAt: Date.now() });
  try {
    await api.sendMessage(sessionId, text, images);
    // pending is cleared by the reducer when message.created lands
    // for a user message we don't yet have, OR by a timeout fallback.
  } catch (err) {
    setPending({ ...pending, error: String(err) });
  }
}
```

The render path concatenates `pending` after the last real message
when there is one, so the bubble is visible immediately. The
reducer clears `pending` when a `message.created` user message
lands that wasn't in our previous `messages[]`. No synthetic IDs.
No reparenting. The `failedSends` retry flow gets re-expressed as
"replay a `PendingSend`".

## The converter

`convertMessages` stays — it's mapping internal shape to
assistant-ui shape and that's a real domain transform. But:

- Drop the WeakMap result cache. Re-running the conversion is cheap;
  measure if it isn't.
- Drop the WeakMap parsed-data cache. Same.
- Keep the closure-instance reuse via `useMemo([sessionId])` because
  that's React-level memoization, not data-layer caching.

If the rewrite measurably regresses render performance, add
React.memo at message-bubble granularity, not data-layer caches.

## Subagent / task event bubbling

Today: subagent SSE events from a Task tool get routed into the
parent session's view via `subagentSessionIdsRef`. We keep this,
implemented as a second `useSession(subagentId)` per active task,
composed at the converter level (the `__task__` renderer already
embeds a child thread). No special-case routing in the parent's
reducer.

## What gets deleted

- `frontend/src/lib/partReducer.ts` (replaced by reducer above)
- `frontend/src/lib/partReducer.test.ts`
- `frontend/src/pages/session-detail/sseDeltaBuffer.ts` (already
  removed in current branch — stays removed)
- `frontend/src/pages/session-detail/sseDeltaBuffer.test.ts`
- The `mergeParts`, `upsertPart`, length-non-decreasing helpers in
  `frontend/src/lib/sseMessageHelpers.ts` — replaced by the reducer.
  `truncatePartField`, `insertMessageByTime`, `inferStatusFromMessage`
  stay.
- The `reparentTempParts` helper — no longer needed.
- The catch-all SSE branch in `useSessionSSE.ts` — explicit table or
  drop on the floor.
- The optimistic-id logic in `useSessionActions.ts:handleSend` —
  replaced by the `pending` slot.
- The cache invalidation logic in `convertMessages.ts` (the
  `convertedMessageCache`, `parsedPartCache`, `partsEqual`, the
  result-array reference cache). Maybe 100 lines.

## What stays

- `extractMessageFromEvent`, `extractPendingPermission`,
  `extractPendingQuestion` — they're just parsers for the wire
  shape and they're fine.
- `sseBackoff.ts` — reconnect schedule is solid.
- `useSessionActions.ts` — minus the optimistic-id slice.
- `useApiStore.ts` cross-session cache — different concern, untouched.
- Memory trimming (`MAX_RETAINED_MESSAGES`) — runs against the
  reducer output, not as a separate `setMessages` call.

## Migration / rollout

This is a rewrite, not an incremental refactor. The path:

1. Build `sessionReducer.ts` + `useSession.ts` next to the existing
   code in a feature-flagged path. New module, no consumers yet.
2. Port `SessionDetail.tsx` behind a `USE_NEW_SSE` flag (env var or
   a debug query param). Both implementations exist in parallel.
3. Run integration tests against both. Add the five
   regression tests called out in `requirements.md`.
4. Delete the old code once the new path passes everything.

This avoids a high-risk single commit and lets us bisect if any
regression shows up in real use. Until the deletion step lands, an
incremental reducer branch can be a useful stabilisation patch, but it
is not the full architecture described here if it still keeps
optimistic temp ids, `reparentTempParts`, `partReducer.ts`, or the
`convertMessages` data-layer caches.

## Open questions for review

1. **Do we trust field ownership over length heuristics?** This spec
   says yes: once a delta arrives for a streaming field, deltas own
   that field. Snapshots own everything else. We should not re-add
   length comparisons unless production telemetry proves this rule is
   insufficient.
2. **Is `useDeferredValue` enough if streaming gets janky?** I'd
   like to commit to "no buffering until measured" but want to
   confirm there's not a known case where the current rAF buffer
   was the difference between "smooth" and "drops keystrokes".
