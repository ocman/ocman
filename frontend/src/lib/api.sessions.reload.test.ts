import { afterEach, expect, it, vi } from 'vitest';
import { sessionApi } from './api.sessions';

afterEach(() => vi.unstubAllGlobals());

it('reloads the owner-qualified session and accepts the empty response', async () => {
  const fetch = vi.fn().mockResolvedValue(new Response(null, { status: 204 }));
  vi.stubGlobal('fetch', fetch);
  await sessionApi.reloadOpencode('session/id', 'r-owner:opencode');
  expect(fetch).toHaveBeenCalledWith(
    '/api/session/session%2Fid/reload-opencode?platform=r-owner%3Aopencode',
    expect.objectContaining({ method: 'POST' }),
  );
});

it('qualifies agent and command catalogs by owner', async () => {
  const fetch = vi.fn().mockImplementation(() => Promise.resolve(new Response('[]', { status: 200 })));
  vi.stubGlobal('fetch', fetch);
  await sessionApi.commands('duplicate', undefined, 'r-owner:opencode');
  await sessionApi.agents('duplicate', undefined, 'r-owner:opencode');
  expect(fetch.mock.calls.map((call) => call[0])).toEqual([
    '/api/session/duplicate/commands?platform=r-owner%3Aopencode',
    '/api/session/duplicate/agents?platform=r-owner%3Aopencode',
  ]);
});
