import { afterEach, describe, expect, it, vi } from 'vitest';
import { api } from './api';

afterEach(() => vi.unstubAllGlobals());

describe('Factory API', () => {
	it('returns the authoritative Plan from a rejected CAS mutation', async () => {
		const plan = { revision: 5, hash: 'hash-5', state: 'draft', graph: { intent: 'Current', targets: [], items: [], dependencies: [] }, planning: [], validation: [] };
		vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(JSON.stringify({ stale: true, plan }), { status: 409, headers: { 'Content-Type': 'application/json' } })));

		await expect(api.mutateFactoryPlan('epic-1', 4, plan.graph)).resolves.toEqual({ stale: true, plan });
	});

	it('preserves non-CAS mutation errors', async () => {
		vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response('invalid Plan', { status: 400 })));
		await expect(api.mutateFactoryPlan('epic-1', 4, { intent: 'Invalid', targets: [], items: [], dependencies: [] })).rejects.toThrow('invalid Plan');
	});

  it('lists, gets, creates, and revision-checks Work Epics', async () => {
    const epic = {
      id: 'epic-1', status: 'open', goal: 'Ship it', initialProject: '/repo',
      formulaId: 'ocman/default', formulaVersion: 1, instantiationId: 'request-1',
      planning: { workId: 'work-1', workStatus: 'open', approvalGateId: 'gate-1', approvalStatus: 'pending' },
		plan: { revision: 2, hash: 'hash-2', state: 'draft', graph: { intent: 'Ship it', targets: [], items: [], dependencies: [] }, planning: [], validation: ['incomplete'] },
    };
    const fetchMock = vi.fn()
      .mockResolvedValueOnce(new Response(JSON.stringify([epic]), { status: 200 }))
      .mockResolvedValueOnce(new Response(JSON.stringify(epic), { status: 200 }))
		.mockResolvedValueOnce(new Response(JSON.stringify(epic), { status: 201 }))
		.mockResolvedValueOnce(new Response(JSON.stringify({ stale: false, plan: epic.plan }), { status: 200 }))
		.mockResolvedValueOnce(new Response(JSON.stringify({ ...epic.plan, state: 'approved' }), { status: 200 }))
		.mockResolvedValueOnce(new Response(JSON.stringify(epic.plan), { status: 200 }));
    vi.stubGlobal('fetch', fetchMock);

    await expect(api.factoryEpics()).resolves.toEqual([epic]);
    await expect(api.factoryEpic('epic/1')).resolves.toEqual(epic);
    await expect(api.createFactoryEpic({
      instantiationId: 'request-1', goal: 'Ship it', initialProject: '/repo', acknowledgeLocalExecution: true,
    })).resolves.toEqual(epic);
	await expect(api.mutateFactoryPlan('epic-1', 2, epic.plan.graph)).resolves.toEqual({ stale: false, plan: epic.plan });
	await expect(api.decideFactoryPlan('epic-1', 'approve', { expectedRevision: 2, expectedHash: 'hash-2', actor: 'operator' })).resolves.toMatchObject({ state: 'approved' });
	await expect(api.completeFactoryPlanningWork('epic-1', 'work/1', 2, 'hash-2')).resolves.toEqual(epic.plan);

    expect(fetchMock.mock.calls[1][0]).toBe('/api/factory/epics/epic%2F1');
    expect(fetchMock.mock.calls[2][0]).toBe('/api/factory/epics');
    expect(fetchMock.mock.calls[2][1]).toMatchObject({
      method: 'POST',
      body: JSON.stringify({
        instantiationId: 'request-1', goal: 'Ship it', initialProject: '/repo', acknowledgeLocalExecution: true,
      }),
    });
	expect(fetchMock.mock.calls[3][1]).toMatchObject({ method: 'POST', body: JSON.stringify({ expectedRevision: 2, graph: epic.plan.graph }) });
	expect(fetchMock.mock.calls[4][0]).toBe('/api/factory/epics/epic-1/plan/approve');
	expect(fetchMock.mock.calls[5][0]).toBe('/api/factory/epics/epic-1/planning/work%2F1/complete');
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
