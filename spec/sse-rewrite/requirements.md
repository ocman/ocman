# Requirements: Simpler live session pipeline

## Problem

The current SSE handling for session detail (`useSessionSSE`,
`useSessionMessages`, `sseMessageHelpers`, `partReducer`,
`sseDeltaBuffer`, `convertMessages` caches, optimistic-id reparenting,
ghost injection) is large enough that bugs are easier to add than
fix. In one session we've seen:

- Tool / question blocks don't render until refresh.
- User messages render, then vanish when SSE arrives, until refresh.
- Streaming text occasionally has mid-text gaps or "rewinds".
- Live updates can appear frozen entirely; refresh fixes it.

Each fix has been a local patch (length-non-decreasing merge,
reparenting helpers, length-based heuristics). The fixes interact
and the cycle continues. The complexity isn't load-bearing — it's
emergent from a few design choices that don't have to hold:

1. Snapshots and deltas are reconciled in the client even though the
   wire is ordered.
2. The page maintains an in-memory cache mirror, a per-Message result
   cache, a per-Part data cache, and a rAF commit buffer — four
   independent invalidation surfaces.
3. The REST `load()` path and the SSE path use different helpers and
   merge rules.
4. Optimistic user messages get a synthetic ID that has to be
   reconciled with the server's real ID when SSE confirms.

We want a pipeline a single person can hold in their head, with one
write path and trivial invalidation.

## Goals

1. **One source of truth.** `messages[]` and `parts[]` are derived
   from a single reducer that consumes (SSE event | REST payload)
   and produces the next state.
2. **Deterministic field ownership.** Snapshots replace fields that
   have not received streaming deltas; deltas append to streaming
   fields and own those fields once observed. This avoids the old
   length-based heuristic while still guaranteeing that visible text
   never rewinds.
3. **Disconnect = reload.** Lost the EventSource? Re-fetch
   `/api/session/{id}`. The reducer accepts a full snapshot and
   replaces state. No partial-merge dance.
4. **Optimistic sends without synthetic IDs.** The composer keeps
   a local "pending" envelope outside `messages[]`. On
   `message.created` for that user message, drop the envelope.
5. **No caching in the data layer.** React's render memoization is
   the only cache. If profiling shows we need more, add it back
   surgically with measurements.

## Non-goals

- Changing the OpenCode wire format. We accept it as-is.
- Changing pagination or message trimming semantics.
- Server-side caching, ETags, or any backend changes.
- Reworking session switching / the cross-session `apiStore` cache
  (that lives in `useApiStore`; this rewrite is the SessionDetail
  page's local state only).
- Multi-platform changes. Claude Code has its own adapter; this
  rewrite only touches the OpenCode SSE path. Claude Code's hook
  receiver and JSONL parser are out of scope.

## User-visible behaviour

### Tool blocks appear live

**Given** I am viewing a session that is actively producing tool
calls, **when** the assistant emits a tool part via SSE, **then**
the tool bubble appears in the conversation thread within one
animation frame, without any manual refresh.

Same applies to `question` prompts, `bash` invocations, `edit`/
`write` diffs, and any other tool type.

### Streaming text never rewinds

**Given** the assistant is streaming a response, **when** new
deltas arrive, **then** the text in the bubble only grows — never
shortens, never blanks, never replaces with a different prefix.

### User messages appear immediately and stay

**Given** I type a message and press send, **when** the request is
in flight, **then** my message bubble is visible immediately
("pending" affordance optional). **And** when SSE delivers the
real `message.created`, the bubble does not disappear or flicker.

### Disconnect-reconnect recovers cleanly

**Given** my SSE connection drops mid-stream, **when** the client
reconnects, **then** the session view reflects the authoritative
server state within one round-trip. Any tokens streamed between
the disconnect and the reconnect appear in their final form (we
won't reconstruct partial deltas across the gap; we just refetch).

### Question answer round-trips work live

**Given** the agent has posted a `question` prompt and I answer it,
**when** the agent's next message arrives via SSE, **then** the
answered question and the agent's follow-up both render without a
manual refresh.

## Out-of-scope edge cases (must not regress, not the focus)

- Long sessions (1000+ messages) — current `MAX_RETAINED_MESSAGES`
  trim behaviour preserved.
- Sub-agent / task sessions — the `__task__` rendering and embedded
  thread preview keep working.
- Failed-send replay — the `failedSends` storage and retry UI keeps
  working. We just route it through a different state-shape inside
  the page; the user-facing affordance doesn't change.

## Success criteria

1. A new integration test (DOM-asserting) exists for each of the
   five user-visible behaviours above. They all pass on `make test`.
2. Lines-of-code in the SSE / page-state stack drops by at least
   30% (counting `useSessionSSE.ts`, `useSessionMessages.ts`,
   `sseMessageHelpers.ts`, `partReducer.ts`, `sseDeltaBuffer.ts`,
   the relevant slice of `useSessionActions.ts`, plus the part of
   `convertMessages.ts` that handles cache invalidation).
3. No new state-mutation code path is added without an integration
   test that covers it (enforced by review, not tooling).
4. The four reported bugs (tool blocks missing, mid-text gaps,
   user messages vanishing, live updates frozen) are demonstrably
   fixed: each has a regression test that fails on `main` and
   passes on this branch.
5. Before deleting legacy catch-all SSE handling, the branch includes
   an event-shape audit covering text, tool, edit/write, permission,
   question, subagent, and reconnect flows. Unknown events may only
   be ignored after that audit confirms they do not carry live
   message or part content.
