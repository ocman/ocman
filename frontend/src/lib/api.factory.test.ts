import { afterEach, describe, expect, it, vi } from 'vitest';
import { api } from './api';

afterEach(() => vi.unstubAllGlobals());

describe('Factory API', () => {
  it('uses the native epic graph endpoints', async () => {
    const fetchMock = vi.fn().mockImplementation(() => new Response(JSON.stringify([]), { status: 200 }));
    vi.stubGlobal('fetch', fetchMock);

    await api.factoryEpics();
    await api.factoryEpic('epic/1');
    await api.createFactoryEpic({ instantiationId: 'request-1', goal: 'Ship it', initialProject: '/repo', acknowledgeLocalExecution: true });
    await api.pourFactoryEpic('epic/1');
		await api.factoryIssues('epic/1');
		await api.factoryIssueComments('epic/1', 'issue/1');
		await api.addFactoryIssueComment('epic/1', 'issue/1', 'Reviewed');
		await api.factoryClaimPlan('epic/1', 'issue/1');
		await api.factoryMaterialize('epic/1', 'issue/2');
		await api.resolveFactoryRecoveryGate('gate/1', 'resume', 'Use A');
		await api.resolveFactoryAuthorityGate('gate/1', 'approve');
    await api.factoryProposals('epic/1');

    expect(fetchMock.mock.calls.map(([url]) => url)).toEqual([
      '/api/factory/epics',
      '/api/factory/epics/epic%2F1',
      '/api/factory/epics',
       '/api/factory/epics/epic%2F1/pour',
			 '/api/factory/epics/epic%2F1/issues',
			'/api/factory/epics/epic%2F1/issues/issue%2F1/comments',
			'/api/factory/epics/epic%2F1/issues/issue%2F1/comments',
			'/api/factory/epics/epic%2F1/plans/issue%2F1',
			'/api/factory/epics/epic%2F1/materializations/issue%2F2',
			'/api/factory/recovery-gates/gate%2F1/resume',
			'/api/factory/authority-gates/gate%2F1/approve',
      '/api/factory/epics/epic%2F1/proposals',
    ]);
    expect(fetchMock.mock.calls[2][1]).toMatchObject({ method: 'POST' });
		expect(fetchMock.mock.calls[3][1]).toMatchObject({ method: 'POST', body: undefined });
		expect(fetchMock.mock.calls[6][1]).toMatchObject({ method: 'POST', body: JSON.stringify({ body: 'Reviewed' }) });
  });
});
