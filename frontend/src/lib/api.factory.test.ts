import { afterEach, describe, expect, it, vi } from 'vitest';
import { api } from './api';

afterEach(() => vi.unstubAllGlobals());

describe('Factory API', () => {
	it('sends execution consent only as explicit confirmation input', async () => {
		const fetchMock = vi.fn().mockResolvedValue(new Response('{}', { status: 200 }));
		vi.stubGlobal('fetch', fetchMock);
		for (const consent of [undefined, false, true]) {
			fetchMock.mockResolvedValueOnce(new Response('{}', { status: 200 }));
			await api.decideFactoryControlAction('epic/1', 'action/1', 'confirm', consent);
			expect(fetchMock).toHaveBeenLastCalledWith('/api/factory/epics/epic%2F1/control-actions/action%2F1/confirm', expect.objectContaining({ method: 'POST', body: consent === undefined ? undefined : JSON.stringify({ acknowledgeLocalExecution: consent }) }));
		}
	});
  it('uses the native epic graph endpoints', async () => {
    const fetchMock = vi.fn().mockImplementation(() => new Response(JSON.stringify([]), { status: 200 }));
    vi.stubGlobal('fetch', fetchMock);

    await api.factoryEpics();
    await api.factoryEpic('epic/1');
    await api.createFactoryEpic({ instantiationId: 'request-1', goal: 'Ship it', initialProject: '/repo', acknowledgeLocalExecution: true });
		await api.factoryIssues('epic/1');
		await api.factoryIssueComments('epic/1', 'issue/1');
		await api.addFactoryIssueComment('epic/1', 'issue/1', 'Reviewed');
		await api.factoryClaimPlan('epic/1', 'issue/1');
		await api.factoryMaterialize('epic/1', 'issue/2');
		await api.resolveFactoryRecoveryGate('gate/1', 'resume', 'Use A');
		await api.resolveFactoryAuthorityGate('gate/1', 'approve');
		await api.resolveFactoryProjectGate('gate/1', 'approve', '', true);
    await api.factoryProposals('epic/1');

    expect(fetchMock.mock.calls.map(([url]) => url)).toEqual([
      '/api/factory/epics',
      '/api/factory/epics/epic%2F1',
      '/api/factory/epics',
			 '/api/factory/epics/epic%2F1/issues',
			'/api/factory/epics/epic%2F1/issues/issue%2F1/comments',
			'/api/factory/epics/epic%2F1/issues/issue%2F1/comments',
			'/api/factory/epics/epic%2F1/plans/issue%2F1',
			'/api/factory/epics/epic%2F1/materializations/issue%2F2',
			'/api/factory/recovery-gates/gate%2F1/resume',
			'/api/factory/authority-gates/gate%2F1/approve',
			'/api/factory/project-gates/gate%2F1/approve',
      '/api/factory/epics/epic%2F1/proposals',
    ]);
    expect(fetchMock.mock.calls[2][1]).toMatchObject({ method: 'POST' });
		expect(fetchMock.mock.calls[5][1]).toMatchObject({ method: 'POST', body: JSON.stringify({ body: 'Reviewed' }) });
  });
});
