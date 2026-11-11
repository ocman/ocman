// @vitest-environment jsdom
import { act, renderHook, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { api, type PrepareSessionResponse } from '../../lib/api';
import { useDraftCatalog } from './useDraftCatalog';

vi.mock('../../lib/api', () => ({ api: { prepareSession: vi.fn() } }));
vi.mock('../../lib/projectSettingsCache', () => ({ loadProjectSettings: vi.fn(async () => ({ models: [], off: false })) }));

const selected: PrepareSessionResponse = {
  platform: 'opencode', agents: [], commands: [], liveConnection: true,
  models: { hasProviders: true, models: [{ provider: 'openai', model: 'gpt', platform: 'opencode' }] },
};
const full: PrepareSessionResponse = {
  ...selected, models: { hasProviders: true, models: [...selected.models.models, { provider: 'anthropic', model: 'sonnet', platform: 'claudecode' }] },
};

function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (reason: Error) => void;
  const promise = new Promise<T>((yes, no) => { resolve = yes; reject = no; });
  return { promise, resolve, reject };
}

beforeEach(() => { vi.clearAllMocks(); localStorage.clear(); });

describe('independent draft catalogs', () => {
  it('does not repeat catalog reads when the selected inventory is complete', async () => {
    vi.mocked(api.prepareSession).mockResolvedValue({ ...selected, additionalModels: false });
    const { result } = renderHook(() => useDraftCatalog({ directory: '/repo' }, 'key', 'request'));
    await waitFor(() => expect(result.current.catalogReady).toBe(true));
    expect(api.prepareSession).toHaveBeenCalledTimes(1);
  });
  it('opens submission on the selected catalog and enriches models later', async () => {
    const inventory = deferred<PrepareSessionResponse>();
    vi.mocked(api.prepareSession).mockImplementation((target) => target.selectedOnly ? Promise.resolve(selected) : inventory.promise);
    const { result } = renderHook(() => useDraftCatalog({ directory: '/repo', platform: 'opencode' }, 'key', 'request'));
    await waitFor(() => expect(result.current.catalogReady).toBe(true));
    expect(result.current.catalog?.models.models).toHaveLength(1);
    await act(async () => inventory.resolve(full));
    expect(result.current.catalog?.models.models).toHaveLength(2);
    expect(result.current.defaultCatalog?.models.models).toHaveLength(1);
    expect(result.current.catalogError).toBe('');
  });

  it('does not turn an optional failure into a submission failure', async () => {
    vi.mocked(api.prepareSession).mockImplementation((target) => target.selectedOnly ? Promise.resolve(selected) : Promise.reject(new Error('sibling offline')));
    const { result } = renderHook(() => useDraftCatalog({ directory: '/repo' }, 'key', 'request'));
    await waitFor(() => expect(result.current.catalogReady).toBe(true));
    expect(result.current.catalogError).toBe('');
    expect(result.current.catalog?.models.models).toHaveLength(1);
  });

  it('refuses inventory returned for another account even on the same route', async () => {
    vi.mocked(api.prepareSession).mockImplementation((target) => target.selectedOnly ? Promise.resolve(selected)
      : Promise.resolve({ ...full, platform: 'another-account' }));
    const { result } = renderHook(() => useDraftCatalog({ directory: '/repo' }, 'key', 'request'));
    await waitFor(() => expect(result.current.catalogReady).toBe(true));
    expect(result.current.catalog?.models.models).toHaveLength(1);
    expect(result.current.catalog?.platform).toBe('opencode');
  });

  it('cancels old inventory and refuses late results after changing owner', async () => {
    const old = deferred<PrepareSessionResponse>();
    const next = { ...selected, platform: 'r-box:opencode' };
    vi.mocked(api.prepareSession).mockImplementation((target) => target.remoteId === 'box' ? Promise.resolve(next)
      : target.selectedOnly ? Promise.resolve(selected) : old.promise);
    const { result, rerender } = renderHook(({ owner }) => useDraftCatalog({ directory: '/repo', remoteId: owner }, owner, owner), { initialProps: { owner: 'local' } });
    await waitFor(() => expect(result.current.catalogReady).toBe(true));
    const signal = vi.mocked(api.prepareSession).mock.calls[1][1];
    rerender({ owner: 'box' });
    await waitFor(() => expect(result.current.catalog?.platform).toBe('r-box:opencode'));
    await act(async () => old.resolve(full));
    expect(signal?.aborted).toBe(true);
    expect(result.current.catalog?.platform).toBe('r-box:opencode');
    expect(result.current.catalog?.models.models).toHaveLength(1);
  });

  it('keeps required failures visible and permits an explicit retry', async () => {
    vi.mocked(api.prepareSession).mockRejectedValue(new Error('selected offline'));
    const { result, rerender } = renderHook(({ request }) => useDraftCatalog({ directory: '/repo' }, 'key', request), { initialProps: { request: 'first' } });
    await waitFor(() => expect(result.current.catalogError).toBe('selected offline'));
    expect(result.current.catalogReady).toBe(false);
    vi.mocked(api.prepareSession).mockResolvedValue(selected);
    rerender({ request: 'retry' });
    await waitFor(() => expect(result.current.catalogReady).toBe(true));
    expect(result.current.catalogError).toBe('');
  });
});
