// @vitest-environment jsdom
import { act, renderHook, waitFor } from '@testing-library/react';
import { afterEach, expect, it, vi } from 'vitest';
import { useGitInfo } from './useGitInfo';

afterEach(() => { vi.unstubAllGlobals(); vi.useRealTimers(); });

it('retains same-owner branch data while paused and during resume refresh', async () => {
  const fetcher = vi.fn().mockResolvedValueOnce(new Response(JSON.stringify({ '/repo': { branch: 'task' } }), { status: 200 }))
    .mockReturnValue(new Promise(() => {}));
  vi.stubGlobal('fetch', fetcher);
  const view = renderHook(({ enabled }) => useGitInfo(['/repo'], 'local', enabled), { initialProps: { enabled: true } });
  await waitFor(() => expect(view.result.current.infos['/repo']?.branch).toBe('task'));
  vi.useFakeTimers();
  try {
    view.rerender({ enabled: false });
    await act(async () => { await vi.advanceTimersByTimeAsync(60_000); });
    expect(fetcher).toHaveBeenCalledTimes(1);
    expect(view.result.current.infos['/repo']?.branch).toBe('task');
    view.rerender({ enabled: true });
    expect(fetcher).toHaveBeenCalledTimes(2);
    expect(view.result.current.infos['/repo']?.branch).toBe('task');
  } finally { view.unmount(); }
});

it('retains still-requested same-owner branches when directories change while paused', async () => {
  const fetcher = vi.fn().mockResolvedValue(new Response(JSON.stringify({ '/repo': { branch: 'task' }, '/removed': { branch: 'old' } }), { status: 200 }));
  vi.stubGlobal('fetch', fetcher);
  const view = renderHook(({ dirs, owner, enabled }) => useGitInfo(dirs, owner, enabled), {
    initialProps: { dirs: ['/repo', '/removed'], owner: 'local', enabled: true },
  });
  await waitFor(() => expect(view.result.current.infos['/repo']?.branch).toBe('task'));
  view.rerender({ dirs: ['/repo', '/added'], owner: 'local', enabled: false });
  expect(view.result.current.infos).toEqual({ '/repo': { branch: 'task' } });
  expect(fetcher).toHaveBeenCalledTimes(1);
  view.rerender({ dirs: ['/repo', '/added'], owner: 'other', enabled: false });
  expect(view.result.current.infos).toEqual({});
  view.unmount();
});

it('clears branch data when the owner changes', async () => {
  const snapshots: Array<{ owner: string; branch?: string }> = [];
  const fetcher = vi.fn()
    .mockResolvedValueOnce(new Response(JSON.stringify({ '/repo': { branch: 'old' } }), { status: 200 }))
    .mockReturnValueOnce(new Promise(() => {}));
  vi.stubGlobal('fetch', fetcher);
  const { result, rerender } = renderHook(
    ({ remoteId }) => {
      const value = useGitInfo(['/repo'], remoteId);
      snapshots.push({ owner: remoteId, branch: value.infos['/repo']?.branch });
      return value;
    },
    { initialProps: { remoteId: 'old-owner' } },
  );
  await waitFor(() => expect(result.current.infos['/repo']?.branch).toBe('old'));

  rerender({ remoteId: 'new-owner' });
  await waitFor(() => expect(result.current.infos).toEqual({}));
  expect(snapshots).not.toContainEqual({ owner: 'new-owner', branch: 'old' });
});

it('keeps the infos reference when a poll returns the same data', async () => {
  // A fresh object per 30s poll re-rendered every sidebar row.
  const body = JSON.stringify({ '/repo': { branch: 'main' } });
  vi.stubGlobal('fetch', vi.fn(async () => new Response(body, { status: 200 })));
  const { result } = renderHook(() => useGitInfo(['/repo'], 'local'));
  await waitFor(() => expect(result.current.infos['/repo']?.branch).toBe('main'));
  const first = result.current.infos;

  act(() => { document.dispatchEvent(new Event('visibilitychange')); });
  await waitFor(() => expect(result.current.loading).toBe(false));
  await act(async () => { await Promise.resolve(); });
  expect(vi.mocked(fetch)).toHaveBeenCalledTimes(2);
  expect(result.current.infos).toBe(first);
});
