import { useCallback, useRef, useState } from 'react';
import type { Dispatch, MutableRefObject, SetStateAction } from 'react';
import type { Message, Part, SessionDetail } from '../../lib/api';
import { useApiStore } from '../../lib/apiStore';
import { hashMessagesAndParts, hashSession } from '../../lib/sessionHash';
import { mergeParts } from '../../lib/sseMessageHelpers';
import { trackRender } from '../../lib/renderRateMonitor';

/** How many messages we request per `getSession` call. */
const PAGE_SIZE = 30;

/** Resolved session metadata used by `load()` to feed the page's
 *  `setSession` callback. Mirrors the shape SessionDetail kept on
 *  the `session` state value. */
export type SessionWithDefaults = SessionDetail['session'] & {
  defaultAgent?: string;
  defaultModel?: string;
};

export interface UseSessionMessagesOptions {
  /** Active session id from the URL. */
  id: string | undefined;
  /** Initial cached payload, used to seed state before the first
   *  `getSession` resolves. `null` for a cold first visit. */
  initialCached: SessionDetail | null;
  /** Page-level setter for `session`. The hook calls it from `load`
   *  whenever the session metadata changed since the last load. */
  setSession: Dispatch<SetStateAction<SessionWithDefaults | null>>;
  /** Hash ref tracking the last applied session metadata. Held as a
   *  ref so the page's effects can reset it when changing sessions. */
  lastSessionHashRef: MutableRefObject<string>;
  /** AbortController ref shared with the rest of the page; the
   *  loadMore() call reads its signal at invocation time. */
  abortSignalRef: MutableRefObject<AbortController | null>;
  /** Counter of messages we've trimmed from the head of the array
   *  (kept by SessionDetail for memory bounding). loadMore reads
   *  this so its offset accounts for trimmed messages. */
  droppedMessageCountRef: MutableRefObject<number>;
  /** Current route session id. Async callbacks must still belong to
   *  this id before mutating page state. */
  activeSessionIdRef: MutableRefObject<string | undefined>;
}

export interface UseSessionMessagesResult {
  messages: Message[];
  setMessages: Dispatch<SetStateAction<Message[]>>;
  parts: Part[];
  setParts: Dispatch<SetStateAction<Part[]>>;
  totalMessages: number;
  setTotalMessages: Dispatch<SetStateAction<number>>;
  loading: boolean;
  setLoading: Dispatch<SetStateAction<boolean>>;
  loadingMore: boolean;
  loadError: string | null;
  setLoadError: Dispatch<SetStateAction<string | null>>;
  /** True for one paint frame after a session change, so the page
   *  can blank the viewport during the fade-in animation. */
  switching: boolean;
  setSwitching: Dispatch<SetStateAction<boolean>>;
  /** Tick that the SSE handler bumps when an edit/write part lands.
   *  Wired to useSessionChanges / useSessionInfo via dirtyTick. */
  changesDirtyTick: number;
  setChangesDirtyTick: Dispatch<SetStateAction<number>>;
  /** Hash of the last applied messages+parts page; reset on session
   *  switch so the next load() definitely re-applies. */
  lastHashRef: MutableRefObject<string>;
  /** Reload the latest page from the server. Merges with already-
   *  loaded older messages, drops optimistic placeholders, seeds the
   *  detail cache, and clears the load-error banner. */
  load: (signal?: AbortSignal) => Promise<void>;
  /** Prepend an older page; idempotent across overlapping ids. */
  loadMore: () => Promise<void>;
}

/**
 * Owns the page's message / part / pagination / loading / error
 * state. The actual `session` value (the metadata header) stays in
 * the page so the SSE handler and the title/header info effects can
 * keep using it; the hook reads `setSession` through props.
 */
export function useSessionMessages({
  id,
  initialCached,
  setSession,
  lastSessionHashRef,
  abortSignalRef,
  droppedMessageCountRef,
  activeSessionIdRef,
}: UseSessionMessagesOptions): UseSessionMessagesResult {
  trackRender('useSessionMessages', { id });
  const getSession = useApiStore((s) => s.getSession);
  const setCachedSession = useApiStore((s) => s.setCachedSession);

  const [messages, setMessages] = useState<Message[]>(initialCached?.messages ?? []);
  const [parts, setParts] = useState<Part[]>(initialCached?.parts ?? []);
  const [totalMessages, setTotalMessages] = useState(
    initialCached?.totalMessages || initialCached?.session.messageCount || 0,
  );
  const [loading, setLoading] = useState(!initialCached);
  const [loadingMore, setLoadingMore] = useState(false);
  const [loadError, setLoadError] = useState<string | null>(null);
  const [switching, setSwitching] = useState(false);
  const [changesDirtyTick, setChangesDirtyTick] = useState(0);

  const lastHashRef = useRef('');

  // Load the latest page (newest messages). Merges with already-
  // loaded older messages and drops optimistic placeholders once
  // real data arrives.
  const load = useCallback(async (signal?: AbortSignal) => {
    if (!id) return;
    try {
      const result = await getSession(id, PAGE_SIZE, 0, signal);
      if (signal?.aborted || activeSessionIdRef.current !== id) return;

      // Only push session metadata if it actually changed.
      const sessionData: SessionWithDefaults = {
        ...result.session,
        contextTokenCount: result.session.contextTokenCount ?? result.contextTokenCount,
        defaultAgent: result.defaultAgent,
        defaultModel: result.defaultModel,
      };
      const sessionHash = hashSession(sessionData);
      if (sessionHash !== lastSessionHashRef.current) {
        lastSessionHashRef.current = sessionHash;
        setSession(sessionData);
      }
      const nextTotalMessages = result.totalMessages || result.session.messageCount || 0;
      setTotalMessages(nextTotalMessages);

      // Only update messages if the latest page actually changed.
      const newMsgs = result.messages || [];
      const newParts = result.parts || [];
      const hash = hashMessagesAndParts(newMsgs, newParts);
      if (hash !== lastHashRef.current) {
        lastHashRef.current = hash;
        // Merge: keep older loaded messages, replace the newest
        // page. Also drop optimistic (temp-*) and error (error-*)
        // messages once real data arrives.
        setMessages((prev) => {
          const newIds = new Set(newMsgs.map((m) => m.id));
          const older = prev.filter((m) =>
            !newIds.has(m.id) && !m.id.startsWith('temp-') && !m.id.startsWith('error-'),
          );
          return [...older, ...newMsgs];
        });
        setParts((prev) => mergeParts(prev, newParts));
      }
      // Seed the detail cache so revisits render instantly. The SSE
      // mirror effect keeps it in sync with live updates after this
      // point. See spec/session-switch-cache.
      setCachedSession(id, {
        session: sessionData,
        messages: newMsgs,
        parts: newParts,
        totalMessages: nextTotalMessages,
        contextTokenCount: result.contextTokenCount,
        defaultAgent: result.defaultAgent,
        defaultModel: result.defaultModel,
      });
      setLoadError(null);
    } catch (e) {
      if (e instanceof DOMException && e.name === 'AbortError') return;
      if (activeSessionIdRef.current !== id) return;
      console.error('Failed to load session', e);
      setLoadError(e instanceof Error ? e.message : 'Failed to load session');
    }
    if (activeSessionIdRef.current === id) setLoading(false);
  }, [activeSessionIdRef, getSession, id, setCachedSession, setSession, lastSessionHashRef]);

  // Load older messages (prepend). Offset accounts for any messages
  // already trimmed from the head via the memory bound.
  const loadMore = useCallback(async () => {
    if (!id || loadingMore) return;
    const signal = abortSignalRef.current?.signal;
    setLoadingMore(true);
    try {
      const result = await getSession(
        id,
        PAGE_SIZE,
        messages.length + droppedMessageCountRef.current,
        signal,
      );
      if (signal?.aborted || activeSessionIdRef.current !== id) return;
      const newMsgs = result.messages || [];
      const newParts = result.parts || [];
      if (newMsgs.length) {
        setMessages((prev) => {
          const existingIds = new Set(prev.map((m) => m.id));
          const unique = newMsgs.filter((m) => !existingIds.has(m.id));
          return [...unique, ...prev];
        });
        setParts((prev) => {
          const existingIds = new Set(prev.map((p) => p.id));
          const unique = newParts.filter((p) => !existingIds.has(p.id));
          return [...unique, ...prev];
        });
      }
    } catch (e) {
      if (e instanceof DOMException && e.name === 'AbortError') return;
      throw e;
    } finally {
      if (activeSessionIdRef.current === id) setLoadingMore(false);
    }
  }, [activeSessionIdRef, getSession, id, messages.length, loadingMore, abortSignalRef, droppedMessageCountRef]);

  return {
    messages,
    setMessages,
    parts,
    setParts,
    totalMessages,
    setTotalMessages,
    loading,
    setLoading,
    loadingMore,
    loadError,
    setLoadError,
    switching,
    setSwitching,
    changesDirtyTick,
    setChangesDirtyTick,
    lastHashRef,
    load,
    loadMore,
  };
}
