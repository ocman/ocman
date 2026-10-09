// @vitest-environment jsdom
import { act, render, screen, within } from '@testing-library/react';
import { beforeEach, expect, it, vi } from 'vitest';
import { api, type RoutineStatsData } from '../lib/api';
import { RoutineStats } from './RoutineStats';

vi.mock('../lib/api', () => ({ api: { routines: { stats: vi.fn() } } }));
const stats: RoutineStatsData = { totalRuns: 60, states: { success: 40, failure: 20 }, averageDurationMs: 1500, totalCost: 1, totalEstCost: 2, costSessions: 2, missingSessions: 1 };
beforeEach(() => { vi.clearAllMocks(); vi.mocked(api.routines.stats).mockResolvedValue(stats); });

it('shows all-run totals, duration and clearly scoped session costs', async () => {
  render(<RoutineStats routineId="r" refreshKey={0} />);
  expect(await screen.findByRole('row', { name: 'Total runs 60' })).toBeInTheDocument();
  expect(screen.getByRole('row', { name: 'Average duration 1.5 s' })).toBeInTheDocument();
  expect(screen.getByRole('row', { name: 'Average session cost $0.5000' })).toBeInTheDocument();
  expect(screen.getByRole('row', { name: 'Running 0' })).toBeInTheDocument();
  expect(screen.getByText(/Reused sessions count once/)).toBeInTheDocument();
  expect(screen.getByRole('status')).toHaveTextContent('Costs unavailable for 1 linked sessions.');
});

it('does not report unavailable duration or costs as zero', async () => {
  vi.mocked(api.routines.stats).mockResolvedValue({ ...stats, averageDurationMs: null, costSessions: 0 });
  render(<RoutineStats routineId="r" refreshKey={0} />);
  expect(await screen.findByRole('row', { name: 'Average duration Unavailable' })).toBeInTheDocument();
  expect(within(screen.getByRole('row', { name: 'Average session cost Unavailable' })).getByText('Unavailable')).toBeInTheDocument();
});

it('shows errors and retries on refresh', async () => {
  vi.mocked(api.routines.stats).mockRejectedValueOnce(new Error('offline'));
  const view = render(<RoutineStats routineId="r" refreshKey={0} />);
  expect(await screen.findByRole('alert')).toHaveTextContent('offline');
  view.rerender(<RoutineStats routineId="r" refreshKey={1} />);
  await screen.findByText('Total runs');
  expect(screen.queryByRole('alert')).not.toBeInTheDocument();
});

it('keeps slow requests across refreshes and cancels on unmount', async () => {
  let finish!: (data: RoutineStatsData) => void;
  vi.mocked(api.routines.stats).mockReturnValue(new Promise((resolve) => { finish = resolve; }));
  const view = render(<RoutineStats routineId="r" refreshKey={0} />);
  expect(screen.getByText('Loading stats...')).toBeInTheDocument();
  view.rerender(<RoutineStats routineId="r" refreshKey={1} />);
  expect(api.routines.stats).toHaveBeenCalledTimes(1);
  const signal = vi.mocked(api.routines.stats).mock.calls[0][1]!;
  view.unmount();
  expect(signal.aborted).toBe(true);
  await act(async () => finish(stats));
  expect(screen.queryByText('Total runs')).not.toBeInTheDocument();
});
