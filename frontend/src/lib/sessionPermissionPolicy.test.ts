import { afterEach, expect, it, vi } from 'vitest';
import { sessionPermissionPolicy } from './sessionPermissionPolicy';

afterEach(() => vi.unstubAllGlobals());

it('routes policy reads and writes with both session and platform identity', async () => {
  const fetch = vi.fn().mockResolvedValue(new Response('{"rules":[],"requests":[]}'));
  vi.stubGlobal('fetch', fetch);
  await sessionPermissionPolicy.get('s/1', 'r-box:agent');
  expect(fetch.mock.calls[0][0]).toBe('/api/session/s%2F1/permission-policy?platform=r-box%3Aagent');
  fetch.mockResolvedValue(new Response('{"rules":[],"requests":[]}'));
  await sessionPermissionPolicy.save('s/1', 'r-box:agent', []);
  expect(fetch.mock.calls[1][1]).toEqual(expect.objectContaining({ method: 'PUT', body: '{"rules":[]}' }));
});
