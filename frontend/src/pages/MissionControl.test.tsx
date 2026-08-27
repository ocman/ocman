// @vitest-environment jsdom
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { useFactoryStatus } from '../lib/queries';
import { MissionControl } from './MissionControl';

vi.mock('../lib/queries', () => ({ useFactoryStatus: vi.fn() }));

const healthy = {
  health: 'healthy' as const,
  idle: true,
  dispatchOwner: true,
  readOnly: false,
  workEpicCount: 0,
  beads: { usable: true, version: '1.1.0', contractVersion: 1 },
};

describe('MissionControl', () => {
  beforeEach(() => vi.resetAllMocks());

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
    expect(screen.queryByText(/Workflows/)).not.toBeInTheDocument();
  });

  it('shows a healthy non-owner as read-only', () => {
    vi.mocked(useFactoryStatus).mockReturnValue({
      data: { ...healthy, dispatchOwner: false, readOnly: true },
    } as never);
    render(<MissionControl />);
    expect(screen.getByText('Another local process owns dispatch; this process is read-only.')).toBeInTheDocument();
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
