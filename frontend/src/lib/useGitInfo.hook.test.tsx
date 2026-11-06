// @vitest-environment jsdom
import { act, waitFor } from '@testing-library/react';
import { renderHookWithQuery as renderHook } from '../test/renderWithQuery';
import { afterEach, expect, it, vi } from 'vitest';
import { useCheckoutGitInfos, useGitInfo } from './useGitInfo';
import { checkoutKey } from './projectIdentity';

afterEach(() => { vi.unstubAllGlobals(); vi.useRealTimers(); });

it('fetches every owner independently and keeps same-path branches separate', async () => {
  const fetcher = vi.fn(async (input: string) => {
    const url = new URL(input, 'http://localhost');
    const owner = url.searchParams.get('remoteId');
    return new Response(JSON.stringify({ '/repo': { branch: `${owner}-branch`, dirty: owner === 'box' } }), { status: 200 });
  });
  vi.stubGlobal('fetch', fetcher);
  const checkouts = [{ directory: '/repo' }, { directory: '/repo', remoteId: 'box' }, { directory: '/repo', remoteId: 'box' }, { directory: '' }];
  const view = renderHook(({ rows, enabled }) => useCheckoutGitInfos(rows, enabled), { initialProps: { rows: checkouts, enabled: true } });
  await waitFor(() => expect(view.result.current[checkoutKey('/repo', 'box')]?.branch).toBe('box-branch'));
  expect(view.result.current[checkoutKey('/repo', 'local')]?.branch).toBe('local-branch');
  expect(view.result.current[checkoutKey('/repo', 'box')]?.dirty).toBe(true);
  expect(fetcher).toHaveBeenCalledTimes(2);
  expect(fetcher.mock.calls.map(([url]) => new URL(url, 'http://localhost').searchParams.get('dirs'))).toEqual(['/repo', '/repo']);
  const first = view.result.current;
  view.rerender({ rows: [...checkouts].reverse(), enabled: true });
  expect(view.result.current).toBe(first);
  vi.useFakeTimers();
  view.rerender({ rows: checkouts, enabled: false });
  await act(async () => { await vi.advanceTimersByTimeAsync(60_000); });
  expect(view.result.current).toEqual(first);
  expect(fetcher).toHaveBeenCalledTimes(2);
  view.unmount();
});

it('does not let an offline owner hide healthy owners or fall back to local git', async () => {
  const fetcher = vi.fn(async (input: string) => input.includes('remoteId=offline')
    ? new Response('offline', { status: 503 })
    : new Response(JSON.stringify({ '/repo': { branch: 'main' } }), { status: 200 }));
  vi.stubGlobal('fetch', fetcher);
  const { result, rerender } = renderHook(({ rows }) => useCheckoutGitInfos(rows), {
    initialProps: { rows: [{ directory: '/repo', remoteId: 'local' }, { directory: '/repo', remoteId: 'offline' }] },
  });
  await waitFor(() => expect(result.current[checkoutKey('/repo', 'local')]?.branch).toBe('main'));
  expect(result.current[checkoutKey('/repo', 'offline')]).toBeUndefined();
  expect(fetcher).toHaveBeenCalledTimes(2);
  rerender({ rows: [] });
  expect(result.current).toEqual({});
  expect(fetcher).toHaveBeenCalledTimes(2);
});

it('keeps known branches visible while a changed directory list refreshes', async () => {
  const fetcher = vi.fn().mockResolvedValueOnce(new Response(JSON.stringify({ '/repo': { branch: 'task' } }), { status: 200 }))
    .mockReturnValue(new Promise(() => {}));
  vi.stubGlobal('fetch', fetcher);
  const view = renderHook(({ rows }) => useCheckoutGitInfos(rows), {
    initialProps: { rows: [{ directory: '/repo', remoteId: 'box' }] },
  });
  await waitFor(() => expect(view.result.current[checkoutKey('/repo', 'box')]?.branch).toBe('task'));
  view.rerender({ rows: [{ directory: '/repo', remoteId: 'box' }, { directory: '/new', remoteId: 'box' }] });
  expect(view.result.current[checkoutKey('/repo', 'box')]?.branch).toBe('task');
  expect(fetcher).toHaveBeenCalledTimes(2);
  view.rerender({ rows: [{ directory: '/new', remoteId: 'box' }] });
  expect(view.result.current[checkoutKey('/repo', 'box')]).toBeUndefined();
  view.rerender({ rows: [{ directory: '/repo', remoteId: 'other' }] });
  expect(view.result.current[checkoutKey('/repo', 'other')]).toBeUndefined();
  view.unmount();
});

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

  act(() => { document.dispatchEvent(new Event('visibilitychange', { bubbles: true })); });
  await waitFor(() => expect(result.current.loading).toBe(false));
  await act(async () => { await Promise.resolve(); });
  expect(vi.mocked(fetch)).toHaveBeenCalledTimes(2);
  expect(result.current.infos).toBe(first);
});
