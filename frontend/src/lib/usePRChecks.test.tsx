// @vitest-environment jsdom
import { act, cleanup, renderHook } from '@testing-library/react';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { usePRChecks } from './usePRChecks';
import { clearPRChecksCache, getCachedPRChecks, resetPRChecksMemoryForTest } from './prChecksCache';
import type { PRChecks } from './upstreamApi';
import { UpstreamApiError } from './upstreamApi';

const empty: PRChecks = { state: 'unknown', checks: [] };
const pending: PRChecks = { state: 'pending', checks: [{ name: 'build', state: 'pending' }] };
const done: PRChecks = { state: 'success', checks: [{ name: 'build', state: 'success' }] };
const advance = async (ms: number) => { await act(async () => { await vi.advanceTimersByTimeAsync(ms); }); };
const mount = (fetchChecks: (signal: AbortSignal, refresh: boolean) => Promise<PRChecks>) => renderHook(() => usePRChecks('host/repo@sha', 'owner/sha', true, fetchChecks));

beforeEach(() => {
  vi.useFakeTimers();
  localStorage.clear();
  clearPRChecksCache();
  vi.spyOn(document, 'hidden', 'get').mockReturnValue(false);
});
afterEach(() => { cleanup(); vi.restoreAllMocks(); vi.useRealTimers(); });

it('confirms empty checks twice at 30s intervals, then stops and caches across remounts', async () => {
  const fetch = vi.fn().mockResolvedValue(empty);
  const hook = mount(fetch);
  await advance(0);
  expect(getCachedPRChecks('host/repo@sha')).toBeUndefined();
  await advance(29_999);
  expect(fetch).toHaveBeenCalledTimes(1);
  await advance(1);
  expect(fetch).toHaveBeenCalledTimes(2);
  await advance(30_000);
  expect(fetch).toHaveBeenCalledTimes(3);
  resetPRChecksMemoryForTest();
  expect(getCachedPRChecks('host/repo@sha')).toEqual(empty);
  await advance(120_000);
  expect(fetch).toHaveBeenCalledTimes(3);
  hook.unmount();
  mount(fetch);
  await advance(0);
  expect(fetch).toHaveBeenCalledTimes(3);
});

it('picks up checks appearing during the empty-check grace', async () => {
  const fetch = vi.fn().mockResolvedValueOnce(empty).mockResolvedValueOnce(pending).mockResolvedValue(done);
  mount(fetch);
  await advance(30_000);
  expect(fetch).toHaveBeenCalledTimes(2);
  expect(getCachedPRChecks('host/repo@sha')).toBeUndefined();
  await advance(5_000);
  expect(getCachedPRChecks('host/repo@sha')).toEqual(done);
});

it('backs errors off to 60s and resets the delay after success', async () => {
  const fetch = vi.fn().mockRejectedValue(new Error('upstream'));
  mount(fetch);
  await advance(0);
  for (const delay of [5_000, 10_000, 20_000, 40_000, 60_000, 60_000]) {
    const calls = fetch.mock.calls.length;
    await advance(delay - 1);
    expect(fetch).toHaveBeenCalledTimes(calls);
    await advance(1);
    expect(fetch).toHaveBeenCalledTimes(calls + 1);
  }
  fetch.mockResolvedValueOnce(pending);
  await advance(60_000);
  await advance(5_000);
  const calls = fetch.mock.calls.length;
  await advance(4_999);
  expect(fetch).toHaveBeenCalledTimes(calls);
  await advance(1);
  expect(fetch).toHaveBeenCalledTimes(calls + 1);
});

it('never settles rate-limited empty responses', async () => {
  const fetch = vi.fn().mockResolvedValue({ ...empty, rateLimit: { limited: true } });
  mount(fetch);
  await advance(120_000);
  expect(fetch.mock.calls.length).toBeGreaterThan(3);
  expect(getCachedPRChecks('host/repo@sha')).toBeUndefined();
});

it('honors a rate-limit retry time beyond the error backoff cap', async () => {
  const fetch = vi.fn().mockRejectedValueOnce(new UpstreamApiError({ error: { code: 'rate_limited', message: 'limited', retryAfter: new Date(Date.now() + 120_000).toISOString() } }, 429)).mockResolvedValue(done);
  mount(fetch);
  await advance(119_999);
  expect(fetch).toHaveBeenCalledTimes(1);
  await advance(1);
  expect(getCachedPRChecks('host/repo@sha')).toEqual(done);
});

it('pauses while hidden and resumes when the document becomes visible', async () => {
  const hidden = vi.spyOn(document, 'hidden', 'get').mockReturnValue(true);
  const fetch = vi.fn().mockResolvedValue(pending);
  mount(fetch);
  await advance(60_000);
  expect(fetch).not.toHaveBeenCalled();
  hidden.mockReturnValue(false);
  act(() => { document.dispatchEvent(new Event('visibilitychange')); });
  await advance(0);
  expect(fetch).toHaveBeenCalledTimes(1);
  hidden.mockReturnValue(true);
  act(() => { document.dispatchEvent(new Event('visibilitychange')); });
  await advance(60_000);
  expect(fetch).toHaveBeenCalledTimes(1);
});
