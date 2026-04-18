# Requirements: Faster session switching via client-side cache

## Problem

Switching between sessions in the dashboard feels sluggish. Every time the user
navigates to a session detail view, the page empties and shows a loading state
until the server returns the session JSON. For sessions with many messages,
this can take seconds — even when the user is just flipping back to a session
they looked at moments ago.

Empirically, a single session switch issues 6–8 HTTP round-trips, two of which
transfer the entire message history of the session. The UI also unconditionally
wipes its state (`setMessages([])`, `setParts([])`, …) before the new response
arrives, so even "instant" switches show a blank screen.

## Goals

1. Switching to a session the user has already viewed in the current tab feels
   instant: the previously-rendered content appears immediately, and any newer
   data is merged in without a visible flash.
2. First-time visits are no slower than today.
3. Live updates via SSE remain correct: a cached render never stays stale for
   longer than a brief background revalidation.
4. Memory usage stays bounded: caching must not grow without limit, even for
   users who browse many sessions in a session.

## Non-goals

- Reducing the actual server response time (covered by a separate spec if
  needed).
- Adding server-side caching, ETags, or HTTP cache headers.
- Changing pagination semantics or the message trimming logic
  (`MAX_RETAINED_MESSAGES` etc.).
- Prefetching on hover — may come later but is out of scope here.

## User-visible behaviour

### Instant return to a recently-viewed session

**Given** I am viewing session A, then navigate to session B, then navigate
back to session A, **when** the URL changes to session A's page, **then** the
session header, messages, and parts for session A appear immediately (same
frame, no spinner). A background refetch is issued silently; if the data
changed in the meantime the UI updates without clearing first.

### First visit to a session is unchanged

**Given** I navigate to a session I have not viewed this tab session, **when**
the URL changes, **then** the loading state shows exactly as today until the
server responds.

### SSE live updates still work

**Given** I am viewing a cached session, **when** SSE events arrive for that
session, **then** the UI applies them as it does today — the cache is updated
in place so subsequent switches see the freshest data.

### Session switching during active streaming

**Given** session A is actively streaming (SSE), and I switch to session B then
back to A, **when** I return, **then** the already-streamed content is visible
immediately and streaming resumes on the new SSE connection without
duplication or loss.

### Cache eviction

**Given** I have viewed more than N distinct sessions in this tab,
**when** I visit a session not in the cache, **then** the oldest cached entry
is evicted (LRU). N should be small enough to keep memory usage modest (target:
5 sessions).

### Removed redundant network calls

**Given** SSE connects for a session, **then** the 500 ms "reconciliation"
`load()` that fires after `onopen` should be skipped if the initial load
already populated data and no gap was detected. This removes one full session
fetch per switch in the common case.

## Acceptance criteria

- [ ] Navigating A → B → A shows session A's content without clearing the page
      or showing a spinner, provided A was viewed within the current tab and
      has not been evicted.
- [ ] The background refetch on a cached switch updates the UI only when the
      content actually changed (use the existing hash-based diffing).
- [ ] First visits to a session behave identically to today (loading state
      appears, spinner clears once data arrives).
- [ ] When switching back to a cached streaming session, no duplicate messages
      or parts appear after SSE reconnects.
- [ ] Cache holds at most 5 entries; oldest is evicted first.
- [ ] The `setTimeout(load, 500)` reconciliation in the SSE `onopen` handler
      only runs when no content event has been received AND the initial
      `load()` did not succeed — not unconditionally.
- [ ] No regression in existing tests; new tests cover the cache hit/miss and
      revalidation behaviour.

## Out-of-scope / future work

- Server-side ETags and 304 responses.
- SQL-level pagination in the DB fallback path.
- Prefetching sibling sessions on hover.
- Deduplicating in-flight requests in `runRequest` (would complement this spec
  but is not strictly required).
