// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { render, screen } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { describe, expect, it, vi } from 'vitest';
import { FactoryEpicCard } from './FactoryEpicCard';
import { factoryEpicIDFromHref, factoryEpicStatus } from './factoryEpicStatus';
import { api, type FactoryEpic } from '../lib/api';

vi.mock('../lib/api', () => ({ api: { factoryEpic: vi.fn() } }));

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

function renderCard() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(<QueryClientProvider client={client}><MemoryRouter><FactoryEpicCard epicID="ship-a1b2">Review and approve the plan</FactoryEpicCard></MemoryRouter></QueryClientProvider>);
}

describe('FactoryEpicCard', () => {
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
    expect(screen.getByText('ship-a1b2 · /repo')).toBeInTheDocument();
  });

  it('keeps the link usable when the epic cannot be loaded', async () => {
    vi.mocked(api.factoryEpic).mockRejectedValue(new Error('gone'));
    renderCard();

    expect(await screen.findByRole('link', { name: 'Review and approve the plan' })).toBeInTheDocument();
  });
});
