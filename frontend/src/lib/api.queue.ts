import { apiFetch, raiseForUnauthorized, readJSON } from './api.requests';
import type { QueuedMessage } from './api.types';

function messageQueueAPI(resource: 'session' | 'session-drafts') {
  const path = (id: string, suffix: string, platform?: string) =>
    `/api/${resource}/${encodeURIComponent(id)}/queue${suffix}${platform ? `?platform=${encodeURIComponent(platform)}` : ''}`;
  return {
    queuedMessages: async (id: string, platform?: string): Promise<QueuedMessage[]> => {
      const resp = await apiFetch(path(id, '', platform));
      await raiseForUnauthorized(resp);
      if (!resp.ok) throw new Error(`HTTP ${resp.status}`);
      return readJSON<QueuedMessage[]>(resp);
    },
    deleteQueuedMessage: async (id: string, queuedId: string, platform?: string) => {
      const resp = await apiFetch(path(id, `/${encodeURIComponent(queuedId)}`, platform), { method: 'DELETE' });
      await raiseForUnauthorized(resp);
      if (!resp.ok && resp.status !== 404) throw new Error(`HTTP ${resp.status}`);
    },
    moveQueuedMessage: async (id: string, queuedId: string, direction: -1 | 1, platform?: string) => {
      const resp = await apiFetch(path(id, `/${encodeURIComponent(queuedId)}/move`, platform), {
        method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ direction }),
      });
      await raiseForUnauthorized(resp);
      if (!resp.ok) throw new Error(`HTTP ${resp.status}`);
    },
  };
}

export const sessionQueueAPI = messageQueueAPI('session');
export const draftQueueAPI = messageQueueAPI('session-drafts');
export type MessageQueueAPI = typeof sessionQueueAPI;
