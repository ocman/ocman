// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import type { ReactNode } from 'react';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { api } from '../lib/api';
import { FactoryConfiguration, FactoryEpicDetail, FactoryEpics, FactoryOverview, FactoryQueue } from './Factory';

vi.mock('../lib/api', () => ({ api: {
    factoryEpics: vi.fn(),
    projects: vi.fn(),
    sessions: vi.fn(),
  factoryEpic: vi.fn(),
   createFactoryEpic: vi.fn(),
   pourFactoryEpic: vi.fn(),
		factoryClaimPlan: vi.fn(),
		factoryMaterialize: vi.fn(),
		reopenFactoryIssue: vi.fn(),
		factoryIssues: vi.fn(),
		factoryIssueComments: vi.fn(),
		addFactoryIssueComment: vi.fn(),
    factoryRemovedIssues: vi.fn(),
    mutateFactoryGraph: vi.fn(),
		factoryQueue: vi.fn(),
		resolveFactoryRecoveryGate: vi.fn(),
		resolveFactoryAuthorityGate: vi.fn(),
    factoryProposals: vi.fn(),
    factoryPlanGate: vi.fn(),
		factoryCloseMol: vi.fn(),
		factoryCloseEpic: vi.fn(),
   factoryFormula: vi.fn(),
   factoryFormulas: vi.fn(),
   validateFactoryFormula: vi.fn(),
    previewFactoryFormula: vi.fn(),
    saveFactoryFormula: vi.fn(),
    factoryCapacityPolicy: vi.fn(),
    setFactoryCapacityPolicy: vi.fn(),
} }));

function renderFactory(children: ReactNode) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(<QueryClientProvider client={client}>{children}</QueryClientProvider>);
}

async function fillEpicForm(user: ReturnType<typeof userEvent.setup>) {
  await user.click(screen.getByRole('button', { name: 'New epic' }));
  await user.type(screen.getByLabelText('Goal'), ' Ship Factory ');
  await user.type(screen.getByLabelText('Brief'), ' Native graph ');
  await user.click(screen.getByRole('combobox', { name: 'Initial Factory project' }));
  await user.click(screen.getByRole('option', { name: '/repo' }));
	await user.click(screen.getByRole('checkbox', { name: 'Allow Factory agents to run commands in this repository' }));
}

beforeEach(() => {
  vi.mocked(api.factoryEpic).mockReset();
  vi.mocked(api.projects).mockReset();
  vi.mocked(api.createFactoryEpic).mockReset();
   vi.mocked(api.pourFactoryEpic).mockReset();
		vi.mocked(api.factoryClaimPlan).mockReset();
		vi.mocked(api.factoryMaterialize).mockReset();
		vi.mocked(api.reopenFactoryIssue).mockReset();
	vi.mocked(api.factoryIssues).mockReset();
	vi.mocked(api.factoryIssueComments).mockReset();
	vi.mocked(api.addFactoryIssueComment).mockReset();
  vi.mocked(api.factoryRemovedIssues).mockReset();
  vi.mocked(api.mutateFactoryGraph).mockReset();
	vi.mocked(api.factoryQueue).mockReset();
	vi.mocked(api.resolveFactoryRecoveryGate).mockReset();
	vi.mocked(api.resolveFactoryAuthorityGate).mockReset();
  vi.mocked(api.factoryProposals).mockReset();
	vi.mocked(api.factoryPlanGate).mockReset();
		vi.mocked(api.factoryCloseMol).mockReset();
		vi.mocked(api.factoryCloseEpic).mockReset();
  vi.mocked(api.factoryFormulas).mockReset();
  vi.mocked(api.validateFactoryFormula).mockReset();
  vi.mocked(api.previewFactoryFormula).mockReset();
  vi.mocked(api.saveFactoryFormula).mockReset();
  vi.mocked(api.factoryCapacityPolicy).mockReset();
  vi.mocked(api.setFactoryCapacityPolicy).mockReset();
	vi.mocked(api.factoryEpics).mockResolvedValue([]);
	vi.mocked(api.factoryQueue).mockResolvedValue([]);
	vi.mocked(api.factoryIssueComments).mockResolvedValue([]);
	vi.mocked(api.sessions).mockResolvedValue([]);
  vi.mocked(api.projects).mockResolvedValue([{ directory: '/repo', sessionCount: 1, messageCount: 1, totalTokensIn: 0, totalTokensOut: 0, lastUsed: 0 }]);
		vi.mocked(api.factoryCloseMol).mockResolvedValue(undefined);
	vi.mocked(api.factoryCloseEpic).mockResolvedValue(undefined);
	vi.mocked(api.mutateFactoryGraph).mockResolvedValue(undefined);
	vi.mocked(api.factoryRemovedIssues).mockResolvedValue([]);
		vi.mocked(api.factoryClaimPlan).mockResolvedValue({} as never);
		vi.mocked(api.factoryMaterialize).mockResolvedValue({} as never);
		vi.mocked(api.reopenFactoryIssue).mockResolvedValue(undefined);
	vi.mocked(api.factoryProposals).mockResolvedValue([]);
	vi.mocked(api.resolveFactoryRecoveryGate).mockResolvedValue({ resolution: 'resume' } as never);
	vi.mocked(api.resolveFactoryAuthorityGate).mockResolvedValue({ resolution: 'approve' } as never);
	vi.mocked(api.factoryFormula).mockResolvedValue({ id: 'ocman/tracer', version: 1, name: 'Tracer', source: 'name = "Tracer"\n', hash: 'hash', sourceHash: 'source-hash', inputs: ['goal'], nodes: [{ key: 'plan', kind: 'plan' }], edges: [{ from: 'approval', to: 'plan' }], valid: true });
	vi.mocked(api.factoryFormulas).mockResolvedValue([{ id: 'ocman/tracer', version: 1, name: 'Tracer', source: 'name = "Tracer"\n', hash: 'hash', sourceHash: 'source-hash', inputs: ['goal'], nodes: [{ key: 'plan', kind: 'plan' }], edges: [], valid: true }]);
	vi.mocked(api.factoryCapacityPolicy).mockResolvedValue({ globalCapacity: 10, projectCapacity: 4, projectOverrides: { '/repo': 2 } });
});

afterEach(() => vi.restoreAllMocks());

describe('Factory interactions', () => {
  it('creates an epic and clears the form after success', async () => {
    const user = userEvent.setup();
    vi.mocked(api.createFactoryEpic).mockResolvedValue({ id: 'epic-1' } as never);
    renderFactory(<MemoryRouter><FactoryEpics /></MemoryRouter>);
    expect(screen.queryByLabelText('Goal')).not.toBeInTheDocument();
    await fillEpicForm(user);

    await user.click(screen.getByRole('button', { name: 'Create epic' }));

    await waitFor(() => expect(api.createFactoryEpic).toHaveBeenCalledWith({
      instantiationId: expect.any(String), goal: 'Ship Factory', brief: 'Native graph', initialProject: '/repo', acknowledgeLocalExecution: true,
    }));
    await waitFor(() => expect(screen.queryByRole('dialog', { name: 'Create epic' })).not.toBeInTheDocument());
  });

  it('requires a selected Factory project and submits its canonical path', async () => {
    const user = userEvent.setup();
    renderFactory(<MemoryRouter><FactoryEpics /></MemoryRouter>);
    await user.click(screen.getByRole('button', { name: 'New epic' }));
    await user.type(screen.getByLabelText('Goal'), 'Ship Factory');
    await user.click(screen.getByRole('button', { name: 'Create epic' }));
    expect(screen.getByRole('alert')).toHaveTextContent('Select an initial Factory project.');

    await user.click(screen.getByRole('combobox', { name: 'Initial Factory project' }));
    await user.click(screen.getByRole('option', { name: '/repo' }));
    expect(screen.queryByRole('alert')).not.toBeInTheDocument();
		await user.click(screen.getByRole('button', { name: 'Create epic' }));
		expect(screen.getByRole('alert')).toHaveTextContent('Acknowledge local command execution.');
		expect(api.createFactoryEpic).not.toHaveBeenCalled();
  });

  it('filters the epic inventory with its labelled toolbar', async () => {
    const user = userEvent.setup();
    vi.mocked(api.factoryEpics).mockResolvedValue([
      { id: 'epic-1', goal: 'Ship Factory', status: 'open', initialProject: '/repo' },
      { id: 'epic-2', goal: 'Refresh docs', status: 'open', initialProject: '/docs' },
    ] as never);
    renderFactory(<MemoryRouter><FactoryEpics /></MemoryRouter>);
    expect(await screen.findByRole('link', { name: 'Ship Factory' })).toBeInTheDocument();
		const table = screen.getByRole('table', { name: 'Epics' });
		expect(within(table).getAllByRole('columnheader').map((cell) => cell.textContent)).toEqual(['Goal', 'Project', 'Status', 'Progress']);
		expect(within(table).getByRole('cell', { name: '/repo' })).toBeInTheDocument();

    await user.type(screen.getByLabelText('Find epics'), 'docs');
    expect(await screen.findByRole('link', { name: 'Refresh docs' })).toBeInTheDocument();
    expect(screen.queryByRole('link', { name: 'Ship Factory' })).not.toBeInTheDocument();
  });

  it('lists every actionable gate and live prompt in the action inbox', async () => {
    const user = userEvent.setup();
    vi.mocked(api.factoryEpics).mockResolvedValue([
      { id: 'epic-1', goal: 'Ship Factory', status: 'open', initialProject: '/repo', planGate: { issueId: 'epic-1.2', proposalRevision: 3, proposalHash: 'abc', resolution: 'open' } },
      { id: 'epic-2', goal: 'Refresh docs', status: 'open', initialProject: '/docs', attempts: [{ id: 'plan-attempt', workId: 'epic-2.1', phase: 'active', session: { platform: 'opencode', id: 'plan-session' } }] },
    ] as never);
    vi.mocked(api.factoryIssues).mockImplementation((id) => Promise.resolve(id === 'epic-1' ? [
      { id: 'epic-1.5', epicId: 'epic-1', kind: 'gate', title: 'Recovery', status: 'open', recovery: { issueId: 'epic-1.5', epicId: 'epic-1', attemptId: 'a1', workId: 'epic-1.4', question: 'Tests fail. Continue?', reason: 'vitest exited 1', choices: ['Skip tests', 'Fix tests'], resolution: 'open' } },
      { id: 'epic-1.6', epicId: 'epic-1', kind: 'gate', title: 'Authority', status: 'open', authority: { issueId: 'epic-1.6', epicId: 'epic-1', attemptId: 'a1', workId: 'epic-1.4', requestId: 'req', permission: 'bash', target: 'rm -rf dist', resolution: 'open' } },
	  { id: 'epic-1.8', epicId: 'epic-1', kind: 'gate', title: 'Pending authority', status: 'open', authority: { issueId: 'epic-1.8', epicId: 'epic-1', attemptId: 'a2', workId: 'epic-1.4', requestId: 'req-2', permission: 'external_directory', target: '/outside', resolution: 'approve_pending' } },
      { id: 'epic-1.7', epicId: 'epic-1', kind: 'gate', title: 'Resolved', status: 'closed', recovery: { issueId: 'epic-1.7', resolution: 'resume', choices: [] } },
    ] : []) as never);
    vi.mocked(api.sessions).mockResolvedValue([{ id: 'plan-session', title: 'Plan docs', status: 'waiting', pendingQuestion: true, pendingPermission: false }] as never);
    renderFactory(<MemoryRouter><FactoryOverview /></MemoryRouter>);

		const inbox = await screen.findByRole('table', { name: 'Action inbox' });
		expect(within(inbox).getAllByRole('columnheader').map((cell) => cell.textContent)).toEqual(['Epic', 'Issue ID', 'Status', 'Actions']);
		expect(within(inbox).getAllByText('Ship Factory')).not.toHaveLength(0);
		expect(within(inbox).getByText('epic-1.2')).toBeInTheDocument();
		expect(within(inbox).getByRole('cell', { name: /epic-1\.5.*Recovery/ })).toBeInTheDocument();
    expect(within(inbox).getByRole('link', { name: 'Review plan' })).toHaveAttribute('href', '/factory/epics/epic-1');
    expect(within(inbox).getByText('Tests fail. Continue?')).toBeInTheDocument();
    expect(within(inbox).getByText('Allow bash on rm -rf dist?')).toBeInTheDocument();
		expect(within(inbox).getByText('Allow external_directory on /outside?')).toBeInTheDocument();
		expect(within(inbox).getByRole('button', { name: 'Retry approve' })).toBeInTheDocument();
    expect(within(inbox).queryByText('Resolved')).not.toBeInTheDocument();
    expect(within(inbox).getByText('Agent is waiting for you: Plan docs')).toBeInTheDocument();
    expect(within(inbox).getByRole('link', { name: 'Answer in session' })).toHaveAttribute('href', '/session/plan-session');

    await user.selectOptions(screen.getByLabelText('Recovery response for epic-1.5'), 'Fix tests');
    await user.click(within(inbox).getByRole('button', { name: 'Retry' }));
    await waitFor(() => expect(api.resolveFactoryRecoveryGate).toHaveBeenCalledWith('epic-1.5', 'retry', 'Fix tests'));
    await user.click(within(inbox).getByRole('button', { name: 'Reject' }));
    await waitFor(() => expect(api.resolveFactoryAuthorityGate).toHaveBeenCalledWith('epic-1.6', 'reject'));
  });

  it('surfaces exhausted work, unmaterialized plans, and stuck epics with an unblocking action', async () => {
    const user = userEvent.setup();
    vi.mocked(api.factoryEpics).mockResolvedValue([
      { id: 'epic-1', goal: 'Ship Factory', status: 'open', initialProject: '/repo', planGate: { issueId: 'epic-1.2', proposalRevision: 1, proposalHash: 'abc', resolution: 'approved' }, progress: { requiredTotal: 4, requiredSucceeded: 3, optionalOpen: 0, closureBlockers: ['Remove dead helpers'], stuck: true } },
      { id: 'epic-2', goal: 'Refresh docs', status: 'open', initialProject: '/docs', planGate: { issueId: 'epic-2.2', proposalRevision: 1, proposalHash: 'def', resolution: 'approved' }, progress: { requiredTotal: 4, requiredSucceeded: 2, optionalOpen: 0, closureBlockers: ['materialization: Refresh docs'], stuck: false } },
      { id: 'epic-3', goal: 'Mystery', status: 'open', initialProject: '/x', progress: { requiredTotal: 2, requiredSucceeded: 1, optionalOpen: 0, closureBlockers: ['Dependent task'], stuck: true } },
      { id: 'epic-4', goal: 'Rejected', status: 'closed', initialProject: '/y', progress: { requiredTotal: 1, requiredSucceeded: 0, optionalOpen: 0, closureBlockers: ['Cancelled'] } },
    ] as never);
    vi.mocked(api.factoryIssues).mockImplementation((id) => Promise.resolve(({
      'epic-1': [
        { id: 'epic-1.4', epicId: 'epic-1', kind: 'task', title: 'Remove dead helpers', status: 'closed', outcome: 'failed', outcomeReason: 'Implementation Session could not be launched', retryAttempts: 4, dispatchState: 'completed' },
        { id: 'epic-1.5', epicId: 'epic-1', kind: 'task', title: 'Done', status: 'closed', outcome: 'succeeded', dispatchState: 'completed' },
      ],
      'epic-2': [{ id: 'epic-2.3', epicId: 'epic-2', kind: 'materialization', title: 'materialization: Refresh docs', status: 'open', dispatchState: 'ready' }],
      'epic-3': [{ id: 'epic-3.2', epicId: 'epic-3', kind: 'task', title: 'Dependent task', status: 'open', dispatchState: 'terminally_blocked' }],
      'epic-4': [{ id: 'epic-4.1', epicId: 'epic-4', kind: 'task', title: 'Cancelled', status: 'closed', outcome: 'cancelled', dispatchState: 'completed' }],
    } as Record<string, unknown[]>)[id] ?? []) as never);
    renderFactory(<MemoryRouter><FactoryOverview /></MemoryRouter>);

    const inbox = await screen.findByRole('table', { name: 'Action inbox' });
    expect(within(inbox).getByText('Work failed after 4 attempts')).toBeInTheDocument();
    expect(within(inbox).getByText('Implementation Session could not be launched')).toBeInTheDocument();
    expect(within(inbox).getByText('Plan approved, no work graph yet')).toBeInTheDocument();
    expect(within(inbox).getByText('Stuck: nothing can proceed')).toBeInTheDocument();
    expect(within(inbox).getByText('Closure blocked by: Dependent task')).toBeInTheDocument();
    expect(within(inbox).getByRole('link', { name: 'Manage graph' })).toHaveAttribute('href', '/factory/epics/epic-3');
    // The epic with a Reopen row is not double-reported as stuck; closed epics never appear.
    expect(within(inbox).getAllByText('Stuck: nothing can proceed')).toHaveLength(1);
    expect(within(inbox).queryByText('Cancelled')).not.toBeInTheDocument();

    await user.click(within(inbox).getByRole('button', { name: 'Reopen' }));
    await waitFor(() => expect(api.reopenFactoryIssue).toHaveBeenCalledWith('epic-1', 'epic-1.4'));
    await user.click(within(inbox).getByRole('button', { name: 'Materialize plan' }));
    await waitFor(() => expect(api.factoryMaterialize).toHaveBeenCalledWith('epic-2', 'epic-2.3'));
  });

  it('shows running implementation and planning work with live session status', async () => {
    vi.mocked(api.factoryEpics).mockResolvedValue([
      { id: 'epic-1', goal: 'Ship Factory', status: 'open', initialProject: '/repo', attempts: [{ id: 'plan-attempt', workId: 'epic-1.1', phase: 'active', session: { platform: 'opencode', id: 'plan-session' } }, { id: 'old-attempt', workId: 'epic-1.1', phase: 'terminal', session: { platform: 'opencode', id: 'old-session' } }] },
    ] as never);
    vi.mocked(api.factoryIssues).mockResolvedValue([]);
    vi.mocked(api.factoryQueue).mockResolvedValue([
      { id: 'epic-1.4', epicId: 'epic-1', title: 'Implement controls', repository: '/repo', state: 'running', attemptId: 'a1', session: { platform: 'opencode', id: 'impl-session' } },
      { id: 'epic-1.5', epicId: 'epic-1', title: 'Next up', repository: '/repo', state: 'ready' },
    ] as never);
    vi.mocked(api.sessions).mockResolvedValue([{ id: 'impl-session', title: 'Implement', status: 'busy', pendingQuestion: false, pendingPermission: false }] as never);
    renderFactory(<MemoryRouter><FactoryOverview /></MemoryRouter>);

		const live = await screen.findByRole('table', { name: 'Live work' });
		expect(within(live).getAllByRole('columnheader').map((cell) => cell.textContent)).toEqual(['Epic', 'Issue ID', 'Status', 'Actions']);
		expect(within(live).getByText('Planning: Ship Factory')).toBeInTheDocument();
		expect(within(live).getByText('epic-1.1')).toBeInTheDocument();
		expect(within(live).getByRole('cell', { name: /epic-1\.4.*Implement controls/ })).toBeInTheDocument();
    expect(within(live).getByText('Implement controls')).toBeInTheDocument();
    expect(within(live).queryByText('Next up')).not.toBeInTheDocument();
    expect(within(live).getAllByRole('link', { name: /Open session/ })).toHaveLength(2);
    expect(await within(live).findByText('Busy')).toBeInTheDocument();
    expect(screen.getByText('Nothing needs your attention.')).toBeInTheDocument();
  });

  it('creates an epic from the selected immutable Formula revision', async () => {
    const user = userEvent.setup();
    vi.mocked(api.factoryFormulas).mockResolvedValue([{ id: 'custom/team', version: 2, name: 'Team', source: '', hash: 'hash', sourceHash: 'source-hash', inputs: [], nodes: [], edges: [], valid: true }] as never);
    vi.mocked(api.createFactoryEpic).mockResolvedValue({ id: 'epic-1' } as never);
    renderFactory(<MemoryRouter><FactoryEpics /></MemoryRouter>);
    await fillEpicForm(user);
    await user.selectOptions(screen.getByLabelText('Formula'), 'custom/team@2');
    await user.click(screen.getByRole('button', { name: 'Create epic' }));
    await waitFor(() => expect(api.createFactoryEpic).toHaveBeenCalledWith(expect.objectContaining({ formulaId: 'custom/team', formulaRevision: 2 })));
  });

  it('explains every epic input and the selected Formula', async () => {
    const user = userEvent.setup();
    vi.mocked(api.factoryFormulas).mockResolvedValue([{ id: 'custom/team', version: 2, name: 'Team', source: '', hash: 'hash', sourceHash: 'source-hash', inputs: [], nodes: [{ key: 'plan', kind: 'plan' }, { key: 'review', kind: 'gate' }], edges: [{ from: 'review', to: 'plan', type: 'blocks' }], valid: true }] as never);
    renderFactory(<MemoryRouter><FactoryEpics /></MemoryRouter>);
    await user.click(screen.getByRole('button', { name: 'New epic' }));

    expect(await screen.findByText('The outcome this Factory work should deliver.')).toBeInTheDocument();
    expect(screen.getByText('Optional context, constraints, and decisions for the planning work.')).toBeInTheDocument();
    expect(screen.getByText('The local repository where Factory starts work. Commands run on this machine.')).toBeInTheDocument();
    expect(screen.getByText('Defines the initial work graph. Formula revisions are immutable.')).toBeInTheDocument();
    expect(screen.getByText('Creates a plan, waits for approval, then materializes the approved plan.')).toBeInTheDocument();

    await screen.findByRole('option', { name: 'Team · custom/team@2' });
    await user.selectOptions(screen.getByLabelText('Formula'), 'custom/team@2');
    expect(screen.getByText('Creates plan (plan) and review (gate); review blocks plan.')).toBeInTheDocument();
  });

  it('reuses the instantiation ID when a failed create is retried unchanged', async () => {
    const user = userEvent.setup();
    const randomUUID = vi.spyOn(crypto, 'randomUUID').mockReturnValue('00000000-0000-0000-0000-000000000001');
    vi.mocked(api.createFactoryEpic).mockRejectedValueOnce(new Error('try again')).mockResolvedValue({ id: 'epic-1' } as never);
    renderFactory(<MemoryRouter><FactoryEpics /></MemoryRouter>);
    await fillEpicForm(user);

    await user.click(screen.getByRole('button', { name: 'Create epic' }));
    await screen.findByRole('alert');
    await user.click(screen.getByRole('button', { name: 'Create epic' }));

    await waitFor(() => expect(api.createFactoryEpic).toHaveBeenCalledTimes(2));
    expect(vi.mocked(api.createFactoryEpic).mock.calls[0][0].instantiationId).toBe('00000000-0000-0000-0000-000000000001');
    expect(vi.mocked(api.createFactoryEpic).mock.calls[1][0].instantiationId).toBe('00000000-0000-0000-0000-000000000001');
    expect(randomUUID).toHaveBeenCalledTimes(1);
  });

  it('shows poured issues in a read-only status board and opens their details', async () => {
    const user = userEvent.setup();
		const pouredIssues = [
			{ id: 'issue-1', epicId: 'epic-1', kind: 'mol', title: 'Child Formula', status: 'open', formulaId: 'custom/child', formulaVersion: 1, formulaHash: 'child-hash', bindings: { goal: 'Ship Factory' } },
			{ id: 'issue-2', epicId: 'epic-1', kind: 'task', title: 'Blocked work', status: 'blocked' },
			{ id: 'issue-3', epicId: 'epic-1', kind: 'task', title: 'Active work', status: 'in_progress' },
			{ id: 'issue-4', epicId: 'epic-1', kind: 'task', title: 'Finished work', status: 'closed' },
			{ id: 'issue-5', epicId: 'epic-1', kind: 'task', title: 'Deferred work', status: 'deferred' },
		];
    vi.mocked(api.factoryEpic).mockResolvedValue({ id: 'epic-1', goal: 'Ship Factory', status: 'open', initialProject: '/repo' } as never);
		vi.mocked(api.factoryIssues).mockResolvedValueOnce([]).mockResolvedValue(pouredIssues as never);
		vi.mocked(api.pourFactoryEpic).mockResolvedValue(pouredIssues as never);
    renderFactory(<MemoryRouter initialEntries={['/factory/epics/epic-1']}><Routes><Route path="/factory/epics/:id" element={<FactoryEpicDetail />} /></Routes></MemoryRouter>);

    await screen.findByText('This epic has no issues yet.');
    await user.click(screen.getByRole('button', { name: 'Pour graph' }));

    expect(await screen.findByLabelText('Epic issues by status')).toBeInTheDocument();
    expect(within(screen.getByRole('region', { name: 'Backlog issues' })).getByText('Child Formula')).toBeInTheDocument();
    expect(within(screen.getByRole('region', { name: 'Blocked issues' })).getByText('Blocked work')).toBeInTheDocument();
    expect(within(screen.getByRole('region', { name: 'In progress issues' })).getByText('Active work')).toBeInTheDocument();
    expect(within(screen.getByRole('region', { name: 'Done issues' })).getByText('Finished work')).toBeInTheDocument();
    expect(within(screen.getByRole('region', { name: 'Other issues' })).getByText('Deferred work')).toBeInTheDocument();
    await user.click(screen.getByRole('button', { name: 'Open issue issue-1' }));
    expect(screen.getByRole('dialog', { name: 'Issue issue-1' })).toHaveTextContent('Child Formula');
  });

  it('shows the linked planning session on epic detail', async () => {
    vi.mocked(api.factoryEpic).mockResolvedValue({ id: 'epic-1', goal: 'Ship Factory', status: 'open', initialProject: '/repo', attempts: [
      { id: 'attempt-0', workId: 'epic-1.plan', phase: 'terminal', session: { platform: 'opencode', id: 'session-0' } },
      { id: 'attempt-1', workId: 'epic-1.plan', phase: 'active', session: { platform: 'opencode', id: 'session-1' } },
    ] } as never);
    vi.mocked(api.factoryIssues).mockResolvedValue([]);
    renderFactory(<MemoryRouter initialEntries={['/factory/epics/epic-1']}><Routes><Route path="/factory/epics/:id" element={<FactoryEpicDetail />} /></Routes></MemoryRouter>);

    const planning = await screen.findByRole('region', { name: 'Planning' });
    expect(planning).not.toHaveTextContent('attempt-1');
    expect(planning).not.toHaveTextContent('session-1');
    expect(planning).toHaveTextContent('Attempt 2 · Running');
    expect(within(planning).getAllByRole('link', { name: 'Open session' })[0]).toHaveAttribute('href', '/session/session-1');
    expect(planning).toHaveTextContent('1 earlier attempt');
    expect(planning).toHaveTextContent('Attempt 1 · Finished');
  });

  it('shows the latest immutable proposal revision and hash on epic detail', async () => {
    vi.mocked(api.factoryEpic).mockResolvedValue({ id: 'epic-1', goal: 'Ship Factory', status: 'open', initialProject: '/repo', proposal: { revision: 2, contentHash: 'sha256:abc123' } } as never);
    vi.mocked(api.factoryIssues).mockResolvedValue([]);
    vi.mocked(api.factoryProposals).mockResolvedValue([{ revision: 1, contentHash: 'sha256:old', manifest: { nodes: [] } }, { revision: 2, contentHash: 'sha256:abc123', manifest: { nodes: [] }, rationaleMarkdown: '# Why' }] as never);
    renderFactory(<MemoryRouter initialEntries={['/factory/epics/epic-1']}><Routes><Route path="/factory/epics/:id" element={<FactoryEpicDetail />} /></Routes></MemoryRouter>);

    expect(await screen.findByText('Proposal revision: 2')).toBeInTheDocument();
    expect(screen.getByText('Content hash: sha256:abc123')).toBeInTheDocument();
    expect(screen.getByText('Proposal revision: 1')).toBeInTheDocument();
  });

  it('shows a retryable proposal history error', async () => {
    vi.mocked(api.factoryEpic).mockResolvedValue({ id: 'epic-1', goal: 'Ship Factory', status: 'open', initialProject: '/repo' } as never);
    vi.mocked(api.factoryIssues).mockResolvedValue([]);
    vi.mocked(api.factoryProposals).mockRejectedValue(new Error('Proposal history unavailable'));
    renderFactory(<MemoryRouter initialEntries={['/factory/epics/epic-1']}><Routes><Route path="/factory/epics/:id" element={<FactoryEpicDetail />} /></Routes></MemoryRouter>);

    expect(await screen.findByRole('alert')).toHaveTextContent('Proposal history unavailable');
    expect(screen.getByRole('button', { name: 'Retry' })).toBeInTheDocument();
  });

	it('approves the exact unresolved Plan gate revision', async () => {
		const user = userEvent.setup();
		vi.mocked(api.factoryEpic)
			.mockResolvedValueOnce({ id: 'epic-1', goal: 'Ship Factory', status: 'open', initialProject: '/repo', planGate: { issueId: 'epic-1.approval', proposalRevision: 2, proposalHash: 'abc', resolution: 'open' } } as never)
			.mockResolvedValueOnce({ id: 'epic-1', goal: 'Ship Factory', status: 'open', initialProject: '/repo', planGate: { issueId: 'epic-1.approval', proposalRevision: 2, proposalHash: 'abc', resolution: 'approved' } } as never);
		vi.mocked(api.factoryIssues).mockResolvedValue([]);
		vi.mocked(api.factoryProposals).mockResolvedValue([]);
		vi.mocked(api.factoryPlanGate).mockResolvedValue({ resolution: 'approved' } as never);
		renderFactory(<MemoryRouter initialEntries={['/factory/epics/epic-1']}><Routes><Route path="/factory/epics/:id" element={<FactoryEpicDetail />} /></Routes></MemoryRouter>);
		await user.click(await screen.findByRole('button', { name: 'Approve plan' }));
		await waitFor(() => expect(api.factoryPlanGate).toHaveBeenCalledWith('epic-1', 'approve', { expectedRevision: 2, expectedHash: 'abc', feedback: '' }));
		expect(await screen.findByRole('status')).toHaveTextContent('Plan approved.');
	});

	it('checks for a new proposal after requesting a revision', async () => {
		const user = userEvent.setup();
		vi.mocked(api.factoryEpic)
			.mockResolvedValueOnce({ id: 'epic-1', goal: 'Ship Factory', status: 'open', initialProject: '/repo', planGate: { issueId: 'epic-1.approval', proposalRevision: 2, proposalHash: 'abc', resolution: 'revision_requested' } } as never)
			.mockResolvedValueOnce({ id: 'epic-1', goal: 'Ship Factory', status: 'open', initialProject: '/repo', planGate: { issueId: 'epic-1.approval', proposalRevision: 3, proposalHash: 'def', resolution: 'open' } } as never);
		vi.mocked(api.factoryIssues).mockResolvedValue([]);
		vi.mocked(api.factoryProposals).mockResolvedValueOnce([]).mockResolvedValueOnce([{ revision: 3, contentHash: 'def', manifest: { nodes: [] } }] as never);
		renderFactory(<MemoryRouter initialEntries={['/factory/epics/epic-1']}><Routes><Route path="/factory/epics/:id" element={<FactoryEpicDetail />} /></Routes></MemoryRouter>);
		expect(await screen.findByText('Revision requested. Waiting for a new Plan proposal.')).toHaveAttribute('role', 'status');
		expect(screen.queryByRole('button', { name: 'Approve plan' })).not.toBeInTheDocument();
		await user.click(screen.getByRole('button', { name: 'Check for new proposal' }));
		expect(await screen.findByRole('button', { name: 'Approve plan' })).toBeInTheDocument();
		expect(await screen.findByText('Proposal revision: 3')).toBeInTheDocument();
	});

	it('announces a rejected Plan without malformed feedback', async () => {
		const user = userEvent.setup();
		vi.mocked(api.factoryEpic)
			.mockResolvedValueOnce({ id: 'epic-1', goal: 'Ship Factory', status: 'open', initialProject: '/repo', planGate: { issueId: 'epic-1.approval', proposalRevision: 2, proposalHash: 'abc', resolution: 'open' } } as never)
			.mockResolvedValueOnce({ id: 'epic-1', goal: 'Ship Factory', status: 'open', initialProject: '/repo', planGate: { issueId: 'epic-1.approval', proposalRevision: 2, proposalHash: 'abc', resolution: 'rejected' } } as never);
		vi.mocked(api.factoryIssues).mockResolvedValue([]);
		vi.mocked(api.factoryProposals).mockResolvedValue([]);
		vi.mocked(api.factoryPlanGate).mockResolvedValue({ resolution: 'rejected' } as never);
		renderFactory(<MemoryRouter initialEntries={['/factory/epics/epic-1']}><Routes><Route path="/factory/epics/:id" element={<FactoryEpicDetail />} /></Routes></MemoryRouter>);
		await user.click(await screen.findByRole('button', { name: 'Reject plan' }));
		expect(await screen.findByRole('status')).toHaveTextContent('Plan rejected.');
	});

	it('distinguishes optional work from closure blockers and explicitly closes the Mol before the Epic', async () => {
		const user = userEvent.setup();
		vi.mocked(api.factoryEpic).mockResolvedValue({ id: 'epic-1', goal: 'Ship Factory', status: 'open', initialProject: '/repo', progress: { requiredTotal: 3, requiredSucceeded: 2, optionalOpen: 4, closureBlockers: ['Required review'] } } as never);
		vi.mocked(api.factoryIssues).mockResolvedValue([{ id: 'epic-1.1', epicId: 'epic-1', kind: 'mol', title: 'Root Mol', status: 'open' }] as never);
		renderFactory(<MemoryRouter initialEntries={['/factory/epics/epic-1']}><Routes><Route path="/factory/epics/:id" element={<FactoryEpicDetail />} /></Routes></MemoryRouter>);
		expect(await screen.findByText('Required work: 2/3 complete. Optional work open: 4.')).toBeInTheDocument();
		expect(screen.getByText('Closure blocked by: Required review')).toBeInTheDocument();
		await user.click(screen.getByRole('button', { name: 'Close Mol' }));
		await waitFor(() => expect(api.factoryCloseMol).toHaveBeenCalledWith('epic-1', 'epic-1.1'));
		await user.click(screen.getByRole('button', { name: 'Close epic' }));
		await waitFor(() => expect(api.factoryCloseEpic).toHaveBeenCalledWith('epic-1'));
	});

	it('explains delayed, retry, blocked, and conditional queue dispatch states', async () => {
		vi.mocked(api.factoryQueue).mockResolvedValue([
			{ id: 'implement-1', epicId: 'epic-1', title: 'Active implementation', repository: '/repo', state: 'running', attemptId: 'attempt-1', session: { platform: 'opencode', id: 'session-1' } },
			{ id: 'implement-2', epicId: 'epic-2', title: 'Next implementation', repository: '/repo', state: 'ready' },
			{ id: 'implement-3', epicId: 'epic-3', title: 'Waiting implementation', repository: '/repo', state: 'terminally_blocked', blockers: [{ id: 'gate-1', reason: 'Rejected', outcome: 'failed' }] },
			{ id: 'implement-4', epicId: 'epic-4', title: 'Retry implementation', repository: '/repo', state: 'retry_wait', retryAt: 1_700_000_000_000, retryAttempts: 2 },
			{ id: 'implement-5', epicId: 'epic-5', title: 'Skipped recovery', repository: '/repo', state: 'not_applicable', blockers: [{ id: 'test-1', reason: 'Passed', outcome: 'succeeded' }] },
			{ id: 'implement-6', epicId: 'epic-6', title: 'Deferred implementation', repository: '/repo', state: 'deferred', outcomeReason: 'waiting for review' },
		] as never);
    renderFactory(<MemoryRouter><FactoryQueue /></MemoryRouter>);

    expect(await screen.findByRole('heading', { name: 'Active work' })).toBeInTheDocument();
		const active = screen.getByRole('table', { name: 'Active work' });
		expect(within(active).getAllByRole('columnheader').map((cell) => cell.textContent)).toEqual(['Issue', 'Epic', 'Repository', 'Dispatch', 'Outcome', 'Session']);
    expect(screen.getByRole('link', { name: 'Open session session-1' })).toHaveAttribute('href', '/session/session-1');
    expect(screen.getByRole('heading', { name: 'Next up' })).toBeInTheDocument();
		expect(screen.getByRole('table', { name: 'Next up' })).toBeInTheDocument();
		expect(screen.getByRole('table', { name: 'Waiting work' })).toBeInTheDocument();
		expect(screen.getByText('Next implementation')).toBeInTheDocument();
		expect(screen.getByText('Waiting implementation')).toBeInTheDocument();
		expect(screen.getByText('Retry implementation')).toBeInTheDocument();
		expect(screen.getByText('Waiting implementation').closest('tr')).toHaveTextContent('Dispatch: cannot proceed because gate-1 failed: Rejected.');
		expect(screen.getByText('Dispatch: retry 2 scheduled for 2023-11-14T22:13:20.000Z.')).toBeInTheDocument();
		expect(screen.getByText('Skipped recovery').closest('tr')).toHaveTextContent('Dispatch: not applicable because the recovery condition was not met (test-1 succeeded: Passed).');
		expect(screen.getByText('Dispatch: delayed: waiting for review.')).toBeInTheDocument();
		expect(screen.getByText('Capacity: 10 global, 4 per project.')).toBeInTheDocument();
	});

	it('links cross-Epic blocker evidence without treating it as local progress', async () => {
		vi.mocked(api.factoryQueue).mockResolvedValue([{ id: 'implement-1', epicId: 'epic-1', title: 'Waiting implementation', repository: '/repo', state: 'terminally_blocked', blockers: [{ id: 'review-1', epicId: 'epic-2', reason: 'Rejected', outcome: 'failed' }] }] as never);
		renderFactory(<MemoryRouter><FactoryQueue /></MemoryRouter>);

		const blocker = await screen.findByRole('link', { name: 'Open blocker review-1' });
		expect(blocker).toHaveAttribute('href', '/factory/issues/review-1');
		expect(blocker.parentElement?.parentElement).toHaveTextContent('Dispatch: cannot proceed because review-1 in Work Epic epic-2 failed: Rejected.');
	});

	it('shows a failed issue outcome in its drawer', async () => {
		const user = userEvent.setup();
		vi.mocked(api.factoryEpic).mockResolvedValue({ id: 'epic-1', goal: 'Ship Factory', status: 'open', initialProject: '/repo' } as never);
		vi.mocked(api.factoryIssues).mockResolvedValue([{ id: 'issue-1', epicId: 'epic-1', kind: 'implementation', title: 'Apply change', status: 'closed', outcome: 'failed', outcomeReason: 'Tests failed', dispatchState: 'completed' }] as never);
		renderFactory(<MemoryRouter initialEntries={['/factory/epics/epic-1']}><Routes><Route path="/factory/epics/:id" element={<FactoryEpicDetail />} /></Routes></MemoryRouter>);

		await user.click(await screen.findByRole('button', { name: 'Open issue issue-1' }));
		expect(screen.getByRole('dialog', { name: 'Issue issue-1' })).toHaveTextContent('failed: Tests failed');
	});

  it('creates graph work from keyboard-entered fields and refreshes the graph', async () => {
    const user = userEvent.setup();
    vi.mocked(api.factoryEpics).mockResolvedValue([
      { id: 'epic-1', goal: 'Ship Factory', status: 'open', initialProject: '/repo' },
      { id: 'epic-2', goal: 'Review Factory', status: 'open', initialProject: '/review' },
    ] as never);
    vi.mocked(api.factoryEpic).mockResolvedValue({ id: 'epic-1', goal: 'Ship Factory', status: 'open', initialProject: '/repo' } as never);
    vi.mocked(api.factoryIssues).mockImplementation((id) => Promise.resolve(id === 'epic-1' ? [{ id: 'epic-1.1', epicId: 'epic-1', kind: 'mol', title: 'Plan', status: 'open' }] : [{ id: 'epic-2.1', epicId: 'epic-2', kind: 'task', title: 'Review', status: 'open' }]) as never);
    renderFactory(<MemoryRouter initialEntries={['/factory/epics/epic-1']}><Routes><Route path="/factory/epics/:id" element={<FactoryEpicDetail />} /></Routes></MemoryRouter>);

    const manage = await screen.findByRole('button', { name: 'Manage graph' });
    expect(screen.queryByLabelText('Graph action')).not.toBeInTheDocument();
    await user.click(manage);
    await user.selectOptions(screen.getByLabelText('Graph action'), 'edit');
    expect(screen.getByLabelText('Work title')).toHaveValue('Plan');
    await user.selectOptions(screen.getByLabelText('Graph action'), 'link');
    expect(screen.getByRole('option', { name: 'Work Epic epic-2: Review (epic-2.1)' })).toBeInTheDocument();
    await user.selectOptions(screen.getByLabelText('Graph action'), 'create');
    await user.type(screen.getByLabelText('Work title'), 'Implement controls');
    await user.type(screen.getByLabelText('Work description'), 'Keyboard accessible');
    await user.tab();
    expect(screen.getByLabelText('Requirement')).toHaveFocus();
    await user.click(screen.getByRole('button', { name: 'Save graph change' }));

    await waitFor(() => expect(api.mutateFactoryGraph).toHaveBeenCalledWith('epic-1', {
      action: 'create', issueId: 'epic-1.1', parentId: 'epic-1.1', kind: 'task', title: 'Implement controls', description: 'Keyboard accessible', requirement: 'required',
    }));
  });

	it('prevents unconfirmed deletion and announces graph mutation errors', async () => {
    const user = userEvent.setup();
    vi.mocked(api.factoryEpic).mockResolvedValue({ id: 'epic-1', goal: 'Ship Factory', status: 'open', initialProject: '/repo' } as never);
    vi.mocked(api.factoryIssues).mockResolvedValue([{ id: 'epic-1.1', epicId: 'epic-1', kind: 'task', title: 'Open work', status: 'open' }] as never);
    vi.mocked(api.factoryProposals).mockResolvedValue([]);
    vi.mocked(api.mutateFactoryGraph).mockRejectedValue(new Error('factory dependency creates a cycle'));
    renderFactory(<MemoryRouter initialEntries={['/factory/epics/epic-1']}><Routes><Route path="/factory/epics/:id" element={<FactoryEpicDetail />} /></Routes></MemoryRouter>);

    await user.click(await screen.findByRole('button', { name: 'Manage graph' }));

    await screen.findByRole('heading', { name: 'Manage graph' });
    await user.selectOptions(screen.getByLabelText('Graph action'), 'delete');
    expect(screen.getByRole('button', { name: 'Soft-delete work' })).toBeDisabled();
    await user.click(screen.getByRole('checkbox'));
    await user.click(screen.getByRole('button', { name: 'Soft-delete work' }));

    expect(await screen.findByRole('alert')).toHaveTextContent('factory dependency creates a cycle');
  });

  it('shows queue loading state', async () => {
    const pending = new Promise<never>(() => {});
    vi.mocked(api.factoryQueue).mockReturnValueOnce(pending);
    renderFactory(<MemoryRouter><FactoryQueue /></MemoryRouter>);
    expect(await screen.findByRole('status')).toHaveTextContent('Loading execution queue…');
  });

  it('shows queue empty state', async () => {
    vi.mocked(api.factoryQueue).mockResolvedValue([]);
    renderFactory(<MemoryRouter><FactoryQueue /></MemoryRouter>);
    expect(await screen.findByText('No implementation work is active or waiting.')).toBeInTheDocument();
  });

	it('shows queue empty state when only completed history is returned', async () => {
		vi.mocked(api.factoryQueue).mockResolvedValue([{ id: 'implement-1', epicId: 'epic-1', title: 'Completed implementation', repository: '/repo', state: 'completed' }] as never);
		renderFactory(<MemoryRouter><FactoryQueue /></MemoryRouter>);
		expect(await screen.findByText('No implementation work is active or waiting.')).toBeInTheDocument();
	});

	it('keeps deletion audit context after a graph mutation', async () => {
		const user = userEvent.setup();
		vi.mocked(api.factoryEpic).mockResolvedValue({ id: 'epic-1', goal: 'Ship Factory', status: 'open', initialProject: '/repo' } as never);
		vi.mocked(api.factoryIssues).mockResolvedValue([{ id: 'epic-1.1', epicId: 'epic-1', kind: 'task', title: 'Open work', status: 'open' }, { id: 'epic-1.2', epicId: 'epic-1', kind: 'task', title: 'Delayed work', status: 'deferred', dispatchState: 'deferred', outcomeReason: 'waiting for review' }] as never);
		vi.mocked(api.factoryRemovedIssues).mockResolvedValue([{ id: 'epic-1.0', epicId: 'epic-1', kind: 'task', title: 'Removed work', status: 'open', removedAt: 1_700_000_000_000 }] as never);
		vi.mocked(api.factoryProposals).mockResolvedValue([]);
		renderFactory(<MemoryRouter initialEntries={['/factory/epics/epic-1']}><Routes><Route path="/factory/epics/:id" element={<FactoryEpicDetail />} /></Routes></MemoryRouter>);

		expect(await screen.findByRole('button', { name: 'Open issue epic-1.2' })).toBeInTheDocument();
		await user.click(screen.getByRole('button', { name: 'Manage graph' }));
		await user.selectOptions(screen.getByLabelText('Graph action'), 'delete');
		await user.click(screen.getByRole('checkbox'));
		await user.click(screen.getByRole('button', { name: 'Soft-delete work' }));

		expect(await screen.findByRole('status')).toHaveTextContent('Work soft-deleted. It remains in Factory audit history.');
		expect(screen.getByRole('region', { name: 'Removed work audit' })).toHaveTextContent('Removed work');
	});

  it('shows queue errors', async () => {
    vi.mocked(api.factoryQueue).mockRejectedValue(new Error('Queue unavailable'));
    renderFactory(<MemoryRouter><FactoryQueue /></MemoryRouter>);
    expect(await screen.findByRole('alert')).toHaveTextContent('Queue unavailable');
  });

	it('shows capacity errors without hiding queue state', async () => {
		vi.mocked(api.factoryQueue).mockResolvedValue([]);
		vi.mocked(api.factoryCapacityPolicy).mockRejectedValue(new Error('Capacity unavailable'));
		renderFactory(<MemoryRouter><FactoryQueue /></MemoryRouter>);
		expect(await screen.findByRole('alert')).toHaveTextContent('Capacity unavailable');
		expect(screen.getByText('No implementation work is active or waiting.')).toBeInTheDocument();
	});

	it('shows the immutable tracer Formula with its validation state', async () => {
		renderFactory(<MemoryRouter><FactoryConfiguration /></MemoryRouter>);

		expect(await screen.findByRole('heading', { name: 'Factory configuration' })).toBeInTheDocument();
		expect(await screen.findByLabelText('Tracer Formula source')).toHaveValue('name = "Tracer"\n');
		expect(screen.getByRole('status')).toHaveTextContent('Formula is valid');
		expect(screen.getByText('plan · plan')).toBeInTheDocument();
		expect(screen.getByText('approval → plan')).toBeInTheDocument();
	});

	it('offers accessible custom TOML Formula controls', () => {
		renderFactory(<MemoryRouter><FactoryConfiguration /></MemoryRouter>);

		expect(screen.getByLabelText('Custom Formula ID')).toBeInTheDocument();
		expect(screen.getByLabelText('Custom Formula TOML')).toBeInTheDocument();
		expect(screen.getByRole('button', { name: 'Validate TOML' })).toBeInTheDocument();
		expect(screen.getByRole('button', { name: 'Preview Formula' })).toBeInTheDocument();
		expect(screen.getByRole('button', { name: 'Save immutable revision' })).toBeInTheDocument();
	});

	it('saves global and per-project capacity policy', async () => {
		const user = userEvent.setup();
		vi.mocked(api.setFactoryCapacityPolicy).mockResolvedValue({ globalCapacity: 12, projectCapacity: 3, projectOverrides: { '/repo': 2 } });
		renderFactory(<MemoryRouter><FactoryConfiguration /></MemoryRouter>);
		const global = await screen.findByLabelText('Global implementation capacity');
		await user.clear(global);
		await user.type(global, '12');
		await user.click(screen.getByRole('button', { name: 'Save capacity policy' }));
		await waitFor(() => expect(api.setFactoryCapacityPolicy).toHaveBeenCalledWith({ globalCapacity: 12, projectCapacity: 4, projectOverrides: { '/repo': 2 } }));
	});

	it('announces rejected capacity updates', async () => {
		const user = userEvent.setup();
		vi.mocked(api.setFactoryCapacityPolicy).mockRejectedValue(new Error('factory capacity must be between 1 and 1000'));
		renderFactory(<MemoryRouter><FactoryConfiguration /></MemoryRouter>);
		await screen.findByLabelText('Project capacity overrides (JSON)');
		await user.click(screen.getByRole('button', { name: 'Save capacity policy' }));
		expect(await screen.findByRole('alert')).toHaveTextContent('factory capacity must be between 1 and 1000');
	});

	it('announces preview and save success', async () => {
		const user = userEvent.setup();
		vi.mocked(api.previewFactoryFormula).mockResolvedValue({ hash: 'preview-hash', sourceHash: 'preview-source-hash', compiled: {}, valid: true } as never);
		vi.mocked(api.saveFactoryFormula).mockResolvedValue({ id: 'custom/team', version: 1, hash: 'saved-hash', sourceHash: 'saved-source-hash', compiled: {}, valid: true } as never);
		renderFactory(<MemoryRouter><FactoryConfiguration /></MemoryRouter>);
		await user.type(screen.getByLabelText('Custom Formula ID'), 'custom/team');
		await user.click(screen.getByRole('button', { name: 'Preview Formula' }));
		expect(await screen.findByText('Preview valid: preview-hash')).toHaveAttribute('role', 'status');
		await user.click(screen.getByRole('button', { name: 'Save immutable revision' }));
		expect(await screen.findByText('Formula saved: custom/team@1')).toHaveAttribute('role', 'status');
	});

	it('shows every Formula validation diagnostic', async () => {
		const user = userEvent.setup();
		vi.mocked(api.previewFactoryFormula).mockResolvedValue({ valid: false, errors: ['composition child is missing binding for goal', 'composition child binding initial_project is unresolved'] } as never);
		renderFactory(<MemoryRouter><FactoryConfiguration /></MemoryRouter>);
		await user.type(screen.getByLabelText('Custom Formula ID'), 'custom/team');

		await user.click(screen.getByRole('button', { name: 'Preview Formula' }));
		expect(api.previewFactoryFormula).toHaveBeenCalledWith(expect.objectContaining({ id: 'custom/team', source: expect.stringContaining('version = 1') }), expect.anything());

		expect(await screen.findByRole('alert', { name: 'Formula diagnostics' })).toHaveTextContent('composition child is missing binding for goal');
		expect(screen.getByRole('alert', { name: 'Formula diagnostics' })).toHaveTextContent('composition child binding initial_project is unresolved');
	});

	it('clears Formula diagnostics after saving a corrected revision', async () => {
		const user = userEvent.setup();
		vi.mocked(api.previewFactoryFormula).mockResolvedValue({ valid: false, errors: ['composition child creates a composition cycle'] } as never);
		vi.mocked(api.saveFactoryFormula).mockResolvedValue({ id: 'custom/team', version: 1, valid: true } as never);
		renderFactory(<MemoryRouter><FactoryConfiguration /></MemoryRouter>);
		await user.type(screen.getByLabelText('Custom Formula ID'), 'custom/team');

		await user.click(screen.getByRole('button', { name: 'Preview Formula' }));
		await screen.findByRole('alert', { name: 'Formula diagnostics' });
		await user.click(screen.getByRole('button', { name: 'Save immutable revision' }));

		expect(await screen.findByText('Formula saved: custom/team@1')).toBeInTheDocument();
		expect(screen.queryByRole('alert', { name: 'Formula diagnostics' })).not.toBeInTheDocument();
	});
});
