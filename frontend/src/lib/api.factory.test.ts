import { afterEach, describe, expect, it, vi } from 'vitest';
import { api } from './api';

afterEach(() => vi.unstubAllGlobals());

describe('Factory API', () => {
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
		.mockResolvedValueOnce(new Response(JSON.stringify({ ...epic.plan, state: 'approved' }), { status: 200 }));
    vi.stubGlobal('fetch', fetchMock);

    await expect(api.factoryEpics()).resolves.toEqual([epic]);
    await expect(api.factoryEpic('epic/1')).resolves.toEqual(epic);
    await expect(api.createFactoryEpic({
      instantiationId: 'request-1', goal: 'Ship it', initialProject: '/repo', acknowledgeLocalExecution: true,
    })).resolves.toEqual(epic);
	await expect(api.mutateFactoryPlan('epic-1', 2, epic.plan.graph)).resolves.toEqual({ stale: false, plan: epic.plan });
	await expect(api.decideFactoryPlan('epic-1', 'approve', { expectedRevision: 2, expectedHash: 'hash-2', actor: 'operator' })).resolves.toMatchObject({ state: 'approved' });

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
  });
});
