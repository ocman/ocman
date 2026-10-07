// @vitest-environment jsdom
import { act, renderHook, waitFor } from '@testing-library/react';
import { beforeEach, expect, it, vi } from 'vitest';
import { api, type SessionModelsResponse } from '../../lib/api';
import { clearModelCatalogCache, readModelCatalog } from '../../lib/modelCatalogCache';
import { useModelCatalog } from './useModelCatalog';

const { getModels } = vi.hoisted(() => ({ getModels: vi.fn() }));
vi.mock('../../lib/apiStore', () => ({ useApiStore: (select: (state: { getModels: typeof getModels }) => unknown) => select({ getModels }) }));
vi.mock('../../lib/api', () => ({ api: { sessionModels: vi.fn(), addFavorite: vi.fn(), removeFavorite: vi.fn() } }));
const catalog = (model: string): SessionModelsResponse => ({ hasProviders: true, models: [{ provider: 'p', model }] });

beforeEach(() => {
  vi.mocked(api.sessionModels).mockReset();
  getModels.mockReset();
  vi.mocked(api.addFavorite).mockReset();
  clearModelCatalogCache();
});

it.each(['resolve', 'reject'])('ignores an older favorite %s after navigating', async (outcome) => {
  let resolve!: () => void;
  let reject!: (error: Error) => void;
  vi.mocked(api.addFavorite).mockImplementationOnce(() => new Promise<void>((ok, fail) => { resolve = ok; reject = fail; }));
  vi.mocked(api.sessionModels).mockResolvedValue(catalog('new'));
  const { result, rerender } = renderHook((props) => useModelCatalog(props.id, 'opencode', props.dir, false), {
    initialProps: { id: 'a', dir: '/a' },
  });
  let favorite!: Promise<void>;
  act(() => { favorite = result.current.handleToggleFavorite('p', 'new', true); });
  rerender({ id: 'b', dir: '/b' });
  act(() => result.current.refreshModels());
  await waitFor(() => expect(result.current.modelOptions).toEqual(['p/new']));
  const calls = vi.mocked(api.sessionModels).mock.calls.length;
  await act(async () => {
    if (outcome === 'resolve') resolve(); else reject(new Error('old favorite failed'));
    await favorite;
  });
  expect(api.sessionModels).toHaveBeenCalledTimes(calls);
  expect(result.current.modelEntries[0].isFavorite).toBeUndefined();
});

it('rejects old-owner responses and callbacks after switching session', async () => {
  let resolveOld!: (value: SessionModelsResponse) => void;
  vi.mocked(api.sessionModels).mockImplementationOnce(() => new Promise((resolve) => { resolveOld = resolve; }));
  vi.mocked(api.sessionModels).mockResolvedValue(catalog('new'));
  const { result, rerender } = renderHook((props) => useModelCatalog(props.id, props.platform, props.directory, false), {
    initialProps: { id: 'a', platform: 'opencode', directory: '/a' },
  });
  const oldRefresh = result.current.refreshModels;
  act(() => oldRefresh());
  rerender({ id: 'b', platform: 'r-owner:opencode', directory: '/b' });
  act(() => result.current.refreshModels());
  await waitFor(() => expect(result.current.modelOptions).toEqual(['p/new']));
  await act(async () => { resolveOld(catalog('old')); });
  expect(result.current.modelOptions).toEqual(['p/new']);
  const calls = vi.mocked(api.sessionModels).mock.calls.length;
  act(() => oldRefresh());
  expect(api.sessionModels).toHaveBeenCalledTimes(calls);
  expect(readModelCatalog('opencode', '/a')).toBeUndefined();
});

it('keeps the newest refresh when responses complete out of order', async () => {
  let resolveOld!: (value: SessionModelsResponse) => void;
  vi.mocked(api.sessionModels).mockImplementationOnce(() => new Promise((resolve) => { resolveOld = resolve; }));
  vi.mocked(api.sessionModels).mockResolvedValue(catalog('fresh'));
  const { result } = renderHook(() => useModelCatalog('a', 'opencode', '/a', false));
  act(() => result.current.refreshModels());
  act(() => result.current.refreshModels());
  await waitFor(() => expect(result.current.modelOptions).toEqual(['p/fresh']));
  await act(async () => { resolveOld(catalog('old')); });
  expect(result.current.modelOptions).toEqual(['p/fresh']);
  expect(readModelCatalog('opencode', '/a')?.map((entry) => entry.model)).toEqual(['fresh']);
});

it('does not apply an older historical fallback after a successful refresh', async () => {
  let resolveHistory!: (models: { provider: string; model: string; count: number }[]) => void;
  getModels.mockImplementationOnce(() => new Promise((resolve) => { resolveHistory = resolve; }));
  vi.mocked(api.sessionModels).mockRejectedValueOnce(new Error('offline'));
  vi.mocked(api.sessionModels).mockResolvedValue(catalog('fresh'));
  const { result } = renderHook(() => useModelCatalog('a', 'opencode', '/a', false));
  act(() => result.current.refreshModels());
  await waitFor(() => expect(getModels).toHaveBeenCalledOnce());
  act(() => result.current.refreshModels());
  await waitFor(() => expect(result.current.modelOptions).toEqual(['p/fresh']));
  await act(async () => { resolveHistory([{ provider: 'p', model: 'old', count: 1 }]); });
  expect(result.current.modelOptions).toEqual(['p/fresh']);
});
