// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { act, renderHook, waitFor } from '@testing-library/react';
import type { ReactNode } from 'react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { api } from './api';
import { useAddFactoryIssueComment, useDecideFactoryPlanGate, useFactoryGraphIssues, useFactoryIssueComments, useFactoryIssues, useFactoryProposals, useFactoryQueue, useResolveFactoryAuthorityGate, useSetFactoryCapacityPolicy, useWorkEpic, useWorkEpics } from './queries';

vi.mock('./api', () => ({ api: {
  factoryEpic: vi.fn(),
  factoryEpics: vi.fn(),
  factoryIssues: vi.fn(),
  factoryIssueComments: vi.fn(),
  addFactoryIssueComment: vi.fn(),
  factoryQueue: vi.fn(),
  setFactoryCapacityPolicy: vi.fn(),
  factoryProposals: vi.fn(),
  factoryPlanGate: vi.fn(),
  resolveFactoryAuthorityGate: vi.fn(),
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
      expect(options?.refetchInterval).toBe(15_000);
    }
  });

  it('refreshes Epics and the queue after mutations', async () => {
    vi.mocked(api.factoryPlanGate).mockResolvedValue({} as never);
    vi.mocked(api.resolveFactoryAuthorityGate).mockResolvedValue({} as never);
    const { client, wrapper } = setup();
    const invalidate = vi.spyOn(client, 'invalidateQueries');
    const gate = renderHook(() => useDecideFactoryPlanGate('epic-1'), { wrapper });
    const authority = renderHook(() => useResolveFactoryAuthorityGate(), { wrapper });
    await act(() => gate.result.current.mutateAsync({ action: 'approve', expectedRevision: 1, expectedHash: 'hash' }));
    await act(() => authority.result.current.mutateAsync({ id: 'gate-1', action: 'approve' }));
    expect(invalidate).toHaveBeenCalledTimes(4);
    expect(invalidate).toHaveBeenCalledWith({ queryKey: ['factory-epics'] });
    expect(invalidate).toHaveBeenCalledWith({ queryKey: ['factory-queue'] });
  });

  it('uses 15-second refreshes for lists, graph issues, comments, and queue', async () => {
    vi.mocked(api.factoryEpics).mockResolvedValue([]);
    vi.mocked(api.factoryIssues).mockResolvedValue([]);
    vi.mocked(api.factoryIssueComments).mockResolvedValue([]);
    vi.mocked(api.factoryQueue).mockResolvedValue([]);
    const { client, wrapper } = setup();
    const epics = renderHook(() => useWorkEpics(), { wrapper });
    const graph = renderHook(() => useFactoryGraphIssues([{ id: 'epic-1' } as never]), { wrapper });
    const comments = renderHook(() => useFactoryIssueComments('epic-1', 'issue-1'), { wrapper });
    const queue = renderHook(() => useFactoryQueue(), { wrapper });
    await waitFor(() => expect(epics.result.current.isSuccess && graph.result.current[0].isSuccess && comments.result.current.isSuccess && queue.result.current.isSuccess).toBe(true));
    for (const key of [['factory-epics'], ['factory-epics', 'epic-1', 'issues'], ['factory-issue-comments', 'epic-1', 'issue-1'], ['factory-queue']]) {
      const options = client.getQueryCache().find({ queryKey: key })?.options as { refetchInterval?: number } | undefined;
      expect(options?.refetchInterval).toBe(15_000);
    }
    [epics, graph, comments, queue].forEach((hook) => hook.unmount());
    client.clear();
  });

  it('keeps comment and capacity caches current after extraction', async () => {
    const { client, wrapper } = setup();
    const comment = { id: 'comment', body: 'Review' } as never;
    vi.mocked(api.addFactoryIssueComment).mockResolvedValue(comment);
    const comments = renderHook(() => useAddFactoryIssueComment('epic', 'issue'), { wrapper });
    await act(() => comments.result.current.mutateAsync('Review'));
    await act(() => comments.result.current.mutateAsync('Review'));
    expect(client.getQueryData(['factory-issue-comments', 'epic', 'issue'])).toEqual([comment, comment]);
    const policy = { globalCapacity: 2, projectCapacity: 1, projectOverrides: {} };
    vi.mocked(api.setFactoryCapacityPolicy).mockResolvedValue(policy);
    const capacity = renderHook(() => useSetFactoryCapacityPolicy(), { wrapper });
    await act(() => capacity.result.current.mutateAsync(policy));
    expect(client.getQueryData(['factory-capacity-policy'])).toEqual(policy);
    comments.unmount();
    capacity.unmount();
    client.clear();
  });
});
