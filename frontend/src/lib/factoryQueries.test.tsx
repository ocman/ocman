// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { act, renderHook, waitFor } from '@testing-library/react';
import type { ReactNode } from 'react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { api } from './api';
import { useDecideFactoryPlanGate, useFactoryIssues, useFactoryProposals, usePourFactoryEpic, useResolveFactoryAuthorityGate, useWorkEpic } from './queries';

vi.mock('./api', () => ({ api: {
  factoryEpic: vi.fn(),
  factoryIssues: vi.fn(),
  factoryProposals: vi.fn(),
  factoryPlanGate: vi.fn(),
  resolveFactoryAuthorityGate: vi.fn(),
  pourFactoryEpic: vi.fn(),
} }));

function setup() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const wrapper = ({ children }: { children: ReactNode }) => <QueryClientProvider client={client}>{children}</QueryClientProvider>;
  return { client, wrapper };
}

describe('Factory query freshness', () => {
  beforeEach(() => vi.clearAllMocks());

  it('polls the live Epic detail, issues, and proposals', async () => {
    vi.mocked(api.factoryEpic).mockResolvedValue({ id: 'epic-1' } as never);
    vi.mocked(api.factoryIssues).mockResolvedValue([]);
    vi.mocked(api.factoryProposals).mockResolvedValue([]);
    const { client, wrapper } = setup();
    const hooks = [
      renderHook(() => useWorkEpic('epic-1'), { wrapper }),
      renderHook(() => useFactoryIssues('epic-1'), { wrapper }),
      renderHook(() => useFactoryProposals('epic-1'), { wrapper }),
    ];
    await waitFor(() => expect(hooks.every((hook) => hook.result.current.isSuccess)).toBe(true));
    for (const key of [['factory-epics', 'epic-1'], ['factory-epics', 'epic-1', 'issues'], ['factory-epics', 'epic-1', 'proposals']]) {
      const options = client.getQueryCache().find({ queryKey: key })?.options as { refetchInterval?: number } | undefined;
      expect(options?.refetchInterval).toBe(10_000);
    }
  });

  it('refreshes Epics and the queue after mutations', async () => {
    vi.mocked(api.factoryPlanGate).mockResolvedValue({} as never);
    vi.mocked(api.resolveFactoryAuthorityGate).mockResolvedValue({} as never);
    vi.mocked(api.pourFactoryEpic).mockResolvedValue([]);
    const { client, wrapper } = setup();
    const invalidate = vi.spyOn(client, 'invalidateQueries');
    const gate = renderHook(() => useDecideFactoryPlanGate('epic-1'), { wrapper });
    const authority = renderHook(() => useResolveFactoryAuthorityGate(), { wrapper });
    const pour = renderHook(() => usePourFactoryEpic('epic-1'), { wrapper });
    await act(() => gate.result.current.mutateAsync({ action: 'approve', expectedRevision: 1, expectedHash: 'hash' }));
    await act(() => authority.result.current.mutateAsync({ id: 'gate-1', action: 'approve' }));
    await act(() => pour.result.current.mutateAsync());
    expect(invalidate).toHaveBeenCalledTimes(6);
    expect(invalidate).toHaveBeenCalledWith({ queryKey: ['factory-epics'] });
    expect(invalidate).toHaveBeenCalledWith({ queryKey: ['factory-queue'] });
  });
});
