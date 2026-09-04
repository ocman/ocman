// @vitest-environment jsdom
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { AppRoutes } from './App';
import { useAddFactoryIssueComment, useClaimFactoryPlan, useCloseFactoryEpic, useCloseFactoryMol, useCreateWorkEpic, useDecideFactoryPlanGate, useFactoryFormula, useFactoryFormulas, useFactoryGraphIssues, useFactoryIssueComments, useFactoryIssues, useFactoryProposals, useFactoryQueue, useFactoryRemovedIssues, useMaterializeFactoryPlan, useMutateFactoryGraph, usePourFactoryEpic, useProjects, useResolveFactoryAuthorityGate, useResolveFactoryRecoveryGate, useSessions, useWorkEpic, useWorkEpics } from './lib/queries';

vi.mock('./lib/queries', () => ({
  useSessions: vi.fn(),
  useWorkEpics: vi.fn(),
  useWorkEpic: vi.fn(),
  useFactoryIssues: vi.fn(),
	useFactoryRemovedIssues: vi.fn(),
	useFactoryGraphIssues: vi.fn(),
	useFactoryIssueComments: vi.fn(),
	useAddFactoryIssueComment: vi.fn(),
	useMutateFactoryGraph: vi.fn(),
	useFactoryProposals: vi.fn(),
	useDecideFactoryPlanGate: vi.fn(),
	useResolveFactoryAuthorityGate: vi.fn(),
	useResolveFactoryRecoveryGate: vi.fn(),
	useFactoryQueue: vi.fn(),
	useCloseFactoryMol: vi.fn(),
	useCloseFactoryEpic: vi.fn(),
	useFactoryFormula: vi.fn(),
  useFactoryFormulas: vi.fn(),
  useProjects: vi.fn(),
  useCreateWorkEpic: vi.fn(),
  usePourFactoryEpic: vi.fn(),
  useClaimFactoryPlan: vi.fn(),
  useMaterializeFactoryPlan: vi.fn(),
}));

function renderRoute(path: string) {
  render(<MemoryRouter initialEntries={[path]}><AppRoutes /></MemoryRouter>);
}

beforeEach(() => {
  vi.mocked(useWorkEpics).mockReturnValue({ data: [], isLoading: false, isError: false, refetch: vi.fn() } as never);
  vi.mocked(useWorkEpic).mockReturnValue({ data: undefined, isLoading: false, isError: false, refetch: vi.fn() } as never);
	vi.mocked(useFactoryIssues).mockReturnValue({ data: [], isLoading: false, isError: false, refetch: vi.fn() } as never);
	vi.mocked(useFactoryRemovedIssues).mockReturnValue({ data: [], isLoading: false, isError: false, refetch: vi.fn() } as never);
	vi.mocked(useFactoryGraphIssues).mockReturnValue([] as never);
	vi.mocked(useFactoryIssueComments).mockReturnValue({ data: [], isLoading: false, isError: false } as never);
	vi.mocked(useAddFactoryIssueComment).mockReturnValue({ mutate: vi.fn(), isPending: false, isError: false } as never);
	vi.mocked(useMutateFactoryGraph).mockReturnValue({ mutateAsync: vi.fn(), isPending: false, isError: false } as never);
	vi.mocked(useFactoryProposals).mockReturnValue({ data: [], isLoading: false, isError: false, refetch: vi.fn() } as never);
	vi.mocked(useDecideFactoryPlanGate).mockReturnValue({ mutate: vi.fn(), isPending: false } as never);
	vi.mocked(useResolveFactoryAuthorityGate).mockReturnValue({ mutate: vi.fn(), isPending: false } as never);
	vi.mocked(useResolveFactoryRecoveryGate).mockReturnValue({ mutate: vi.fn(), isPending: false } as never);
	vi.mocked(useFactoryQueue).mockReturnValue({ data: [], isLoading: false, isError: false, refetch: vi.fn() } as never);
	vi.mocked(useSessions).mockReturnValue({ data: [], isLoading: false, isError: false } as never);
	vi.mocked(useCloseFactoryMol).mockReturnValue({ mutate: vi.fn(), isPending: false, isError: false } as never);
	vi.mocked(useCloseFactoryEpic).mockReturnValue({ mutate: vi.fn(), isPending: false, isError: false } as never);
	vi.mocked(useFactoryFormula).mockReturnValue({ data: { id: 'ocman/tracer', version: 1, name: 'Tracer', source: '', hash: 'hash', sourceHash: 'source-hash', inputs: [], nodes: [], edges: [], valid: true }, isLoading: false, isError: false, refetch: vi.fn() } as never);
	vi.mocked(useFactoryFormulas).mockReturnValue({ data: [], isLoading: false, isError: false, refetch: vi.fn() } as never);
  vi.mocked(useProjects).mockReturnValue({ data: [], isLoading: false, isError: false, refetch: vi.fn() } as never);
  vi.mocked(useCreateWorkEpic).mockReturnValue({ mutateAsync: vi.fn(), isPending: false } as never);
  vi.mocked(usePourFactoryEpic).mockReturnValue({ mutate: vi.fn(), isPending: false } as never);
  vi.mocked(useClaimFactoryPlan).mockReturnValue({ mutate: vi.fn(), isPending: false } as never);
  vi.mocked(useMaterializeFactoryPlan).mockReturnValue({ mutate: vi.fn(), isPending: false } as never);
});

describe('Factory routes', () => {
  it('renders action inbox and live work on the Factory overview', () => {
    renderRoute('/factory');
    expect(screen.getByRole('heading', { name: 'Action inbox' })).toBeInTheDocument();
    expect(screen.getByText('Nothing needs your attention.')).toBeInTheDocument();
    expect(screen.getByText('No agents are working right now.')).toBeInTheDocument();
  });

  it('keeps the epic list focused on epics', () => {
    renderRoute('/factory/epics');
    expect(screen.getByRole('heading', { name: 'Epics' })).toBeInTheDocument();
    expect(screen.queryByRole('heading', { name: 'Action inbox' })).not.toBeInTheDocument();
    expect(screen.queryByRole('heading', { name: 'Live work' })).not.toBeInTheDocument();
  });

  it('explains the Factory process', () => {
    renderRoute('/factory/how-to');
    expect(screen.getByRole('heading', { name: 'How Factory works' })).toBeInTheDocument();
    expect(screen.getByRole('heading', { name: 'Create an epic' })).toBeInTheDocument();
    expect(screen.getByRole('heading', { name: 'Review the result' })).toBeInTheDocument();
    expect(screen.getByRole('link', { name: 'How to', current: 'page' })).toHaveAttribute('href', '/factory/how-to');
  });

  it('renders an explicit loading state for epic routes', () => {
    vi.mocked(useWorkEpics).mockReturnValue({ isLoading: true, isError: false } as never);
    renderRoute('/factory/epics');
    expect(screen.getByRole('status')).toHaveTextContent('Loading epics…');
  });

  it('renders a retryable error state for the issue route', () => {
    vi.mocked(useWorkEpics).mockReturnValue({ isLoading: false, isError: true, error: new Error('Factory unavailable'), refetch: vi.fn() } as never);
    renderRoute('/factory/issues');
    expect(screen.getByRole('alert')).toHaveTextContent('Factory unavailable');
    expect(screen.getByRole('button', { name: 'Retry' })).toBeInTheDocument();
  });

  it('renders an epic graph', () => {
    const epic = { id: 'epic-1', goal: 'Ship Factory', status: 'open', initialProject: '/repo', formulaId: 'ocman/tracer', formulaVersion: 1, formulaRevision: 1, formulaHash: 'hash', formulaOrigin: 'built-in', instantiationId: 'request-1' };
    vi.mocked(useWorkEpic).mockReturnValue({ data: epic, isLoading: false, isError: false } as never);
    vi.mocked(useFactoryIssues).mockReturnValue({ data: [{ id: 'issue-1', epicId: epic.id, kind: 'mol', title: 'Child Formula', status: 'open', formulaId: 'custom/child', formulaVersion: 1, formulaHash: 'child-hash', bindings: { goal: 'Ship Factory' } }], isLoading: false, isError: false } as never);
    renderRoute('/factory/epics/epic-1');
    expect(screen.getByRole('heading', { name: 'Ship Factory' })).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Open issue issue-1' }));
    expect(screen.getByRole('dialog', { name: 'Issue issue-1' })).toHaveTextContent('Child Formula');
  });

  it('renders issues as tickets', () => {
    const epic = { id: 'epic-1', goal: 'Ship Factory', status: 'open', initialProject: '/repo', formulaId: 'ocman/tracer', formulaVersion: 1, formulaRevision: 1, formulaHash: 'hash', formulaOrigin: 'built-in', instantiationId: 'request-1' };
    vi.mocked(useWorkEpics).mockReturnValue({ data: [epic], isLoading: false, isError: false } as never);
    vi.mocked(useFactoryGraphIssues).mockReturnValue([{ data: [{ id: 'issue-1', epicId: epic.id, kind: 'plan', title: 'Plan Factory', status: 'open' }], isLoading: false, isError: false }] as never);
    renderRoute('/factory/issues');
    expect(screen.getByRole('heading', { name: 'Factory issues' })).toBeInTheDocument();
    expect(screen.getByText('issue-1')).toBeInTheDocument();
    expect(screen.getByText('Plan Factory')).toBeInTheDocument();
    expect(screen.getByText('open')).toBeInTheDocument();
  });
});
