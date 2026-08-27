import { afterEach, describe, expect, it, vi } from 'vitest';
import { api } from './api';

afterEach(() => vi.unstubAllGlobals());

describe('Factory API', () => {
  it('lists, gets, and creates Work Epics', async () => {
    const epic = {
      id: 'epic-1', status: 'open', goal: 'Ship it', initialProject: '/repo',
      formulaId: 'ocman/default', formulaVersion: 1, instantiationId: 'request-1',
      planning: { workId: 'work-1', workStatus: 'open', approvalGateId: 'gate-1', approvalStatus: 'pending' },
    };
    const fetchMock = vi.fn()
      .mockResolvedValueOnce(new Response(JSON.stringify([epic]), { status: 200 }))
      .mockResolvedValueOnce(new Response(JSON.stringify(epic), { status: 200 }))
      .mockResolvedValueOnce(new Response(JSON.stringify(epic), { status: 201 }));
    vi.stubGlobal('fetch', fetchMock);

    await expect(api.factoryEpics()).resolves.toEqual([epic]);
    await expect(api.factoryEpic('epic/1')).resolves.toEqual(epic);
    await expect(api.createFactoryEpic({
      instantiationId: 'request-1', goal: 'Ship it', initialProject: '/repo', acknowledgeLocalExecution: true,
    })).resolves.toEqual(epic);

    expect(fetchMock.mock.calls[1][0]).toBe('/api/factory/epics/epic%2F1');
    expect(fetchMock.mock.calls[2][0]).toBe('/api/factory/epics');
    expect(fetchMock.mock.calls[2][1]).toMatchObject({
      method: 'POST',
      body: JSON.stringify({
        instantiationId: 'request-1', goal: 'Ship it', initialProject: '/repo', acknowledgeLocalExecution: true,
      }),
    });
  });

  it('supports the Formula library and browser-safe draft operations', async () => {
    const fetchMock = vi.fn()
      .mockResolvedValueOnce(new Response(JSON.stringify([]), { status: 200 }))
      .mockResolvedValueOnce(new Response(JSON.stringify({ definitionYaml: 'schema: 1' }), { status: 200 }))
      .mockResolvedValueOnce(new Response(JSON.stringify({ valid: false, errors: ['invalid'] }), { status: 200 }))
      .mockResolvedValueOnce(new Response(JSON.stringify({ name: 'Preview', nodes: [], edges: [] }), { status: 200 }))
      .mockResolvedValueOnce(new Response(JSON.stringify({ id: 'custom/team', revision: 1 }), { status: 201 }))
      .mockResolvedValueOnce(new Response(null, { status: 204 }))
      .mockResolvedValueOnce(new Response(null, { status: 204 }));
    vi.stubGlobal('fetch', fetchMock);

    await api.factoryFormulas();
    await api.copyFactoryFormula('ocman/default', 1);
    await api.validateFactoryFormula('bad yaml');
    await api.previewFactoryFormula('schema: 1', { goal: 'Ship', initial_project: '/repo' });
    await api.saveFactoryFormula({ id: 'custom/team', name: 'Team', definitionYaml: 'schema: 1' });
    await api.archiveFactoryFormula('custom/team');
    await api.deleteFactoryFormula('custom/team');

    expect(fetchMock.mock.calls.map(([url]) => url)).toEqual([
      '/api/factory/formulas', '/api/factory/formulas/copy', '/api/factory/formulas/validate',
      '/api/factory/formulas/preview', '/api/factory/formulas', '/api/factory/formulas/archive',
      '/api/factory/formulas/delete',
    ]);
    expect(fetchMock.mock.calls[2][1]).toMatchObject({ body: JSON.stringify({ definitionYaml: 'bad yaml' }) });
  });
});
