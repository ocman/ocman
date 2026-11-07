import { afterEach, expect, it, vi } from 'vitest';
import { draftQueueAPI, sessionQueueAPI } from './api.queue';

afterEach(() => vi.unstubAllGlobals());

it.each([['session', sessionQueueAPI], ['session-drafts', draftQueueAPI]] as const)('routes %s queue reads and mutations through the same resource', async (resource, api) => {
  const fetch = vi.fn().mockResolvedValue(new Response('[]'));
  vi.stubGlobal('fetch', fetch);
  expect(await api.queuedMessages('ocm_test', 'r-box:opencode')).toEqual([]);
  expect(fetch).toHaveBeenLastCalledWith(`/api/${resource}/ocm_test/queue?platform=r-box%3Aopencode`, undefined);
  fetch.mockResolvedValue(new Response(null, { status: 204 }));
  await api.deleteQueuedMessage('ocm_test', 'q/1', 'r-box:opencode');
  expect(fetch).toHaveBeenLastCalledWith(`/api/${resource}/ocm_test/queue/q%2F1?platform=r-box%3Aopencode`, { method: 'DELETE' });
  await api.moveQueuedMessage('ocm_test', 'q/1', -1, 'r-box:opencode');
  expect(fetch).toHaveBeenLastCalledWith(`/api/${resource}/ocm_test/queue/q%2F1/move?platform=r-box%3Aopencode`, {
    method: 'POST', headers: { 'Content-Type': 'application/json' }, body: '{"direction":-1}',
  });
});

it('keeps draft queue delete idempotent and surfaces read and move failures', async () => {
  vi.stubGlobal('fetch', vi.fn().mockImplementation(async () => new Response('missing', { status: 404 })));
  await expect(draftQueueAPI.deleteQueuedMessage('draft', 'gone')).resolves.toBeUndefined();
  await expect(draftQueueAPI.queuedMessages('draft')).rejects.toThrow('HTTP 404');
  await expect(draftQueueAPI.moveQueuedMessage('draft', 'gone', 1)).rejects.toThrow('HTTP 404');
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response('failure', { status: 500 })));
  await expect(draftQueueAPI.deleteQueuedMessage('draft', 'held')).rejects.toThrow('HTTP 500');
});
