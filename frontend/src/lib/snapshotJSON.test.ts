import { beforeEach, afterEach, describe, expect, it, vi } from 'vitest';
import { clearSessionSnapshots, snapshotJSON, snapshotPollingDelay } from './snapshotJSON';

describe('conditional session snapshots', () => {
  beforeEach(() => clearSessionSnapshots());
  afterEach(() => { vi.unstubAllGlobals(); vi.useRealTimers(); });

  it('subtracts snapshot age so sharing does not add another recovery interval', async () => {
    vi.useFakeTimers();
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response('[]', { headers: { ETag: '"v1"', 'X-Ocman-Snapshot-Age-Ms': '4000' } })));
    const params = { view: 'sidebar' };
    await snapshotJSON('/api/sessions?view=sidebar');
    expect(snapshotPollingDelay('/api/sessions', params, 5000)).toBe(1000);
    vi.advanceTimersByTime(1000);
    expect(snapshotPollingDelay('/api/sessions', params, 5000)).toBe(250);
    expect(snapshotPollingDelay('/api/other', params, 5000)).toBe(5000);
  });

  it('reuses a matching version without exposing mutable cached data', async () => {
    const fetch = vi.fn().mockResolvedValueOnce(new Response(JSON.stringify([{ title: 'original' }]), { headers: { ETag: '"v1"' } }))
      .mockResolvedValueOnce(new Response(null, { status: 304 }));
    vi.stubGlobal('fetch', fetch);
    const first = await snapshotJSON<{ title: string }[]>('/api/sessions?view=sidebar');
    first[0].title = 'local change';
    const second = await snapshotJSON<{ title: string }[]>('/api/sessions?view=sidebar');
    expect(second).toEqual([{ title: 'original' }]);
    expect(fetch.mock.calls[1][1]).toMatchObject({ cache: 'no-store', headers: { 'If-None-Match': '"v1"' } });
  });

  it('does not return stale content after a failed read', async () => {
    const fetch = vi.fn().mockResolvedValueOnce(new Response('[]', { headers: { ETag: '"v1"' } }))
      .mockResolvedValueOnce(new Response('unavailable', { status: 503 }))
      .mockResolvedValueOnce(new Response('[]'));
    vi.stubGlobal('fetch', fetch);
    await snapshotJSON('/api/sessions');
    await expect(snapshotJSON('/api/sessions')).rejects.toThrow('unavailable');
    await snapshotJSON('/api/sessions');
    expect(fetch.mock.calls[2][1].headers).toBeUndefined();
  });

  it('separates queries, supports older servers and bounds retained entries', async () => {
    const fetch = vi.fn().mockImplementation(() => Promise.resolve(new Response('[]', { headers: { ETag: '"v1"' } })));
    vi.stubGlobal('fetch', fetch);
    for (let i = 0; i < 65; i++) await snapshotJSON(`/api/sessions?dir=${i}`);
    await snapshotJSON('/api/sessions?dir=0');
    expect(fetch.mock.calls.at(-1)![1].headers).toBeUndefined();
    fetch.mockResolvedValueOnce(new Response('[]'));
    await snapshotJSON('/api/sessions?dir=0');
    await snapshotJSON('/api/sessions?dir=0');
    expect(fetch.mock.calls.at(-1)![1].headers).toBeUndefined();
  });
});
