// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { act, fireEvent, render, screen } from '@testing-library/react';
import { MaintenanceSettings } from './MaintenanceSettings';
import type { MaintenanceStatus, MaintenanceStep } from '../lib/maintenance';

vi.mock('../lib/maintenance', () => ({
  maintenance: { status: vi.fn(), cleanup: vi.fn(), restore: vi.fn(), deleteDump: vi.fn() },
}));
import { maintenance } from '../lib/maintenance';

const m = maintenance as unknown as Record<string, ReturnType<typeof vi.fn>>;

function status(over: Partial<MaintenanceStatus> = {}, job: Partial<MaintenanceStatus['job']> = {}): MaintenanceStatus {
  return {
    available: true,
    dbPath: '/home/u/opencode.db',
    dbBytes: 28.6e9,
    dumpPath: '/home/u/opencode.db.ocman-diffs',
    dumpBytes: 0,
    cutoffDays: 30,
    ...over,
    job: { running: false, steps: [], ...job },
  };
}

const steps = (states: MaintenanceStep['state'][]): MaintenanceStep[] =>
  states.map((state, i) => ({ name: `Step ${i}`, state, detail: state === 'failed' ? 'boom' : undefined }));

beforeEach(() => {
  vi.spyOn(window, 'confirm').mockReturnValue(true);
});
afterEach(() => {
  vi.clearAllMocks();
  vi.useRealTimers();
});

describe('MaintenanceSettings', () => {
  it('pauses job polling while hidden and refreshes on return', async () => {
    vi.useFakeTimers();
    let hidden = true;
    const visibility = vi.spyOn(document, 'hidden', 'get').mockImplementation(() => hidden);
    m.status.mockResolvedValue(status({}, { running: true }));
    const view = render(<MaintenanceSettings />);
    try {
      await act(async () => { await vi.advanceTimersByTimeAsync(10_000); });
      expect(m.status).not.toHaveBeenCalled();
      await act(async () => { hidden = false; document.dispatchEvent(new Event('visibilitychange')); });
      expect(m.status).toHaveBeenCalledTimes(1);
      act(() => { hidden = true; document.dispatchEvent(new Event('visibilitychange')); });
      await act(async () => { await vi.advanceTimersByTimeAsync(10_000); });
      expect(m.status).toHaveBeenCalledTimes(1);
    } finally { view.unmount(); visibility.mockRestore(); }
  });
  it('shows the database and disables restore without a dump', async () => {
    m.status.mockResolvedValue(status());
    render(<MaintenanceSettings />);
    expect(await screen.findByText('/home/u/opencode.db')).toBeVisible();
    expect(screen.getByText(/28\.6 GB/)).toBeVisible();
    expect(screen.getByText('No dump yet.')).toBeVisible();
    expect(screen.getByRole('button', { name: 'Restore' })).toBeDisabled();
    expect(screen.getByRole('button', { name: 'Delete dump' })).toBeDisabled();
  });

  it('explains when maintenance is unavailable', async () => {
    m.status.mockResolvedValue(status({ available: false }));
    render(<MaintenanceSettings />);
    expect(await screen.findByText(/needs the OpenCode platform/)).toBeVisible();
  });

  it('starts a cleanup after confirmation and polls until it finishes', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    m.status.mockResolvedValueOnce(status());
    m.cleanup.mockResolvedValue(status({}, { job: 'cleanup', running: true, steps: steps(['done', 'running', 'pending']) }));
    m.status.mockResolvedValue(status({ dumpBytes: 8e9 }, { job: 'cleanup', running: false, finishedAt: 'now', steps: steps(['done', 'done', 'done']) }));
    render(<MaintenanceSettings />);

    fireEvent.click(await screen.findByRole('button', { name: 'Clean up' }));
    expect(window.confirm).toHaveBeenCalledOnce();
    expect(await screen.findByRole('img', { name: 'running' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Clean up' })).toBeDisabled();

    await act(async () => { await vi.advanceTimersByTimeAsync(1100); });
    expect(await screen.findByText('Finished.')).toBeVisible();
    expect(screen.getAllByRole('img', { name: 'done' })).toHaveLength(3);
    expect(screen.getByRole('button', { name: 'Restore' })).toBeEnabled();
  });

  it('shows the clicked button busy while the request is in flight', async () => {
    let finish!: (s: MaintenanceStatus) => void;
    m.status.mockResolvedValue(status({ dumpBytes: 1e9 }));
    m.deleteDump.mockReturnValue(new Promise<MaintenanceStatus>((resolve) => { finish = resolve; }));
    render(<MaintenanceSettings />);
    fireEvent.click(await screen.findByRole('button', { name: 'Delete dump' }));
    const button = screen.getByRole('button', { name: 'Delete dump' });
    expect(button).toHaveAttribute('aria-busy', 'true');
    expect(button).toBeDisabled();

    await act(async () => { finish(status()); });
    expect(await screen.findByText('No dump yet.')).toBeVisible();
    expect(screen.getByRole('button', { name: 'Delete dump' })).toHaveAttribute('aria-busy', 'false');
  });

  it('does nothing when the confirmation is declined', async () => {
    vi.mocked(window.confirm).mockReturnValue(false);
    m.status.mockResolvedValue(status({ dumpBytes: 1e9 }));
    render(<MaintenanceSettings />);
    fireEvent.click(await screen.findByRole('button', { name: 'Delete dump' }));
    expect(m.deleteDump).not.toHaveBeenCalled();
  });

  it('shows a failed job and a rejected action', async () => {
    m.status.mockResolvedValue(status({ dumpBytes: 1e9 }, { job: 'cleanup', error: 'close opencode (pid 42)', finishedAt: 'now', steps: steps(['done', 'failed', 'skipped']) }));
    m.restore.mockRejectedValue(new Error('maintenance already running'));
    render(<MaintenanceSettings />);

    expect(await screen.findByText('close opencode (pid 42)')).toBeVisible();
    expect(screen.getByText(/boom/)).toBeVisible();
    fireEvent.click(screen.getByRole('button', { name: 'Restore' }));
    expect(await screen.findByText('maintenance already running')).toBeVisible();
  });

  it('shows a load error', async () => {
    m.status.mockRejectedValue(new Error('offline'));
    render(<MaintenanceSettings />);
    expect(await screen.findByRole('alert')).toHaveTextContent('offline');
  });
});
