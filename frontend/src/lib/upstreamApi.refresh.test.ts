// @vitest-environment jsdom
import { afterEach, expect, it, vi } from 'vitest';
import { fetchPRs, fetchIssues, fetchPRChecks, fetchPRMergeability } from './upstreamApi';

afterEach(() => vi.restoreAllMocks());

const base = { dir: '/repo', remoteId: 'local', remote: 'origin', signal: new AbortController().signal };
const readers = [
  (refresh: boolean) => fetchPRs({ ...base, refresh, page: 1, state: 'open', mine: undefined }),
  (refresh: boolean) => fetchIssues({ ...base, refresh, page: 1, state: 'open', mine: undefined }),
  (refresh: boolean) => fetchPRChecks({ ...base, refresh, sha: 'abc123' }),
  (refresh: boolean) => fetchPRMergeability({ ...base, refresh, number: 42 }),
];

it.each(readers)('requests cached data normally and fresh data only with the refresh flag', async (read) => {
  const fetch = vi.spyOn(globalThis, 'fetch').mockImplementation(async () => new Response('{}', { status: 200 }));
  await read(false);
  expect(new URL(String(fetch.mock.calls[0][0]), 'http://localhost').searchParams.has('refresh')).toBe(false);
  await read(true);
  expect(new URL(String(fetch.mock.calls[1][0]), 'http://localhost').searchParams.get('refresh')).toBe('1');
});
