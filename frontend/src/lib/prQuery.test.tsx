// @vitest-environment jsdom
import { act, cleanup, renderHook, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { usePRMergeability } from './usePRMergeability';
import { clearPRChecksCache, getCachedPRChecks } from './prChecksCache';
import { previewAuthChanged } from './previews';
import { usePRChecks } from './usePRChecks';
import type { PRChecks } from './upstreamApi';

const pr = { host: 'github.com', repo: 'o/r', number: 42, headSha: 'sha', updatedAt: 'today', status: 'open' as const };
const done = { mergeable: true, approved: true };
const mount = (load: (signal: AbortSignal, refresh: boolean) => Promise<typeof done>) =>
  renderHook(() => usePRMergeability(pr, '/repo', 'local', 'origin', true, load));
beforeEach(() => clearPRChecksCache());
afterEach(cleanup);

it('keeps a shared request alive until its last visible reader leaves', () => {
  const load = vi.fn().mockImplementation(() => new Promise(() => {}));
  const first = mount(load);
  const second = mount(load);
  expect(load).toHaveBeenCalledOnce();
  first.unmount();
  expect(load.mock.calls[0][0].aborted).toBe(false);
  second.unmount();
  expect(load.mock.calls[0][0].aborted).toBe(true);
  mount(load);
  expect(load).toHaveBeenCalledTimes(2);
});

it('cancels an in-flight read when its last reader is hidden, not while another reader is visible', () => {
  const load = vi.fn().mockImplementation(() => new Promise(() => {}));
  const first = renderHook(({ visible }) => usePRMergeability(pr, '/repo', 'local', 'origin', visible, load), { initialProps: { visible: true } });
  const second = mount(load);
  first.rerender({ visible: false });
  expect(load.mock.calls[0][0].aborted).toBe(false);
  second.unmount();
  expect(load.mock.calls[0][0].aborted).toBe(true);
});

it('retains settled results across unmounts without fetching again', async () => {
  const load = vi.fn().mockResolvedValue(done);
  const first = mount(load);
  await waitFor(() => expect(first.result.current).toEqual(done));
  first.unmount();
  expect(mount(load).result.current).toEqual(done);
  expect(load).toHaveBeenCalledOnce();
});

it('shares check requests and never persists a response after its last reader leaves', async () => {
  let finish!: (value: PRChecks) => void;
  const load = vi.fn().mockImplementation(() => new Promise<PRChecks>((resolve) => { finish = resolve; }));
  const useChecks = () => usePRChecks('github.com/o/r@sha', 'local/sha', true, load);
  const first = renderHook(useChecks);
  const second = renderHook(useChecks);
  expect(load).toHaveBeenCalledOnce();
  first.unmount();
  expect(load.mock.calls[0][0].aborted).toBe(false);
  second.unmount();
  expect(load.mock.calls[0][0].aborted).toBe(true);
  await act(async () => { finish({ state: 'success', checks: [{ name: 'build', state: 'success' }] }); });
  expect(getCachedPRChecks('github.com/o/r@sha')).toBeUndefined();
});

it('keeps status requests for different owners independent', () => {
  const load = vi.fn().mockImplementation(() => new Promise(() => {}));
  const local = mount(load);
  renderHook(() => usePRMergeability(pr, '/repo', 'remote-box', 'origin', true, load));
  expect(load).toHaveBeenCalledTimes(2);
  local.unmount();
  expect(load.mock.calls[0][0].aborted).toBe(true);
  expect(load.mock.calls[1][0].aborted).toBe(false);
});

it('retains reviews-only results with known mergeability', async () => {
  const load = vi.fn().mockResolvedValue({ mergeable: null, approved: false });
  const useStatus = () => usePRMergeability({ ...pr, mergeable: true }, '/repo', 'local', 'origin', true, load);
  const first = renderHook(useStatus);
  await waitFor(() => expect(first.result.current.approved).toBe(false));
  first.unmount();
  expect(renderHook(useStatus).result.current).toEqual({ mergeable: true, approved: false });
  expect(load).toHaveBeenCalledOnce();
});

it('invalidates only matching repositories and drops retained data after credential changes', async () => {
  const load = vi.fn().mockResolvedValue(done);
  const first = mount(load);
  await waitFor(() => expect(first.result.current).toEqual(done));
  act(() => clearPRChecksCache(['github.com/other/repo']));
  expect(load).toHaveBeenCalledOnce();
  act(() => clearPRChecksCache(['github.com/o/r']));
  await waitFor(() => expect(load).toHaveBeenCalledTimes(2));
  first.unmount();
  previewAuthChanged();
  mount(load);
  await waitFor(() => expect(load).toHaveBeenCalledTimes(3));
});

it('reloads mounted status readers after credential changes', async () => {
  const load = vi.fn().mockResolvedValue(done);
  const first = mount(load);
  await waitFor(() => expect(first.result.current).toEqual(done));
  act(() => previewAuthChanged());
  await waitFor(() => expect(load).toHaveBeenCalledTimes(2));
});

it('shares an explicit refresh between readers and consumes it before a new PR head', async () => {
  const load = vi.fn().mockResolvedValue(done);
  const first = renderHook(({ headSha }) => usePRMergeability({ ...pr, headSha }, '/repo', 'local', 'origin', true, load), { initialProps: { headSha: 'sha' } });
  const second = mount(load);
  await waitFor(() => expect(second.result.current).toEqual(done));
  act(() => clearPRChecksCache());
  await waitFor(() => expect(load).toHaveBeenCalledTimes(2));
  expect(load).toHaveBeenLastCalledWith(expect.any(AbortSignal), true);
  await waitFor(() => expect(first.result.current).toEqual(done));
  mount(load);
  expect(load).toHaveBeenCalledTimes(2);
  first.rerender({ headSha: 'new-sha' });
  await waitFor(() => expect(load).toHaveBeenCalledTimes(3));
  expect(load).toHaveBeenLastCalledWith(expect.any(AbortSignal), false);
});
