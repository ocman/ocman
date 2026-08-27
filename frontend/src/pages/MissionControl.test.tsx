// @vitest-environment jsdom
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { useAddFactoryPlanningWork, useCreateWorkEpic, useDecideFactoryPlan, useFactoryStatus, useMutateFactoryPlan, useWorkEpics } from '../lib/queries';
import { MissionControl } from './MissionControl';

vi.mock('../lib/queries', () => ({
  useFactoryStatus: vi.fn(),
  useWorkEpics: vi.fn(),
  useCreateWorkEpic: vi.fn(),
	useMutateFactoryPlan: vi.fn(),
	useAddFactoryPlanningWork: vi.fn(),
	useDecideFactoryPlan: vi.fn(),
}));

const healthy = {
  health: 'healthy' as const,
  idle: true,
  dispatchOwner: true,
  readOnly: false,
  workEpicCount: 0,
  beads: { usable: true, version: '1.1.0', contractVersion: 1 },
};

describe('MissionControl', () => {
  const mutateAsync = vi.fn();

  beforeEach(() => {
    vi.resetAllMocks();
    vi.mocked(useWorkEpics).mockReturnValue({ data: [] } as never);
    vi.mocked(useCreateWorkEpic).mockReturnValue({ mutateAsync, isPending: false } as never);
		vi.mocked(useMutateFactoryPlan).mockReturnValue({ mutateAsync: vi.fn(), isPending: false } as never);
		vi.mocked(useAddFactoryPlanningWork).mockReturnValue({ mutateAsync: vi.fn(), isPending: false } as never);
		vi.mocked(useDecideFactoryPlan).mockReturnValue({ mutateAsync: vi.fn(), isPending: false } as never);
  });
  afterEach(() => vi.unstubAllGlobals());

  it('shows loading before Factory status arrives', () => {
    vi.mocked(useFactoryStatus).mockReturnValue({ isLoading: true } as never);
    render(<MissionControl />);
    expect(screen.getByRole('status')).toHaveTextContent('Loading Factory status');
  });

  it('shows a retryable transport failure', async () => {
    const refetch = vi.fn();
    vi.mocked(useFactoryStatus).mockReturnValue({
      isLoading: false,
      isError: true,
      error: new Error('connection refused'),
      refetch,
    } as never);
    render(<MissionControl />);

    expect(screen.getByRole('alert')).toHaveTextContent('connection refused');
    await userEvent.click(screen.getByRole('button', { name: 'Retry' }));
    expect(refetch).toHaveBeenCalledOnce();
  });

  it('shows healthy ownership and an explicit empty state', () => {
    vi.mocked(useFactoryStatus).mockReturnValue({ data: healthy } as never);
    render(<MissionControl />);

    expect(screen.getByRole('heading', { name: 'Mission Control' })).toBeInTheDocument();
    expect(screen.getByText('Healthy · idle')).toBeInTheDocument();
    expect(screen.getByText('This process owns dispatch.')).toBeInTheDocument();
    expect(screen.getByText('No Work Epics yet')).toBeInTheDocument();
    expect(screen.getByRole('form')).toBeInTheDocument();
    expect(screen.queryByText(/Workflows/)).not.toBeInTheDocument();
  });

  it('shows a healthy non-owner as read-only', () => {
    vi.mocked(useFactoryStatus).mockReturnValue({
      data: { ...healthy, dispatchOwner: false, readOnly: true },
    } as never);
    render(<MissionControl />);
    expect(screen.getByText('Another local process owns dispatch; this process is read-only.')).toBeInTheDocument();
    expect(screen.queryByRole('form')).not.toBeInTheDocument();
  });

  it('creates from the shipped Formula with one UUID and local execution acknowledgement', async () => {
    mutateAsync.mockResolvedValue({ id: 'epic-1' });
    vi.stubGlobal('crypto', { randomUUID: vi.fn(() => 'request-1') });
    vi.mocked(useFactoryStatus).mockReturnValue({ data: healthy } as never);
    render(<MissionControl />);

    expect(screen.getByLabelText('Shipped Formula')).toHaveValue('ocman/default v1');
    expect(screen.getByLabelText('Shipped Formula')).toHaveAttribute('readonly');
    await userEvent.type(screen.getByLabelText('Goal'), 'Ship the factory');
    await userEvent.type(screen.getByLabelText('Initial project'), '/repos/ocman');
    await userEvent.click(screen.getByRole('checkbox', { name: /executes locally without isolation/i }));
    await userEvent.click(screen.getByRole('button', { name: 'Create Work Epic' }));

    expect(mutateAsync).toHaveBeenCalledWith({
      instantiationId: 'request-1',
      goal: 'Ship the factory',
      initialProject: '/repos/ocman',
      acknowledgeLocalExecution: true,
    });
    expect(crypto.randomUUID).toHaveBeenCalledOnce();
  });

  it('reuses an instantiation ID after failure and replaces it when inputs change', async () => {
    mutateAsync
      .mockRejectedValueOnce(new Error('response lost'))
      .mockRejectedValueOnce(new Error('response lost'))
      .mockResolvedValueOnce({ id: 'epic-1' });
    const randomUUID = vi.fn()
      .mockReturnValueOnce('request-1')
      .mockReturnValueOnce('request-2');
    vi.stubGlobal('crypto', { randomUUID });
    vi.mocked(useFactoryStatus).mockReturnValue({ data: healthy } as never);
    render(<MissionControl />);

    await userEvent.type(screen.getByLabelText('Goal'), ' Ship it ');
    await userEvent.type(screen.getByLabelText('Initial project'), '/repo ');
    await userEvent.click(screen.getByRole('checkbox'));
    const submit = screen.getByRole('button', { name: 'Create Work Epic' });
    await userEvent.click(submit);
    await waitFor(() => expect(mutateAsync).toHaveBeenCalledTimes(1));
    await userEvent.click(submit);
    await waitFor(() => expect(mutateAsync).toHaveBeenCalledTimes(2));

    await userEvent.clear(screen.getByLabelText('Goal'));
    await userEvent.type(screen.getByLabelText('Goal'), 'Ship something else');
    await userEvent.click(submit);
    await waitFor(() => expect(mutateAsync).toHaveBeenCalledTimes(3));

    expect(mutateAsync.mock.calls.map(([request]) => request.instantiationId)).toEqual([
      'request-1', 'request-1', 'request-2',
    ]);
    expect(mutateAsync.mock.calls[0][0]).toMatchObject({ goal: 'Ship it', initialProject: '/repo' });
    expect(randomUUID).toHaveBeenCalledTimes(2);
  });

  it('rejects a relative initial project before mutation', async () => {
    vi.mocked(useFactoryStatus).mockReturnValue({ data: healthy } as never);
    render(<MissionControl />);
    await userEvent.type(screen.getByLabelText('Goal'), 'Ship it');
    await userEvent.type(screen.getByLabelText('Initial project'), 'repos/ocman');
    await userEvent.click(screen.getByRole('checkbox'));
    await userEvent.click(screen.getByRole('button', { name: 'Create Work Epic' }));

    expect(screen.getByRole('alert')).toHaveTextContent('Initial project must be an absolute path.');
    expect(mutateAsync).not.toHaveBeenCalled();
  });

  it('lists an epic with its initial planning and approval states', () => {
    vi.mocked(useFactoryStatus).mockReturnValue({ data: { ...healthy, workEpicCount: 1 } } as never);
    vi.mocked(useWorkEpics).mockReturnValue({ data: [{
      id: 'epic-1', status: 'open', goal: 'Ship it', initialProject: '/repo',
      formulaId: 'ocman/default', formulaVersion: 1, instantiationId: 'request-1',
      planning: { workId: 'work-1', workStatus: 'open', approvalGateId: 'gate-1', approvalStatus: 'pending' },
    }] } as never);
    render(<MissionControl />);

    const article = screen.getByRole('article', { name: 'Ship it' });
    expect(within(article).getByText('Initial project').nextSibling).toHaveTextContent('/repo');
    expect(within(article).getByText('Planning Work status').nextSibling).toHaveTextContent('open');
    expect(within(article).getByText('Plan approval Gate status').nextSibling).toHaveTextContent('pending');
  });

	it('shows exact Plan evidence and only enables approval after validation passes', async () => {
		const decidePlan = vi.fn().mockResolvedValue({});
		vi.mocked(useFactoryStatus).mockReturnValue({ data: healthy } as never);
		vi.mocked(useDecideFactoryPlan).mockReturnValue({ mutateAsync: decidePlan, isPending: false } as never);
		vi.mocked(useWorkEpics).mockReturnValue({ data: [{
			id: 'epic-1', status: 'open', goal: 'Ship it', initialProject: '/repo', formulaId: 'ocman/default', formulaVersion: 1, instantiationId: 'request-1',
			planning: { workId: 'work-1', workStatus: 'closed', approvalGateId: 'gate-1', approvalStatus: 'open' },
			plan: {
				revision: 4, hash: '1234567890abcdef', state: 'draft', validation: [],
				graph: { intent: 'Ship it', targets: [], items: [], dependencies: [] },
				planning: [{ id: 'work-1', targetId: 'app', repository: '/repo', status: 'closed', session: { platform: 'agent', id: 'session-1' } }],
			},
		}] } as never);
		render(<MissionControl />);

		expect(screen.getByText(/revision 4/)).toHaveTextContent('1234567890ab');
		expect(screen.getByRole('list', { name: 'Planning Sessions' })).toHaveTextContent('/repo: closed · session session-1');
		await userEvent.click(screen.getByRole('checkbox', { name: /approved work executes locally/i }));
		await userEvent.click(screen.getByRole('button', { name: 'Approve exact revision' }));
		expect(decidePlan).toHaveBeenCalledWith({ action: 'approve', request: { expectedRevision: 4, expectedHash: '1234567890abcdef', actor: 'operator', acknowledgeLocalExecution: true } });
	});

	it('edits the whole draft and adds repository-scoped Planning Work', async () => {
		const mutatePlan = vi.fn().mockResolvedValue({});
		const addPlanning = vi.fn().mockResolvedValue({});
		vi.mocked(useFactoryStatus).mockReturnValue({ data: healthy } as never);
		vi.mocked(useMutateFactoryPlan).mockReturnValue({ mutateAsync: mutatePlan, isPending: false } as never);
		vi.mocked(useAddFactoryPlanningWork).mockReturnValue({ mutateAsync: addPlanning, isPending: false } as never);
		vi.mocked(useWorkEpics).mockReturnValue({ data: [{
			id: 'epic-1', status: 'open', goal: 'Ship', initialProject: '/repo', formulaId: 'ocman/default', formulaVersion: 1, instantiationId: 'request-1',
			planning: { workId: 'work-1', workStatus: 'open', approvalGateId: 'gate-1', approvalStatus: 'open' },
			plan: { revision: 2, hash: 'hash-2', state: 'draft', graph: { intent: 'Ship', targets: [], items: [], dependencies: [] }, planning: [], validation: ['incomplete'] },
		}] } as never);
		render(<MissionControl />);

		const graph = screen.getByLabelText('Draft graph for epic-1');
		fireEvent.change(graph, { target: { value: JSON.stringify({ intent: 'Changed', targets: [], items: [], dependencies: [] }) } });
		await userEvent.click(screen.getByRole('button', { name: 'Save graph' }));
		expect(mutatePlan).toHaveBeenCalledWith({ expectedRevision: 2, graph: { intent: 'Changed', targets: [], items: [], dependencies: [] } });

		await userEvent.type(screen.getByLabelText('Target ID'), 'api');
		await userEvent.type(screen.getByLabelText('Target repository'), '/repo/api');
		await userEvent.type(screen.getByLabelText('Delivery remote'), 'origin');
		await userEvent.type(screen.getByLabelText('Delivery base branch'), 'main');
		await userEvent.type(screen.getByLabelText('Delivery base SHA'), 'abc123');
		await userEvent.click(screen.getByRole('checkbox', { name: /Planning Work executes locally/i }));
		await userEvent.click(screen.getByRole('button', { name: 'Add Planning Work' }));
		expect(addPlanning).toHaveBeenCalledWith({ expectedRevision: 2, target: { id: 'api', hostId: 'local', repository: '/repo/api', deliveryBase: { remote: 'origin', baseBranch: 'main', baseSha: 'abc123' } } });
		expect(screen.getByRole('button', { name: 'Approve exact revision' })).toBeDisabled();
	});

  it('keeps stale epics visible with a retry action', async () => {
    const refetch = vi.fn();
    vi.mocked(useFactoryStatus).mockReturnValue({ data: healthy } as never);
    vi.mocked(useWorkEpics).mockReturnValue({
      data: [{
        id: 'epic-1', status: 'open', goal: 'Cached epic', initialProject: '/repo',
        formulaId: 'ocman/default', formulaVersion: 1, instantiationId: 'request-1',
        planning: { workId: 'work-1', workStatus: 'open', approvalGateId: 'gate-1', approvalStatus: 'pending' },
      }],
      isError: true,
      error: new Error('refresh failed'),
      refetch,
    } as never);
    render(<MissionControl />);

    expect(screen.getByRole('article', { name: 'Cached epic' })).toBeInTheDocument();
    expect(screen.getByRole('alert')).toHaveTextContent('Work Epics may be stale: refresh failed');
    await userEvent.click(screen.getByRole('button', { name: 'Retry Work Epics' }));
    expect(refetch).toHaveBeenCalledOnce();
  });

  it('shows loading and retryable failure states for Work Epics', async () => {
    vi.mocked(useFactoryStatus).mockReturnValue({ data: healthy } as never);
    vi.mocked(useWorkEpics).mockReturnValue({ isLoading: true } as never);
    const { rerender } = render(<MissionControl />);
    expect(within(screen.getByRole('region', { name: 'Work Epics' })).getByRole('status')).toHaveTextContent('Loading Work Epics');

    const refetch = vi.fn();
    vi.mocked(useWorkEpics).mockReturnValue({
      isError: true,
      error: new Error('epics unavailable'),
      refetch,
    } as never);
    rerender(<MissionControl />);
    expect(screen.getByRole('alert')).toHaveTextContent('epics unavailable');
    await userEvent.click(screen.getByRole('button', { name: 'Retry Work Epics' }));
    expect(refetch).toHaveBeenCalledOnce();
  });

  it('shows pending and create error states', () => {
    vi.mocked(useFactoryStatus).mockReturnValue({ data: healthy } as never);
    vi.mocked(useCreateWorkEpic).mockReturnValue({
      mutateAsync,
      isPending: true,
      isError: true,
      error: new Error('creation failed'),
    } as never);
    render(<MissionControl />);

    expect(screen.getByRole('button', { name: 'Creating…' })).toBeDisabled();
    expect(screen.getByRole('alert')).toHaveTextContent('creation failed');
  });

  it('keeps cached status visible while warning that refresh failed', () => {
    vi.mocked(useFactoryStatus).mockReturnValue({
      data: healthy,
      isError: true,
      error: new Error('refresh failed'),
      refetch: vi.fn(),
    } as never);
    render(<MissionControl />);

    expect(screen.getByText('No Work Epics yet')).toBeInTheDocument();
    expect(screen.getByRole('alert')).toHaveTextContent('Factory status may be stale: refresh failed');
  });

  it.each([
    ['unavailable', 'Beads 1.2.0 is unsupported; install version >=1.1.0 and <1.2.0.'],
    ['degraded', 'Factory Beads store is unavailable; verify its data directory and run bd status.'],
  ] as const)('shows actionable %s health', (health, message) => {
    vi.mocked(useFactoryStatus).mockReturnValue({
      data: {
        ...healthy,
        health,
        idle: false,
        beads: { usable: false, reason: 'beads_problem', message },
        reason: 'beads_problem',
        message,
      },
    } as never);
    render(<MissionControl />);

    expect(screen.getByRole('alert')).toHaveTextContent(message);
    expect(screen.queryByText('No Work Epics yet')).not.toBeInTheDocument();
  });
});
