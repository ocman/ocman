// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { act, renderHook, waitFor } from '@testing-library/react';
import type { ReactNode } from 'react';
import { describe, expect, it, vi } from 'vitest';
import { api, type WorkEpic } from './api';
import { useCreateWorkEpic, useFactoryFormulaActions, useFactoryFormulas, useWorkEpic, useWorkEpics } from './queries';

vi.mock('./api', () => ({ api: {
  createFactoryEpic: vi.fn(),
  factoryEpic: vi.fn(),
  factoryEpics: vi.fn(),
  factoryFormulas: vi.fn(),
  copyFactoryFormula: vi.fn(),
  validateFactoryFormula: vi.fn(),
  previewFactoryFormula: vi.fn(),
  saveFactoryFormula: vi.fn(),
  archiveFactoryFormula: vi.fn(),
  deleteFactoryFormula: vi.fn(),
} }));

function setup() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const wrapper = ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={client}>{children}</QueryClientProvider>
  );
  return { client, wrapper };
}

describe('Factory queries', () => {
  it('loads the epic list and an encoded epic detail through the API abstraction', async () => {
    vi.mocked(api.factoryEpics).mockResolvedValue([]);
    vi.mocked(api.factoryEpic).mockResolvedValue({ id: 'epic-1' } as WorkEpic);
    const { client, wrapper } = setup();

    const list = renderHook(() => useWorkEpics(), { wrapper });
    const detail = renderHook(() => useWorkEpic('epic-1'), { wrapper });
    await waitFor(() => expect(list.result.current.isSuccess).toBe(true));
    await waitFor(() => expect(detail.result.current.isSuccess).toBe(true));

    expect(api.factoryEpics).toHaveBeenCalledWith(expect.any(AbortSignal));
    expect(api.factoryEpic).toHaveBeenCalledWith('epic-1', expect.any(AbortSignal));
    const options = client.getQueryCache().find({ queryKey: ['factory-epics'] })?.options as
      | { refetchInterval?: number }
      | undefined;
    expect(options?.refetchInterval).toBe(10_000);
  });
});

it('loads Formulas and delegates editor actions with library refreshes', async () => {
  vi.mocked(api.factoryFormulas).mockResolvedValue([]);
  vi.mocked(api.copyFactoryFormula).mockResolvedValue({ definitionYaml: 'schema: 1' } as never);
  vi.mocked(api.validateFactoryFormula).mockResolvedValue({ valid: true, errors: [] } as never);
  vi.mocked(api.previewFactoryFormula).mockResolvedValue({ nodes: [], edges: [] } as never);
  vi.mocked(api.saveFactoryFormula).mockResolvedValue({ id: 'custom/team', revision: 1 } as never);
  vi.mocked(api.archiveFactoryFormula).mockResolvedValue(undefined);
  vi.mocked(api.deleteFactoryFormula).mockResolvedValue(undefined);
  const { client, wrapper } = setup();
  const invalidate = vi.spyOn(client, 'invalidateQueries');
  const list = renderHook(() => useFactoryFormulas(), { wrapper });
  const actions = renderHook(() => useFactoryFormulaActions(), { wrapper });
  await waitFor(() => expect(list.result.current.isSuccess).toBe(true));

  await act(() => actions.result.current.copy.mutateAsync({ id: 'ocman/default', revision: 1 }));
  await act(() => actions.result.current.validate.mutateAsync('schema: 1'));
  await act(() => actions.result.current.preview.mutateAsync({ definitionYaml: 'schema: 1', parameters: { goal: 'Ship' } }));
  await act(() => actions.result.current.save.mutateAsync({ id: 'custom/team', name: 'Team', definitionYaml: 'schema: 1' }));
  await act(() => actions.result.current.archive.mutateAsync('custom/team'));
  await act(() => actions.result.current.remove.mutateAsync('custom/team'));

  expect(api.factoryFormulas).toHaveBeenCalledWith(expect.any(AbortSignal));
  expect(api.copyFactoryFormula).toHaveBeenCalledWith('ocman/default', 1);
  expect(invalidate).toHaveBeenCalledWith({ queryKey: ['factory-formulas'] });
});

it('adds a created epic and invalidates Factory status and epics', async () => {
  const epic = {
    id: 'epic-1', status: 'open', goal: 'Ship it', initialProject: '/repo',
    formulaId: 'ocman/default', formulaVersion: 1, instantiationId: 'request-1',
    formulaRevision: 1, formulaHash: 'hash', formulaOrigin: 'built-in',
    planning: { workId: 'work-1', workStatus: 'open', approvalGateId: 'gate-1', approvalStatus: 'pending' },
  } satisfies WorkEpic;
  vi.mocked(api.createFactoryEpic).mockResolvedValue(epic);
  const { client, wrapper } = setup();
  client.setQueryData<WorkEpic[]>(['factory-epics'], []);
  const invalidate = vi.spyOn(client, 'invalidateQueries');
  const { result } = renderHook(() => useCreateWorkEpic(), { wrapper });

  await act(() => result.current.mutateAsync({
    instantiationId: 'request-1', goal: 'Ship it', initialProject: '/repo', acknowledgeLocalExecution: true,
  }));

  expect(client.getQueryData<WorkEpic[]>(['factory-epics'])).toEqual([epic]);
  expect(invalidate).toHaveBeenCalledWith({ queryKey: ['factory-status'] });
  expect(invalidate).toHaveBeenCalledWith({ queryKey: ['factory-epics'] });
});
