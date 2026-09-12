// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { api, type FactoryEpic, type FactoryIssue } from '../lib/api';
import { FactoryActionCard } from './FactoryActionCard';
import { factoryActionFromHref } from './factoryEpicStatus';
import { MarkdownContent } from './assistant/MarkdownText';

vi.mock('../lib/api', () => ({ api: {
  factoryEpic: vi.fn(), factoryIssues: vi.fn(), reopenFactoryIssue: vi.fn(),
  factoryClaimPlan: vi.fn(), factoryMaterialize: vi.fn(), pourFactoryEpic: vi.fn(),
  factoryPlanGate: vi.fn(), resolveFactoryRecoveryGate: vi.fn(), resolveFactoryAuthorityGate: vi.fn(),
} }));

const epic: FactoryEpic = {
  id: 'ship', status: 'open', goal: 'Ship Factory', initialProject: '/repo', formulaId: 'ocman/tracer', formulaVersion: 1,
  formulaRevision: 1, formulaHash: 'hash', formulaOrigin: 'built-in', instantiationId: 'one',
  progress: { requiredTotal: 1, requiredSucceeded: 0, optionalOpen: 0 },
};
const issue: FactoryIssue = { id: 'ship.3', epicId: 'ship', title: 'Build API', kind: 'implementation', status: 'closed', outcome: 'failed', outcomeReason: 'Dependency failed' };

function renderCard(text = '[Factory actions](/factory/epics/ship?human=1&issue=ship.3)') {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  render(<QueryClientProvider client={client}><MemoryRouter><MarkdownContent text={text} /></MemoryRouter></QueryClientProvider>);
  return client;
}

beforeEach(() => {
  vi.resetAllMocks();
  vi.mocked(api.factoryEpic).mockResolvedValue(epic);
  vi.mocked(api.factoryIssues).mockResolvedValue([issue]);
});

describe('Factory human action cards', () => {
  it.each([
    ['/factory/epics/ship?human=1&issue=ship.3', { epicID: 'ship', issueID: 'ship.3' }],
    ['/factory/epics/a%29b?human=1&issue=x%26y', { epicID: 'a)b', issueID: 'x&y' }],
    ['/factory/epics/ship?human=1', { epicID: 'ship', issueID: '' }],
    ['/factory/overview?human=1', { epicID: '', issueID: '' }],
    ['/factory/epics/ship', undefined],
    ['https://evil.test/factory/epics/ship?human=1', undefined],
    ['//evil.test/factory/epics/ship?human=1', undefined],
    ['/factory/epics/ship/extra?human=1', undefined],
    ['/factory/epics/%FF?human=1', undefined],
    [undefined, undefined],
  ])('parses only supported local links: %s', (href, expected) => {
    expect(factoryActionFromHref(href)).toEqual(expected);
  });

  it('reopens only on a human click, disables pending clicks, and refreshes live state', async () => {
    let finish!: (value: unknown) => void;
    vi.mocked(api.reopenFactoryIssue).mockImplementation(() => new Promise((resolve) => { finish = resolve; }));
    renderCard();
    const button = await screen.findByRole('button', { name: 'Reopen issue' });
    expect(api.reopenFactoryIssue).not.toHaveBeenCalled();
    expect(screen.getByText('Dependency failed')).toBeInTheDocument();
    expect(screen.getByRole('link', { name: 'Build API' })).toHaveAttribute('href', '/factory/issues/ship.3');
    fireEvent.click(button);
    await waitFor(() => expect(api.reopenFactoryIssue).toHaveBeenCalledWith('ship', 'ship.3'));
    expect(await screen.findByRole('button', { name: 'Reopening…' })).toBeDisabled();
    vi.mocked(api.factoryIssues).mockResolvedValue([{ ...issue, status: 'open', outcome: undefined }]);
    await act(async () => finish({}));
    expect(await screen.findByText('Issue reopened.')).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Reopen issue' })).not.toBeInTheDocument();
    expect(api.factoryIssues).toHaveBeenCalledTimes(2);
  });

  it('shows server rejection and permits a retry', async () => {
    vi.mocked(api.reopenFactoryIssue).mockRejectedValueOnce(new Error('Issue changed; reload')).mockResolvedValue({});
    renderCard();
    fireEvent.click(await screen.findByRole('button', { name: 'Reopen issue' }));
    expect(await screen.findByRole('alert')).toHaveTextContent('Issue changed; reload');
    fireEvent.click(screen.getByRole('button', { name: 'Reopen issue' }));
    expect(await screen.findByText('Issue reopened.')).toBeInTheDocument();
  });

  it.each(['open', 'succeeded', 'paused', 'closed', 'plan'])('does not offer reopening for ineligible work: %s', async (state) => {
    if (state === 'paused' || state === 'closed') vi.mocked(api.factoryEpic).mockResolvedValue({ ...epic, status: state });
    else vi.mocked(api.factoryIssues).mockResolvedValue([{ ...issue, ...(state === 'open' ? { status: 'open' } : state === 'plan' ? { kind: 'plan' } : { outcome: 'succeeded' }) }]);
    renderCard();
    await screen.findByText('Build API');
    expect(screen.queryByRole('button', { name: 'Reopen issue' })).not.toBeInTheDocument();
  });

  it('removes a stale reopen button when another client changes the issue', async () => {
    const client = renderCard();
    await screen.findByRole('button', { name: 'Reopen issue' });
    act(() => client.setQueryData(['factory-epics', 'ship', 'issues'], [{ ...issue, outcome: 'succeeded' }]));
    await waitFor(() => expect(screen.queryByRole('button', { name: 'Reopen issue' })).not.toBeInTheDocument());
  });

  it('offers planning and materialization actions on an epic card', async () => {
    vi.mocked(api.factoryIssues).mockResolvedValue([
      { ...issue, id: 'ship.1', kind: 'plan', status: 'open', outcome: undefined, dispatchState: 'ready' },
      { ...issue, id: 'ship.2', kind: 'materialization', status: 'open', outcome: undefined, dispatchState: 'ready' },
    ]);
    vi.mocked(api.factoryClaimPlan).mockResolvedValue({ session: { id: 'ses-1' } } as Awaited<ReturnType<typeof api.factoryClaimPlan>>);
    vi.mocked(api.factoryMaterialize).mockResolvedValue({ id: 'mat', issueId: 'ship.2', proposalRevision: 1, proposalHash: 'hash', manifestKey: 'mat', implementationId: 'impl', issues: [] });
    renderCard('[Factory actions](/factory/epics/ship?human=1)');
    fireEvent.click(await screen.findByRole('button', { name: 'Claim plan' }));
    expect(await screen.findByRole('link', { name: 'Open planning session' })).toHaveAttribute('href', '/session/ses-1');
    expect(api.factoryClaimPlan).toHaveBeenCalledWith('ship', 'ship.1');
    fireEvent.click(screen.getByRole('button', { name: 'Materialize plan' }));
    expect(await screen.findByText('Plan materialized.')).toBeInTheDocument();
    expect(api.factoryMaterialize).toHaveBeenCalledWith('ship', 'ship.2');
  });

  it('pours only an empty open epic and reports failures', async () => {
    vi.mocked(api.factoryIssues).mockResolvedValue([]);
    vi.mocked(api.pourFactoryEpic).mockRejectedValueOnce(new Error('Cannot pour')).mockResolvedValue([]);
    renderCard('[Factory actions](/factory/epics/ship?human=1)');
    fireEvent.click(await screen.findByRole('button', { name: 'Pour graph' }));
    expect(await screen.findByRole('alert')).toHaveTextContent('Cannot pour');
    fireEvent.click(screen.getByRole('button', { name: 'Pour graph' }));
    expect(await screen.findByText('Graph poured.')).toBeInTheDocument();
    expect(api.pourFactoryEpic).toHaveBeenCalledWith('ship');
    expect(screen.getByRole('button', { name: 'Pour graph' })).toBeDisabled();
  });

  it('keeps review and graph controls available', async () => {
    vi.mocked(api.factoryEpic).mockResolvedValue({ ...epic, planGate: { issueId: 'gate', resolution: 'open', proposalRevision: 1, proposalHash: 'hash' } });
    renderCard('[Factory actions](/factory/epics/ship?human=1)');
    expect(await screen.findByRole('link', { name: 'Review plan' })).toHaveAttribute('href', '/factory/epics/ship');
    expect(screen.getByRole('link', { name: 'Manage graph' })).toHaveAttribute('href', '/factory/epics/ship');
  });

  it.each([['Approve plan', 'approve'], ['Request revision', 'revise'], ['Reject plan', 'reject']])('submits %s against the displayed revision', async (label, action) => {
    vi.mocked(api.factoryEpic).mockResolvedValue({ ...epic, planGate: { issueId: 'gate', resolution: 'open', proposalRevision: 2, proposalHash: 'live-hash' } });
    vi.mocked(api.factoryPlanGate).mockResolvedValue({ issueId: 'gate', resolution: action, proposalRevision: 2, proposalHash: 'live-hash' });
    renderCard();
    await screen.findByText('Plan revision 2. Approval starts implementation.');
    expect(api.factoryPlanGate).not.toHaveBeenCalled();
    fireEvent.change(screen.getByLabelText('Plan feedback'), { target: { value: 'Human feedback' } });
    fireEvent.click(screen.getByRole('button', { name: label }));
    expect(await screen.findByText('Plan decision saved.')).toBeInTheDocument();
    expect(api.factoryPlanGate).toHaveBeenCalledWith('ship', action, { expectedRevision: 2, expectedHash: 'live-hash', feedback: 'Human feedback' });
  });

  it.each([['Resume work', 'resume'], ['Retry work', 'retry'], ['Cancel work', 'cancel']])('allows a human to choose %s', async (label, action) => {
    const recovery = { issueId: 'ship.3', epicId: 'ship', attemptId: 'attempt', workId: 'ship.1', question: 'Which API?', reason: 'Both supported', choices: ['A', 'B'], resolution: 'open' };
    vi.mocked(api.factoryIssues).mockResolvedValue([{ ...issue, kind: 'gate', recovery }]);
    vi.mocked(api.resolveFactoryRecoveryGate).mockResolvedValue({ ...recovery, resolution: action });
    renderCard();
    await screen.findByText('Which API?');
    expect(api.resolveFactoryRecoveryGate).not.toHaveBeenCalled();
    fireEvent.change(screen.getByLabelText('Recovery response'), { target: { value: 'B' } });
    fireEvent.click(screen.getByRole('button', { name: label }));
    expect(await screen.findByText('Recovery decision saved.')).toBeInTheDocument();
    expect(api.resolveFactoryRecoveryGate).toHaveBeenCalledWith('ship.3', action, action === 'resume' ? 'B' : '');
  });

  it('keeps a pending recovery limited to retrying resume and displays errors', async () => {
    const recovery = { issueId: 'ship.3', epicId: 'ship', attemptId: 'attempt', workId: 'ship.1', question: 'Which API?', reason: 'Both supported', choices: [], response: 'A', resolution: 'resume_pending' };
    vi.mocked(api.factoryIssues).mockResolvedValue([{ ...issue, recovery }]);
    vi.mocked(api.resolveFactoryRecoveryGate).mockRejectedValue(new Error('Session offline'));
    renderCard();
    fireEvent.change(await screen.findByLabelText('Recovery response'), { target: { value: 'B' } });
    expect(screen.queryByRole('button', { name: 'Retry work' })).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Resume work' }));
    expect(await screen.findByRole('alert')).toHaveTextContent('Session offline');
  });

  it.each([['Approve once', 'approve', 'open'], ['Reject permission', 'reject', 'open'], ['Approve once', 'approve', 'approve_pending'], ['Reject permission', 'reject', 'reject_pending']])('submits %s for a live authority gate in %s state', async (label, action, resolution) => {
    const authority = { issueId: 'ship.3', epicId: 'ship', attemptId: 'attempt', workId: 'ship.1', requestId: 'req', permission: 'bash', target: 'git push', resolution };
    vi.mocked(api.factoryIssues).mockResolvedValue([{ ...issue, kind: 'gate', authority }]);
    vi.mocked(api.resolveFactoryAuthorityGate).mockResolvedValue({ ...authority, resolution: action });
    renderCard();
    await screen.findByText('Target: git push');
    expect(api.resolveFactoryAuthorityGate).not.toHaveBeenCalled();
    if (resolution.endsWith('_pending')) expect(screen.queryByRole('button', { name: action === 'approve' ? 'Reject permission' : 'Approve once' })).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: label }));
    expect(await screen.findByText('Permission decision saved.')).toBeInTheDocument();
    expect(api.resolveFactoryAuthorityGate).toHaveBeenCalledWith('ship.3', action);
  });

  it('handles deleted issues without offering a different target', async () => {
    renderCard('[Factory actions](/factory/epics/ship?human=1&issue=missing)');
    expect(await screen.findByText('Issue missing is no longer available.')).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Reopen issue' })).not.toBeInTheDocument();
  });

  it('retries failed reads without exposing stale actions', async () => {
    vi.mocked(api.factoryIssues).mockRejectedValueOnce(new Error('Offline'));
    renderCard();
    expect(screen.getByText('Loading available actions…')).toBeInTheDocument();
    fireEvent.click(await screen.findByRole('button', { name: 'Retry' }));
    expect(await screen.findByRole('button', { name: 'Reopen issue' })).toBeInTheDocument();
  });

  it('renders an inbox card when the tool has no target', () => {
    renderCard('[Factory actions](/factory/overview?human=1)');
    expect(screen.getByRole('link', { name: 'Open action inbox' })).toHaveAttribute('href', '/factory/overview');
    expect(api.factoryEpic).not.toHaveBeenCalled();
    expect(api.factoryIssues).not.toHaveBeenCalled();
  });

  it('renders safely directly as well as through markdown', async () => {
    const client = new QueryClient();
    render(<QueryClientProvider client={client}><MemoryRouter><FactoryActionCard epicID="ship" /></MemoryRouter></QueryClientProvider>);
    expect(await screen.findByRole('button', { name: 'Reopen issue' })).toBeInTheDocument();
  });
});
