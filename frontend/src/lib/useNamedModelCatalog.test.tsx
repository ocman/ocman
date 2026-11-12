// @vitest-environment jsdom
import { beforeEach, expect, it, vi } from 'vitest';
import { renderHook, waitFor } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import type { ReactNode } from 'react';
import { api } from './api';
import { useMachinePlatforms } from './useCapabilities';
import { useNamedModelCatalog } from './useNamedModelCatalog';
import { queryClient } from './queryClient';

vi.mock('./api', () => ({ api: { prepareSession: vi.fn() } }));
vi.mock('./useCapabilities', () => ({ useMachinePlatforms: vi.fn() }));

beforeEach(() => { vi.resetAllMocks(); queryClient.clear(); });

function catalog() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const wrapper = ({ children }: { children: ReactNode }) => <QueryClientProvider client={client}>{children}</QueryClientProvider>;
  return renderHook(() => useNamedModelCatalog('/repo', 'box', [{ provider: 'p', model: 'legacy' }]), { wrapper });
}

it('loads each named account without merging identical model IDs or using a native-kind fallback', async () => {
  vi.mocked(useMachinePlatforms).mockReturnValue([
    { id: 'r-box:cnx_work', kind: 'opencode', displayName: 'Work', available: true, capabilities: { modelCatalog: true } },
    { id: 'r-box:cnx_home', kind: 'opencode', displayName: 'Home', available: true, capabilities: { modelCatalog: true } },
    { id: 'r-box:cnx_off', kind: 'opencode', available: false, capabilities: { modelCatalog: true } },
  ] as never);
  vi.mocked(api.prepareSession).mockResolvedValue({ models: { hasProviders: true, models: [
    { platform: 'opencode', provider: 'p', model: 'same' },
    { provider: 'p', model: 'hidden', hidden: true },
    { provider: 'p', model: 'offline', isAvailable: false },
  ] } } as never);
  const { result } = catalog();
  await waitFor(() => expect(result.current.loading).toBe(false));
  expect(result.current.entries.map(({ platform, platformName, model }) => ({ platform, platformName, model }))).toEqual([
    { platform: 'cnx_work', platformName: 'Work', model: 'same' },
    { platform: 'cnx_home', platformName: 'Home', model: 'same' },
  ]);
  expect(api.prepareSession).toHaveBeenCalledTimes(2);
  expect(api.prepareSession).toHaveBeenCalledWith({ directory: '/repo', remoteId: 'box', platform: 'r-box:cnx_work', selectedOnly: true }, expect.any(AbortSignal));
});

it('keeps legacy catalogs when the owner has no named platforms', () => {
  vi.mocked(useMachinePlatforms).mockReturnValue([]);
  expect(catalog().result.current.entries).toEqual([{ provider: 'p', model: 'legacy' }]);
  expect(api.prepareSession).not.toHaveBeenCalled();
});

it('reports a failed named catalog instead of offering another native account implicitly', async () => {
  vi.mocked(useMachinePlatforms).mockReturnValue([{ id: 'cnx_work', kind: 'opencode', available: true, capabilities: { modelCatalog: true } }] as never);
  vi.mocked(api.prepareSession).mockRejectedValue(new Error('offline'));
  const { result } = catalog();
  await waitFor(() => expect(result.current.error).toBe(true));
  expect(result.current.entries).toEqual([]);
});
