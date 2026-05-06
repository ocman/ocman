// Buffer + rAF-flush for `message.part.delta` SSE events.
//
// PROBLEM
//
// OpenCode emits `message.part.delta` events at ~30 Hz while the LLM
// streams. The original handler called `setParts((prev) => ...)`
// synchronously on every event, which:
//
//   1. forces React to schedule a render every ~33 ms;
//   2. each render walks/clones the entire parts array (O(N)
//      where N = number of parts in the conversation);
//   3. saturates the scheduler so user-initiated work (clicks,
//      navigation) gets queued behind the streaming backlog.
//
// FIX
//
// Buffer deltas in a Map keyed by `(partId, field)` and flush them
// from a single `requestAnimationFrame` callback. This caps render
// frequency at the browser's paint rate (~60 Hz max, usually less
// when the tab is busy) regardless of how fast the wire pushes,
// while still rendering each delta on the very next frame so the
// streaming UI feels live.
//
// Each delta keeps the original message id and session id so we can
// still create new parts on the fly when a delta arrives before its
// `message.part.updated` start event.
//
// SHAPE
//
// `createSseDeltaBuffer(setParts)` returns:
//   - `enqueue(...)` to record a delta and (re)arm the rAF flush;
//   - `flush()` to apply all buffered deltas synchronously (used by
//     tests and on teardown so no work is lost mid-frame);
//   - `cancel()` to cancel any pending rAF and discard the buffer
//     (used when the SSE connection tears down).
//
// The implementation is intentionally setState-agnostic: it accepts
// the `setParts` dispatcher as a parameter so the same module can
// be reused if other call sites need similar coalescing later.

import type { Dispatch, SetStateAction } from 'react';
import type { Part } from '../../lib/api';

/**
 * One pending delta entry. We keep the most recent (messageId,
 * sessionId) because they don't change across deltas for the same
 * partId, but the first delta we see may have stale routing
 * metadata if the message wasn't created locally yet — so the last
 * write wins, which mirrors the behaviour of the original
 * synchronous handler.
 */
interface PendingDelta {
  partId: string;
  messageId: string;
  sessionId: string;
  /** Map of field path → accumulated string. Field paths use dot
   *  notation: `text` is a top-level field, `text.value` writes to
   *  `existing.text.value`. Top-level fields and nested fields are
   *  applied independently. */
  fieldDeltas: Map<string, string>;
}

export interface SseDeltaBuffer {
  /**
   * Record a delta for `partId` on `field`, accumulating with any
   * previous delta in the same frame. Schedules an rAF flush if one
   * isn't already pending.
   */
  enqueue(args: {
    partId: string;
    messageId: string;
    sessionId: string;
    field: string;
    delta: string;
  }): void;
  /**
   * Apply all buffered deltas immediately. Idempotent: a no-op when
   * the buffer is empty. Cancels any pending rAF since the work it
   * would have done is now committed.
   */
  flush(): void;
  /**
   * Cancel any pending rAF and drop the buffer. Used on SSE
   * teardown so a queued frame doesn't fire after the
   * EventSource has been closed.
   */
  cancel(): void;
}

/**
 * `requestAnimationFrame` is the right scheduler in browsers, but
 * this module is called from tests under jsdom (which omits it).
 * Fall back to `setTimeout(..., 16)` when rAF isn't available so
 * the buffer remains exercised end-to-end in tests.
 */
function getRaf(): (cb: FrameRequestCallback) => number {
  if (typeof requestAnimationFrame === 'function') {
    return requestAnimationFrame.bind(globalThis);
  }
  return (cb) => (setTimeout(() => cb(performance.now()), 16) as unknown as number);
}

function getCancelRaf(): (handle: number) => void {
  if (typeof cancelAnimationFrame === 'function') {
    return cancelAnimationFrame.bind(globalThis);
  }
  return (handle) => clearTimeout(handle as unknown as ReturnType<typeof setTimeout>);
}

/**
 * Apply one delta entry against a parts array, returning a new
 * array. Mirrors the original synchronous handler's logic exactly:
 * dotted fields write to nested objects; flat fields write at the
 * top level; missing parts are appended with an initial `{ type:
 * 'text', [field]: delta }` payload.
 */
export function applyDelta(prev: Part[], pending: PendingDelta): Part[] {
  const idx = prev.findIndex((p) => p.id === pending.partId);
  if (idx >= 0) {
    const existing = prev[idx];
    let existingData: Record<string, unknown>;
    try {
      existingData = typeof existing.data === 'string'
        ? JSON.parse(existing.data) as Record<string, unknown>
        : existing.data as unknown as Record<string, unknown>;
    } catch {
      existingData = {};
    }
    let updatedData: Record<string, unknown> = existingData;
    for (const [field, deltaText] of pending.fieldDeltas) {
      const dotIdx = field.indexOf('.');
      if (dotIdx > 0) {
        const parent = field.slice(0, dotIdx);
        const child = field.slice(dotIdx + 1);
        const parentObj = (updatedData[parent] as Record<string, unknown> | undefined) || {};
        const currentVal = (parentObj[child] as string) || '';
        updatedData = {
          ...updatedData,
          [parent]: { ...parentObj, [child]: currentVal + deltaText },
        };
      } else {
        const currentVal = (updatedData[field] as string) || '';
        updatedData = { ...updatedData, [field]: currentVal + deltaText };
      }
    }
    const updated = [...prev];
    updated[idx] = {
      ...existing,
      data: updatedData as unknown as string,
    };
    return updated;
  }
  // Part doesn't exist yet — synthesise it with all buffered deltas
  // applied. Mirrors the original "create on first delta" behaviour
  // for the case where `message.part.updated` (the start event)
  // arrives after the first delta.
  let initialData: Record<string, unknown> = { type: 'text' };
  for (const [field, deltaText] of pending.fieldDeltas) {
    const dotIdx = field.indexOf('.');
    if (dotIdx > 0) {
      const parent = field.slice(0, dotIdx);
      const child = field.slice(dotIdx + 1);
      const parentObj = (initialData[parent] as Record<string, unknown> | undefined) || {};
      initialData = {
        ...initialData,
        [parent]: { ...parentObj, [child]: deltaText },
      };
    } else {
      initialData = { ...initialData, [field]: deltaText };
    }
  }
  const newPart: Part = {
    id: pending.partId,
    messageId: pending.messageId,
    sessionId: pending.sessionId,
    data: initialData as unknown as string,
  };
  return [...prev, newPart];
}

export function createSseDeltaBuffer(
  setParts: Dispatch<SetStateAction<Part[]>>,
): SseDeltaBuffer {
  const pendingByPart = new Map<string, PendingDelta>();
  let rafHandle: number | null = null;
  const raf = getRaf();
  const cancelRaf = getCancelRaf();

  function commit() {
    rafHandle = null;
    if (pendingByPart.size === 0) return;
    // Snapshot + clear up-front so any deltas that arrive during the
    // updater (we're about to call setParts) get scheduled into the
    // next frame instead of clobbering the in-flight commit.
    const batch = Array.from(pendingByPart.values());
    pendingByPart.clear();
    setParts((prev) => {
      let next = prev;
      for (const pending of batch) next = applyDelta(next, pending);
      return next;
    });
  }

  return {
    enqueue({ partId, messageId, sessionId, field, delta }) {
      let entry = pendingByPart.get(partId);
      if (!entry) {
        entry = {
          partId,
          messageId,
          sessionId,
          fieldDeltas: new Map(),
        };
        pendingByPart.set(partId, entry);
      } else {
        // Latest routing metadata wins (cheap; protects against
        // stale messageIds from a part that got moved between
        // messages — should never happen in practice but matches
        // the original "last write" semantics).
        entry.messageId = messageId;
        entry.sessionId = sessionId;
      }
      const accumulated = entry.fieldDeltas.get(field) ?? '';
      entry.fieldDeltas.set(field, accumulated + delta);
      if (rafHandle === null) {
        rafHandle = raf(commit);
      }
    },
    flush() {
      if (rafHandle !== null) {
        cancelRaf(rafHandle);
        rafHandle = null;
      }
      commit();
    },
    cancel() {
      if (rafHandle !== null) {
        cancelRaf(rafHandle);
        rafHandle = null;
      }
      pendingByPart.clear();
    },
  };
}
