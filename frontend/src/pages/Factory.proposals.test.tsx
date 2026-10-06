// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { beforeEach, expect, it, vi } from 'vitest';
import { api, type FactoryEpic, type FactoryProposal } from '../lib/api';
import { FactoryEpicDetail } from './FactoryEpics';

vi.mock('@xyflow/react', async (original) => ({
  ...await original<typeof import('@xyflow/react')>(),
  ReactFlow: ({ nodes }: { nodes: { id: string; className: string; data: { label: React.ReactNode } }[] }) => <div>{nodes.map((node) => <div key={node.id} data-testid={`node-${node.id}`} className={node.className}>{node.data.label}</div>)}</div>,
}));
vi.mock('../lib/api', () => ({ api: {
  factoryEpic: vi.fn(), factoryEpics: vi.fn(), factoryIssues: vi.fn(), factoryRemovedIssues: vi.fn(),
  factoryProposals: vi.fn(), factoryPlanGate: vi.fn(), factoryQueue: vi.fn(), projects: vi.fn(), sessions: vi.fn(), sessionModels: vi.fn(),
} }));

const original: FactoryProposal = { revision: 2, contentHash: 'approved', manifest: {
  epicId: 'ship', molId: 'mol', project: '/repo', nodes: [{ key: 'existing', type: 'implementation', requirement: 'required', title: 'Existing task' }],
} };
const pending: FactoryProposal = { revision: 4, contentHash: 'pending', manifest: {
  epicId: 'ship', molId: 'mol', project: '/repo', nodes: [], baseRevision: 2,
  issues: [
    { id: 'ship.1', epicId: 'ship', project: '/repo', kind: 'implementation', title: 'Existing task', status: 'closed', manifestKey: 'existing' },
    { id: 'ship.2', epicId: 'ship', project: '/repo', kind: 'task', title: 'First new task', status: 'open' },
    { id: 'ship.3', epicId: 'ship', project: '/repo', kind: 'task', title: 'Second new task', status: 'open', dependsOn: [{ id: 'ship.2', type: 'blocks' }] },
  ],
} };
const epic: FactoryEpic = { id: 'ship', goal: 'Ship', status: 'open', initialProject: '/repo', formulaId: 'ocman/tracer', formulaVersion: 4, formulaRevision: 4, formulaHash: 'formula', formulaOrigin: 'built-in', instantiationId: '', progress: { requiredTotal: 3, requiredSucceeded: 1, optionalOpen: 0 },
  planGate: { issueId: 'gate', resolution: 'open', proposalRevision: 4, proposalHash: 'pending' },
};

beforeEach(() => {
  vi.resetAllMocks();
  vi.mocked(api.factoryEpic).mockResolvedValue(epic);
  vi.mocked(api.factoryEpics).mockResolvedValue([]);
  vi.mocked(api.factoryIssues).mockResolvedValue([]);
  vi.mocked(api.factoryRemovedIssues).mockResolvedValue([]);
  vi.mocked(api.factoryQueue).mockResolvedValue([]);
  vi.mocked(api.sessions).mockResolvedValue([]);
  vi.mocked(api.projects).mockResolvedValue([]);
  vi.mocked(api.sessionModels).mockResolvedValue({ models: [], hasProviders: false });
  vi.mocked(api.factoryProposals).mockResolvedValue([original, pending]);
  vi.mocked(api.factoryPlanGate).mockResolvedValue({ ...epic.planGate!, resolution: 'approved' });
});

function mount() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  render(<QueryClientProvider client={client}><MemoryRouter initialEntries={['/factory/epics/ship']}><Routes><Route path="/factory/epics/:id" element={<FactoryEpicDetail />} /></Routes></MemoryRouter></QueryClientProvider>);
}

it('lets the exact pending proposal be approved from its expanded revision', async () => {
  mount();
  const approve = await screen.findByRole('button', { name: 'Approve revision 4' });
  expect(approve.closest('details')).toHaveAttribute('open');
  expect(screen.queryByRole('button', { name: 'Approve revision 2' })).not.toBeInTheDocument();
  fireEvent.click(approve);
  await waitFor(() => expect(api.factoryPlanGate).toHaveBeenCalledWith('ship', 'approve', { expectedRevision: 4, expectedHash: 'pending', feedback: '' }));
});

it('marks additions without treating materialized identities as new work', async () => {
  const intermediate = { ...pending, revision: 3, contentHash: 'intermediate', manifest: { ...pending.manifest, issues: pending.manifest.issues!.slice(0, 2) } };
  vi.mocked(api.factoryProposals).mockResolvedValue([original, intermediate, pending]);
  mount();
  const first = await screen.findByTestId('node-ship.2');
  expect(within(first).getByText('Added')).toBeInTheDocument();
  expect(within(screen.getByTestId('node-ship.3')).getByText('Added')).toBeInTheDocument();
  expect(within(screen.getByTestId('node-ship.1')).queryByText('Added')).not.toBeInTheDocument();
});

it('makes the absence of a new pending proposal explicit', async () => {
  vi.mocked(api.factoryEpic).mockResolvedValue({ ...epic, planGate: { issueId: 'gate', resolution: 'approved', proposalRevision: 2, proposalHash: 'approved' } });
  vi.mocked(api.factoryProposals).mockResolvedValue([original]);
  mount();
  await userEvent.setup().click(await screen.findByRole('tab', { name: 'Plan' }));
  expect(screen.getByText('No new proposal is awaiting approval.')).toBeInTheDocument();
  expect(screen.queryByRole('button', { name: /Approve/ })).not.toBeInTheDocument();
});

it('keeps additions visible in the Graph tab', async () => {
  vi.mocked(api.factoryIssues).mockResolvedValue(pending.manifest.issues!);
  mount();
  await userEvent.setup().click(await screen.findByRole('tab', { name: 'Graph' }));
  const graph = within(screen.getByRole('tabpanel', { name: 'Graph' }));
  await waitFor(() => expect(within(graph.getByTestId('node-ship.2')).getByText('Added')).toBeInTheDocument());
});
