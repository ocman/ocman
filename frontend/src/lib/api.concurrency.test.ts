import { afterEach, expect, it, vi } from 'vitest';
import { api } from './api';

afterEach(() => vi.unstubAllGlobals());

it('fetches concurrency with project/date filters and cancellation', async () => {
  const data = { bucketMs: 3_600_000, series: [{ timestamp: 1000, sessions: 2 }] };
  const fetch = vi.fn().mockResolvedValue(new Response(JSON.stringify(data), { status: 200 }));
  vi.stubGlobal('fetch', fetch);
  const signal = new AbortController().signal;
  expect(await api.sessionConcurrency({ days: 7, dir: '/repo/foo' }, signal)).toEqual(data);
  expect(fetch).toHaveBeenCalledWith('/api/analytics/session-concurrency?days=7&dir=%2Frepo%2Ffoo', expect.objectContaining({ signal }));
});
