import { useEffect, useRef } from 'react';
import { useApiStore } from '../../lib/apiStore';
import type { SessionView } from '../../lib/sessionReducer';
import { latestPage } from './sessionView';

/** Debounced mirror, flushed before navigation so revisits render instantly. */
export function useSessionCache(sessionId: string | undefined, view: SessionView, totalMessages: number, pageSize: number) {
  const updateCachedSession = useApiStore((s) => s.updateCachedSession);
  const mirrorWriteRef = useRef<(() => void) | null>(null);
  const mirrorTimerRef = useRef<number | null>(null);
  useEffect(() => {
    // The reducer may still contain the outgoing session during navigation.
    if (!sessionId || !view.session || view.sessionId !== sessionId) return;
    const { defaultAgent, defaultModel, ...sessionForCache } = view.session;
    void defaultAgent;
    void defaultModel;
    mirrorWriteRef.current = () => {
      updateCachedSession(sessionId, (prev) => ({
        ...prev,
        session: sessionForCache,
        sessionTree: view.sessionTree,
        ...latestPage(view.messages, view.parts, pageSize),
        totalMessages: Math.max(prev.totalMessages ?? 0, totalMessages),
      }));
    };
    if (mirrorTimerRef.current === null) {
      mirrorTimerRef.current = window.setTimeout(() => {
        mirrorTimerRef.current = null;
        mirrorWriteRef.current?.();
      }, 500);
    }
  }, [sessionId, view.sessionId, view.session, view.sessionTree, view.messages, view.parts, totalMessages, updateCachedSession, pageSize]);

  useEffect(() => () => {
    if (mirrorTimerRef.current !== null) {
      window.clearTimeout(mirrorTimerRef.current);
      mirrorTimerRef.current = null;
    }
    mirrorWriteRef.current?.();
    mirrorWriteRef.current = null;
  }, [sessionId]);
}
