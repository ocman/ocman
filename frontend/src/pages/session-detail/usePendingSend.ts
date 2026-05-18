// Optimistic user-send state, kept outside the SessionView.
//
// Architecture §"The composer / optimistic send":
//
//   The composer page owns a `pending` slot. The render path
//   concatenates `pending` after the last real message when there
//   is one, so the bubble is visible immediately. The hook clears
//   `pending` when a `message.created` user message lands that
//   wasn't in our previous `messages[]`. No synthetic IDs. No
//   reparenting.
//
// Why this is simpler than the legacy `temp-*` id system:
//
//   - The pending bubble is never inside `messages[]`, so there's
//     nothing to filter / reparent / dedupe on the server-message
//     hot path.
//   - Reconciliation is one comparison: "did a new user message
//     appear in `messages` since the last render?" If yes, clear.
//   - Refresh recovery is unaffected — pending lives in component
//     state, so a page reload starts fresh; the persisted
//     `failedSends` list takes over via the same UI affordance.
//
// The hook does not call sendMessage itself; it just owns the slot.
// The page wires it into useSessionActions / the Composer.

import { useCallback, useEffect, useRef, useState } from 'react';
import type { Message, Part } from '../../lib/api';

export interface PendingSendImage {
  url: string;
  mime: string;
}

export interface PendingSend {
  /**
   * Stable id for the optimistic bubble. Used as the Message id and
   * Part messageId on `materializePending`. Generated once on
   * `begin()`. NOT a `temp-*`-prefixed id — the new pipeline doesn't
   * special-case the prefix anywhere.
   */
  id: string;
  text: string;
  images?: PendingSendImage[];
  /** Composer selections at the time of send. Replayed on retry. */
  model?: string;
  agent?: string;
  reasoning?: string;
  /** Wall-clock ms; surfaced to the renderer for ordering / display. */
  startedAt: number;
  /** Populated by `fail()` when the network call rejected. */
  error?: string;
}

export interface UsePendingSendResult {
  pending: PendingSend | null;
  /** Begin a new send. Generates an id and seeds `startedAt`. */
  begin: (
    text: string,
    images?: PendingSendImage[],
    opts?: { model?: string; agent?: string; reasoning?: string },
  ) => string;
  /** Set the failure flag on the pending entry. The bubble stays
   *  visible so the user can retry / dismiss it. */
  fail: (message: string) => void;
  /** Drop the pending entry. Called explicitly on retry / dismiss /
   *  manual clear. */
  clear: () => void;
  /** Hook the page's current messages list. When a new user message
   *  appears that wasn't there last frame, pending is cleared.
   *  Pure read — call from render. */
  observeMessages: (messages: Message[]) => void;
}

/** Crypto-strong-ish unique id. Avoids the `temp-` prefix legacy
 *  helpers used to recognise. We don't strictly need randomness —
 *  the id is only used to key the optimistic bubble until the
 *  server's id replaces it via `observeMessages` — but a 16-char
 *  alphabet keeps debug-log lines readable. */
function generateId(): string {
  if (typeof crypto !== 'undefined' && typeof crypto.randomUUID === 'function') {
    return `pending-${crypto.randomUUID()}`;
  }
  return `pending-${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 8)}`;
}

export function usePendingSend(sessionId: string | undefined): UsePendingSendResult {
  const [pending, setPending] = useState<PendingSend | null>(null);
  // Snapshot of the user-message ids that existed at the moment
  // `begin()` was called. The next `observeMessages` call clears
  // pending when it sees a user-message id that isn't in this set
  // — that's the server's `message.created` for our optimistic
  // send. Captured at begin time (not maintained across every
  // render) so the comparison is robust against re-renders /
  // strict-mode double-invoke / random observe ordering.
  const baselineUserIdsRef = useRef<Set<string> | null>(null);
  // Latest `messages` reference observed during render. The
  // session-change effect uses this to recompute the baseline if
  // needed; tests use it to verify lifecycle.
  const lastMessagesRef = useRef<Message[]>([]);

  // Session change resets state. Drop any in-flight pending from
  // the previous session — matching the legacy behaviour where
  // navigation aborted optimistic work. The setState-in-effect is
  // intentional: this is the canonical way to reset a hook's state
  // on a key change without forcing the component to use `key=` to
  // remount.
  useEffect(() => {
    // eslint-disable-next-line react-hooks/set-state-in-effect
    setPending(null);
    baselineUserIdsRef.current = null;
  }, [sessionId]);

  const begin = useCallback<UsePendingSendResult['begin']>((text, images, opts) => {
    // Snapshot the current user-message ids so the next time a
    // server-delivered message lands with a new id, observeMessages
    // can confidently clear pending. Reading `lastMessagesRef`
    // captures whatever the latest observed messages are — robust
    // against tests that don't pre-render with the messages array.
    const baseline = new Set<string>();
    for (const m of lastMessagesRef.current) {
      if (m.data.role === 'user') baseline.add(m.id);
    }
    baselineUserIdsRef.current = baseline;
    const id = generateId();
    setPending({
      id,
      text,
      images,
      model: opts?.model,
      agent: opts?.agent,
      reasoning: opts?.reasoning,
      startedAt: Date.now(),
    });
    return id;
  }, []);

  const fail = useCallback((message: string) => {
    setPending((prev) => (prev ? { ...prev, error: message } : prev));
  }, []);

  const clear = useCallback(() => {
    setPending(null);
    baselineUserIdsRef.current = null;
  }, []);

  const observeMessages = useCallback((messages: Message[]) => {
    lastMessagesRef.current = messages;
    const baseline = baselineUserIdsRef.current;
    if (!baseline) return;
    // Did a user message arrive that wasn't there when begin() was
    // called? If yes, the server has acked our send and the
    // optimistic bubble can step aside.
    for (const m of messages) {
      if (m.data.role !== 'user') continue;
      if (!baseline.has(m.id)) {
        setPending((prev) => (prev ? null : prev));
        baselineUserIdsRef.current = null;
        return;
      }
    }
  }, []);

  return { pending, begin, fail, clear, observeMessages };
}

/**
 * Materialise the pending slot into the messages/parts arrays the
 * converter consumes. When `pending` is null, returns the inputs
 * unchanged (reference-equal) so React downstream caches don't
 * invalidate.
 *
 * Semantics:
 *   - One user Message is appended at the end of `messages` with
 *     `data.role: 'user'` and `timeCreated: pending.startedAt`.
 *   - One text Part is appended with `pending.id`'s message
 *     reference.
 *   - Each image becomes one additional file Part.
 *
 * The renderer is responsible for surfacing the `pending.error`
 * affordance (a small "failed — retry" banner on the bubble); it
 * reads from `pending` directly, not from the materialised message.
 */
export function materializePending(
  sessionId: string,
  pending: PendingSend | null,
  messages: Message[],
  parts: Part[],
): { messages: Message[]; parts: Part[] } {
  if (!pending) return { messages, parts };

  const userMsg: Message = {
    id: pending.id,
    sessionId,
    timeCreated: pending.startedAt,
    data: { role: 'user' },
  };
  const extraParts: Part[] = [];
  if (pending.text) {
    extraParts.push({
      id: `${pending.id}-text`,
      messageId: pending.id,
      sessionId,
      data: { type: 'text', text: pending.text } as unknown as string,
    });
  }
  if (pending.images) {
    pending.images.forEach((img, i) => {
      extraParts.push({
        id: `${pending.id}-img-${i}`,
        messageId: pending.id,
        sessionId,
        data: { type: 'file', mime: img.mime, url: img.url } as unknown as string,
      });
    });
  }

  return {
    messages: [...messages, userMsg],
    parts: [...parts, ...extraParts],
  };
}
