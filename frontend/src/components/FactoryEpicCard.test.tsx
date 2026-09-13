// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { MemoryRouter, useLocation } from 'react-router-dom';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { FactoryEpicCard } from './FactoryEpicCard';
import { factoryEpicIDFromHref, factoryEpicStatus } from './factoryEpicStatus';
import { api, type FactoryEpic, type FactoryIssue } from '../lib/api';

vi.mock('../lib/api', () => ({ api: { factoryEpic: vi.fn(), factoryIssues: vi.fn(), factoryClaimPlan: vi.fn() } }));

const plan: FactoryIssue = { id: 'ship-a1b2.1.1', epicId: 'ship-a1b2', project: '/repo', kind: 'plan', title: 'Plan delivery', status: 'open', dispatchState: 'ready' };

beforeEach(() => {
  vi.mocked(api.factoryIssues).mockResolvedValue([]);
});

const epic = (overrides: Partial<FactoryEpic> = {}): FactoryEpic => ({
  id: 'ship-a1b2', status: 'open', goal: 'Ship Factory', initialProject: '/repo', formulaId: 'ocman/tracer', formulaVersion: 1,
  formulaRevision: 1, formulaHash: 'hash', formulaOrigin: 'built-in', instantiationId: 'one',
  progress: { requiredTotal: 0, requiredSucceeded: 0, optionalOpen: 0 }, ...overrides,
});

describe('factoryEpicIDFromHref', () => {
  it.each([
    ['/factory/epics/ship-a1b2', 'ship-a1b2'],
    ['/factory/epics/ship-a1b2/issues', undefined],
    ['/factory/epics/', undefined],
    ['https://example.com/factory/epics/ship-a1b2', undefined],
    [undefined, undefined],
  ])('reads %s', (href, want) => {
    expect(factoryEpicIDFromHref(href)).toBe(want);
  });
});

describe('factoryEpicStatus', () => {
  it.each([
    ['Plan awaiting your approval', epic({ planGate: { issueId: 'g', proposalRevision: 1, proposalHash: 'h', resolution: 'open' } })],
    ['Plan revision requested', epic({ planGate: { issueId: 'g', proposalRevision: 1, proposalHash: 'h', resolution: 'revision_requested' } })],
    ['Plan rejected', epic({ planGate: { issueId: 'g', proposalRevision: 1, proposalHash: 'h', resolution: 'rejected' } })],
    ['Closed', epic({ status: 'closed' })],
    ['Paused', epic({ status: 'paused' })],
    ['Stuck: nothing can proceed', epic({ progress: { requiredTotal: 2, requiredSucceeded: 1, optionalOpen: 0, stuck: true } })],
    ['2/3 required work complete, 1 optional open', epic({ progress: { requiredTotal: 3, requiredSucceeded: 2, optionalOpen: 1 } })],
    ['3/3 required work complete', epic({ progress: { requiredTotal: 3, requiredSucceeded: 3, optionalOpen: 0 } })],
    ['Open', epic()],
  ])('reports %s', (text, input) => {
    expect(factoryEpicStatus(input).text).toBe(text);
  });

  it('marks work that still needs the user', () => {
    expect(factoryEpicStatus(epic({ planGate: { issueId: 'g', proposalRevision: 1, proposalHash: 'h', resolution: 'open' } })).tone).toBe('action');
    expect(factoryEpicStatus(epic({ status: 'closed' })).tone).toBe('done');
  });
});

function Location() {
  const location = useLocation();
  return <output>{location.pathname}{location.search}</output>;
}

function renderCard() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(<QueryClientProvider client={client}><MemoryRouter><p><FactoryEpicCard epicID="ship-a1b2">Review and approve the plan</FactoryEpicCard></p><Location /></MemoryRouter></QueryClientProvider>);
  return client;
}

describe('FactoryEpicCard', () => {
	it('shows every Epic project', async () => {
		vi.mocked(api.factoryEpic).mockResolvedValue(epic({ projects: [{ path: '/repo', removable: false }, { path: '/docs', removable: true }] }));
		renderCard();
		expect(await screen.findByText('/docs')).toBeInTheDocument();
	});

  it('claims ready planning work from the card and opens its session', async () => {
    vi.mocked(api.factoryEpic).mockResolvedValue(epic());
    vi.mocked(api.factoryIssues).mockResolvedValue([plan]);
    vi.mocked(api.factoryClaimPlan).mockResolvedValue({ attempt: { id: 'a', workId: plan.id, phase: 'active', session: { platform: 'opencode', id: 'session/1' } }, session: { platform: 'opencode', id: 'session/1' } });
    renderCard();

    fireEvent.click(await screen.findByRole('button', { name: 'Claim plan' }));

    await waitFor(() => expect(api.factoryClaimPlan).toHaveBeenCalledWith('ship-a1b2', plan.id));
    expect(await screen.findByText('/session/session%2F1?factoryEpic=ship-a1b2')).toBeInTheDocument();
  });

  it('disables claiming while pending and shows claim errors on the card', async () => {
    vi.mocked(api.factoryEpic).mockResolvedValue(epic());
    vi.mocked(api.factoryIssues).mockResolvedValue([plan]);
    let reject!: (error: Error) => void;
    vi.mocked(api.factoryClaimPlan).mockReturnValue(new Promise((_, fail) => { reject = fail; }));
    renderCard();

    fireEvent.click(await screen.findByRole('button', { name: 'Claim plan' }));
    expect(await screen.findByRole('button', { name: 'Claiming plan…' })).toBeDisabled();
    reject(new Error('Planner unavailable'));
    expect(await screen.findByRole('alert')).toHaveTextContent('Planner unavailable');
    expect(screen.getByRole('button', { name: 'Claim plan' })).toBeEnabled();
  });

  it.each<[string, FactoryEpic, FactoryIssue]>([
    ['paused epic', epic({ status: 'paused' }), plan],
    ['closed epic', epic({ status: 'closed' }), plan],
    ['blocked plan', epic(), { ...plan, dispatchState: 'waiting' }],
    ['task', epic(), { ...plan, kind: 'task' }],
    ['no ready plan', epic(), { ...plan, dispatchState: undefined }],
    ...['prepared', 'active', 'stopping'].map((phase): [string, FactoryEpic, FactoryIssue] => [phase, epic({ attempts: [{ id: 'a', workId: plan.id, phase, session: { platform: 'opencode', id: 's' } }] }), plan]),
  ])('does not offer claiming for %s', async (_, data, issue) => {
    vi.mocked(api.factoryEpic).mockResolvedValue(data);
    vi.mocked(api.factoryIssues).mockResolvedValue([issue]);
    const client = renderCard();

    await screen.findByTestId('epic-card-ship-a1b2');
    if (data.status === 'open') await waitFor(() => expect(client.getQueryData(['factory-epics', data.id, 'issues'])).toEqual([issue]));
    expect(screen.queryByRole('button', { name: 'Claim plan' })).not.toBeInTheDocument();
  });

  it('removes the claim action after the plan is claimed', async () => {
    vi.mocked(api.factoryEpic).mockResolvedValue(epic());
    vi.mocked(api.factoryIssues).mockResolvedValue([plan]);
    vi.mocked(api.factoryClaimPlan).mockImplementation(async () => {
      vi.mocked(api.factoryIssues).mockResolvedValue([{ ...plan, dispatchState: 'running' }]);
      return { attempt: { id: 'a', workId: plan.id, phase: 'active', session: { platform: 'opencode', id: 's' } }, session: { platform: 'opencode', id: 's' } };
    });
    renderCard();

    fireEvent.click(await screen.findByRole('button', { name: 'Claim plan' }));
    await screen.findByText('/session/s?factoryEpic=ship-a1b2');
    await waitFor(() => expect(screen.queryByRole('button')).not.toBeInTheDocument());
  });

  it('shows the agent link while the epic loads', () => {
    vi.mocked(api.factoryEpic).mockReturnValue(new Promise(() => {}));
    renderCard();

    expect(screen.getByRole('link', { name: 'Review and approve the plan' })).toHaveAttribute('href', '/factory/epics/ship-a1b2');
  });

  it('replaces the link with the live epic status', async () => {
    vi.mocked(api.factoryEpic).mockResolvedValue(epic({ planGate: { issueId: 'g', proposalRevision: 1, proposalHash: 'h', resolution: 'open' } }));
    renderCard();

    expect(await screen.findByText('Plan awaiting your approval')).toBeInTheDocument();
    expect(screen.getByRole('link', { name: 'Ship Factory' })).toHaveAttribute('href', '/factory/epics/ship-a1b2');
    expect(screen.getByTestId('epic-card-ship-a1b2')).toHaveTextContent('ship-a1b2 · /repo');
    expect(screen.getByTitle('/repo')).toHaveTextContent('/repo');
  });

  it('keeps the link usable when the epic cannot be loaded', async () => {
    vi.mocked(api.factoryEpic).mockRejectedValue(new Error('gone'));
    renderCard();

    expect(await screen.findByRole('link', { name: 'Review and approve the plan' })).toBeInTheDocument();
  });
});
