import type { Page } from '@playwright/test';
import type { BackendConversationDraft } from '../src/lib/backendConversationDrafts';
import type { StartSessionRequest, StartSessionResponse } from '../src/lib/api.types';

/** Stateful draft persistence shared by browser specs; never launches an agent. */
export async function mockDraftRoutes(page: Page, start?: (request: StartSessionRequest, id: string) => StartSessionResponse) {
  const drafts = new Map<string, BackendConversationDraft>();
  await page.route('**/api/session-drafts**', async (route) => {
    const request = route.request();
    const [, id, action] = new URL(request.url()).pathname.split('/').slice(2);
    if (!id) return route.fulfill({ json: [...drafts.values()] });
    const previous = drafts.get(id);
    if (request.method() === 'PUT') {
      if (!previous || previous.phase === 'draft') drafts.set(id, {
        id, metadata: request.postDataJSON(), phase: 'draft', steps: {}, createdAt: previous?.createdAt ?? Date.now(),
      });
      return route.fulfill({ status: 204 });
    }
    if (!previous) return route.fulfill({ status: 404 });
    if (request.method() === 'DELETE') {
      if (!['draft', 'discarded', 'failed', 'interrupted'].includes(previous.phase) || previous.result?.sessionId === id) return route.fulfill({ status: 409 });
      drafts.set(id, { ...previous, phase: 'discarded', metadata: { draftId: id, directory: '' }, request: undefined, result: undefined, steps: {}, error: undefined });
      return route.fulfill({ status: 204 });
    }
    if (action === 'start') {
      const submitted = request.postDataJSON() as StartSessionRequest;
      try {
        const result = start?.(submitted, id);
        const accepted: BackendConversationDraft = { ...previous, request: submitted, submittedAt: Date.now(),
          phase: result ? 'ready' : 'accepted', steps: result ? { prompt: 'done' } : {}, result };
        drafts.set(id, accepted);
        return route.fulfill({ status: 202, json: accepted });
      } catch (error) {
        return route.fulfill({ status: 502, body: String(error) });
      }
    }
    return route.fulfill({ json: previous });
  });
}
