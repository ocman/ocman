import { useCallback, useEffect, useRef, useState } from 'react';
import type { Dispatch, SetStateAction } from 'react';
import type { Message, Part } from '../../lib/api';
import { listFailedSends, type FailedSend } from '../../lib/failedSends';
import type { UsePendingSendResult } from './usePendingSend';

export interface UseFailedSendRehydrateOptions {
  id: string | undefined;
  /** Truthy once the session detail has loaded. */
  sessionLoaded: boolean;
  messages: Message[];
  parts: Part[];
  pending: UsePendingSendResult;
}

export interface UseFailedSendRehydrateResult {
  failedSends: FailedSend[];
  setFailedSends: Dispatch<SetStateAction<FailedSend[]>>;
}

/**
 * Owns the per-session list of failed sends (persisted in localStorage
 * by `useSessionActions`) and replays the first un-delivered one into
 * the pending bubble after a page refresh so its retry banner
 * re-appears. Entries whose text already exists as a real user message
 * reached the server and are skipped.
 */
export function useFailedSendRehydrate({
  id,
  sessionLoaded,
  messages,
  parts,
  pending,
}: UseFailedSendRehydrateOptions): UseFailedSendRehydrateResult {
  // Keyed on `id` and reset during render (derived-state pattern) so
  // switching sessions never shows the previous session's failures.
  const [state, setState] = useState<{ id: string | undefined; list: FailedSend[] }>(() => ({
    id,
    list: id ? listFailedSends(id) : [],
  }));
  if (state.id !== id) {
    setState({ id, list: id ? listFailedSends(id) : [] });
  }
  // On the mismatch render above React re-renders before commit, so
  // `state.list` is always the current session's list by the time
  // effects run.
  const failedSends = state.list;
  const setFailedSends = useCallback<Dispatch<SetStateAction<FailedSend[]>>>((update) => {
    setState((prev) => ({
      id: prev.id,
      list: typeof update === 'function' ? update(prev.list) : update,
    }));
  }, []);

  const rehydratedRef = useRef<Set<string>>(new Set());
  useEffect(() => {
    rehydratedRef.current = new Set();
  }, [id]);
  useEffect(() => {
    if (!sessionLoaded || failedSends.length === 0) return;
    if (pending.pending) return; // a fresh send is already in flight
    const realUserTexts = new Set(
      messages
        .filter((m) => m.data?.role === 'user')
        .flatMap((m) =>
          parts
            .filter((p) => p.messageId === m.id)
            .map((p) => {
              try {
                const pd = typeof p.data === 'string' ? JSON.parse(p.data) : p.data;
                return pd?.type === 'text' ? (pd.text || '') : '';
              } catch {
                return '';
              }
            })
            .filter(Boolean),
        ),
    );
    const ghost = failedSends.find((e) =>
      !rehydratedRef.current.has(e.id) && !realUserTexts.has(e.text),
    );
    if (!ghost) return;
    rehydratedRef.current.add(ghost.id);
    pending.begin(ghost.text, ghost.images, {
      model: ghost.model,
      agent: ghost.agent,
      reasoning: ghost.reasoning,
    });
    pending.fail(ghost.error);
  }, [sessionLoaded, failedSends, messages, parts, pending]);

  return { failedSends, setFailedSends };
}
