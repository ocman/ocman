// useGhostInjection — re-injects ghost user-message bubbles for failed
// sends that survived a page refresh. The optimistic messages are never
// written to the DB, so on a cold load they're absent from `messages` even
// though the persisted entry is back in `failedSends`. The hook skips
// entries whose text already appears as a real user message in the loaded
// thread (request reached the server and SSE delivered it).
//
// `messagesRef` / `partsRef` are used instead of listing messages/parts as
// deps to avoid the infinite loop where ghost injection appends to messages
// → memory-trimming effect trims messages → ghost injection re-fires.

import { useEffect, useRef } from 'react';
import { removeFailedSend, type FailedSend } from '../../lib/failedSends';
import type { Message, Part } from '../../lib/api';

interface GhostInjectionOptions {
  session: { id: string } | null;
  failedSends: FailedSend[];
  setFailedSends: React.Dispatch<React.SetStateAction<FailedSend[]>>;
  messagesRef: React.MutableRefObject<Message[]>;
  partsRef: React.MutableRefObject<Part[]>;
  setMessages: React.Dispatch<React.SetStateAction<Message[]>>;
  setParts: React.Dispatch<React.SetStateAction<Part[]>>;
}

export function useGhostInjection({
  session,
  failedSends,
  setFailedSends,
  messagesRef,
  partsRef,
  setMessages,
  setParts,
}: GhostInjectionOptions): void {
  // Track which ghost IDs we've already injected so the effect is
  // idempotent even when `messages` / `parts` change.
  const injectedGhostIdsRef = useRef<Set<string>>(new Set());

  // Reset the injected set when the session changes.
  useEffect(() => {
    injectedGhostIdsRef.current = new Set();
  }, [session?.id]);

  useEffect(() => {
    if (!session || failedSends.length === 0) return;
    const currentMessages = messagesRef.current;
    const currentParts = partsRef.current;
    const existingIds = new Set(currentMessages.map(m => m.id));
    const realUserTexts = new Set(
      currentMessages
        .filter(m => m.data?.role === 'user')
        .flatMap(m =>
          currentParts
            .filter(p => p.messageId === m.id)
            .map(p => {
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

    const ghostsToInject = failedSends.filter(e => {
      if (existingIds.has(e.id)) return false;
      if (injectedGhostIdsRef.current.has(e.id)) return false;
      if (e.text && realUserTexts.has(e.text)) return false;
      return true;
    });
    if (ghostsToInject.length === 0) return;

    const newMsgs: Message[] = [];
    const newParts: Part[] = [];
    for (const entry of ghostsToInject) {
      injectedGhostIdsRef.current.add(entry.id);
      newMsgs.push({
        id: entry.id,
        sessionId: session.id,
        timeCreated: entry.failedAt,
        data: { role: 'user' },
      });
      if (entry.text) {
        newParts.push({
          id: 'part-' + entry.id,
          messageId: entry.id,
          sessionId: session.id,
          data: { type: 'text', text: entry.text } as unknown as string,
        });
      }
      if (entry.images) {
        entry.images.forEach((img, i) => {
          newParts.push({
            id: `part-${entry.id}-img-${i}`,
            messageId: entry.id,
            sessionId: session.id,
            data: { type: 'file', mime: img.mime, url: img.url } as unknown as string,
          });
        });
      }
    }
    setMessages(prev => [...prev, ...newMsgs]);
    setParts(prev => [...prev, ...newParts]);

    // Drop rehydrated entries that were filtered out (request succeeded server-side).
    const droppedIds = failedSends
      .filter(e => !ghostsToInject.includes(e) && !existingIds.has(e.id))
      .map(e => e.id);
    if (droppedIds.length > 0) {
      setFailedSends(prev => prev.filter(e => !droppedIds.includes(e.id)));
      droppedIds.forEach(idToDrop => removeFailedSend(session.id, idToDrop));
    }
  }, [session, failedSends, messagesRef, partsRef, setMessages, setParts, setFailedSends]);
}
