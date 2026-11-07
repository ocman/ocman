import type { Message, Part } from '../../lib/api.types';
import { fetchBackendDraft, isBackendDraft, useBackendConversationDrafts } from '../../lib/backendConversationDrafts';
import { useEffect } from 'react';
import { LaunchSetup } from './LaunchSetup';
import { StartProgress } from './StartProgress';
import { parsePart } from '../../lib/convertMessages';

/** Setup stays attached to the initial user message, including after reload. */
export function useSessionLaunchTimeline(id: string | undefined, messages: Message[], parts: Part[], hasMore: boolean) {
  const record = useBackendConversationDrafts((state) => id ? state.drafts[id] : undefined);
  useEffect(() => {
    if (isBackendDraft(id) && !record?.request) void fetchBackendDraft(id!).catch(() => {});
  }, [id, record?.request]);
  if (!record?.request || !record.submittedAt) return null;
  const first = !hasMore ? messages.find((message) => message.data.role === 'user') : undefined;
  const visible = first && parts.some((part) => part.messageId === first.id && (() => {
    const data = parsePart(part);
    return data.type === 'text' && !!data.text?.trim() || data.type === 'file';
  })());
  return {
    context: first && visible ? { messageId: first.id, timestamp: record.submittedAt,
      setup: <LaunchSetup steps={record.steps} platform={record.request.platform} complete={record.phase === 'ready'} /> } : null,
    // Assistant activity can stream before history contains the initial user
    // message. Keep the durable launch visible until that message has a body.
    empty: !hasMore && !visible ? <StartProgress prompt={record.request.prompt || ''} images={record.request.send?.images}
      timestamp={record.submittedAt} steps={record.steps} platform={record.request.platform} complete={record.phase === 'ready'} /> : null,
  };
}
