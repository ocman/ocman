import { useCallback, useEffect, useLayoutEffect, useRef, useState } from 'react';
import { api, type QueuedMessage } from './api';
import { onQueueUpdated, onSseConnect } from './useGlobalEvents';
import { remoteLog } from './remoteLog';

/**
 * Owns the follow-up message queue for a session (#58). The queue lives
 * server-side; this hook is a thin renderer of it, following the same
 * pattern the conversation SSE uses:
 *
 *   - Load the full list from the endpoint on mount AND on every SSE
 *     (re)connect. The /api/events stream can drop while disconnected, so
 *     reconnecting means "I may have missed events" — reconcile by
 *     refetching (exactly how useSession refetches the conversation on
 *     reconnect).
 *   - Between loads, apply every ocman.queue.updated broadcast (a full
 *     authoritative snapshot, 0..N items) directly.
 *
 * A load carries a monotonic token; a live broadcast that lands while a
 * load is in flight wins (bumps the token), so the slower load result is
 * dropped and can't resurrect a drained item.
 */
export function useMessageQueue(sessionId?: string, platform?: string) {
  const [queue, setQueue] = useState<QueuedMessage[]>([]);
  // Bumped when a load starts and when an update is applied. A load discards
  // its result if a newer request or broadcast landed since.
  const seqRef = useRef(0);

  const applyLatest = useCallback((messages: QueuedMessage[]) => {
    seqRef.current += 1;
    // Defensive: only ever store an array. A misrouted/erroring endpoint
    // (or a proxy returning HTML/an object) must never make the list a
    // non-array and crash QueuedMessages' .map at render.
    setQueue(Array.isArray(messages) ? messages : []);
  }, []);

  const load = useCallback(() => {
    if (!sessionId) return;
    const issuedAt = ++seqRef.current;
    api.queuedMessages(sessionId, platform)
      .then((messages) => {
        if (seqRef.current !== issuedAt) return; // a newer update won
        applyLatest(messages);
      })
      .catch((e) => remoteLog.error('Failed to load message queue', e));
  }, [sessionId, platform, applyLatest]);

  // Track the session the current list belongs to, so we only reset the
  // list on an actual session change — not on every effect re-run (e.g.
  // when `platform` resolves from undefined → "opencode" after the session
  // loads). Clearing unconditionally would wipe a just-applied enqueue
  // broadcast, making a new message vanish until a manual refresh.
  const loadedSessionRef = useRef<string | undefined>(undefined);
  const currentSessionRef = useRef(sessionId);
  const currentPlatformRef = useRef(platform);
  useLayoutEffect(() => {
    currentSessionRef.current = sessionId;
    currentPlatformRef.current = platform;
  }, [sessionId, platform]);

  useEffect(() => {
    let cancelled = false;

    if (!sessionId) {
      loadedSessionRef.current = undefined;
      Promise.resolve().then(() => { if (!cancelled) applyLatest([]); });
      return () => { cancelled = true; };
    }

    // Reset the list only when moving to a different session.
    if (loadedSessionRef.current !== sessionId) {
      loadedSessionRef.current = sessionId;
      Promise.resolve().then(() => { if (!cancelled) applyLatest([]); load(); });
    } else {
      // Same session, effect re-ran (e.g. platform resolved): reconcile
      // without wiping the current list.
      load();
    }

    const unsubQueue = onQueueUpdated((sid, messages) => {
      if (sid !== sessionId || !messages) return;
      applyLatest(messages);
    });
    // Reconcile on every SSE (re)connect — reload the list from the
    // endpoint, mirroring the conversation's refetch-on-reconnect. The seq
    // guard keeps this reload from clobbering a fresher broadcast.
    const unsubConnect = onSseConnect(load);

    return () => { cancelled = true; unsubQueue(); unsubConnect(); };
  }, [sessionId, platform, applyLatest, load]);

  const refresh = useCallback((expectedSessionId?: string, expectedPlatform?: string) => {
    if (
      !sessionId
      || (expectedSessionId && expectedSessionId !== currentSessionRef.current)
      || (expectedPlatform && expectedPlatform !== currentPlatformRef.current)
    ) return;
    load();
  }, [sessionId, load]);

  const remove = useCallback((id: string) => {
    if (!sessionId) return;
    // Optimistic: drop locally (bump seq so an in-flight load can't
    // resurrect it); the broadcast reconciles to server truth.
    seqRef.current += 1;
    setQueue((prev) => prev.filter((m) => m.id !== id));
    api.deleteQueuedMessage(sessionId, id, platform)
      .catch((e) => { remoteLog.error('Failed to remove queued message', e); refresh(); });
  }, [sessionId, platform, refresh]);

  const move = useCallback((id: string, direction: -1 | 1) => {
    if (!sessionId) return;
    api.moveQueuedMessage(sessionId, id, direction, platform)
      .catch((e) => { remoteLog.error('Failed to move queued message', e); refresh(); });
    // The broadcast supplies the authoritative new order.
  }, [sessionId, platform, refresh]);

  return { queue, refresh, remove, move };
}
