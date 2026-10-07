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
