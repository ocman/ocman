// @vitest-environment jsdom
import { act, render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router-dom';
import { beforeEach, expect, it, vi } from 'vitest';

const { apiMock } = vi.hoisted(() => ({
  apiMock: {
    list: vi.fn(),
    create: vi.fn(),
    get: vi.fn(),
    cancel: vi.fn(),
    runNow: vi.fn(),
    setEnabled: vi.fn(),
  },
}));

vi.mock('../lib/api', () => ({ api: { promptSchedules: apiMock } }));

import { PromptSchedules } from './PromptSchedules';

const scheduled = {
  id: 'ps_old', directory: '/repo', remoteId: 'local', prompt: 'inspect me', state: 'scheduled' as const,
  runAt: Date.parse('2030-01-02T03:04:00Z'), createdAt: 1, updatedAt: 1,
  timingType: 'once' as const, timezone: 'UTC', enabled: true, sessionMode: 'fresh' as const,
};

beforeEach(() => {
  vi.clearAllMocks();
  apiMock.list.mockResolvedValue([scheduled]);
});

it('covers create, list, inspect, cancel, run-now, and linked-session lifecycle', async () => {
  const user = userEvent.setup();
  const created = { ...scheduled, id: 'ps_new', prompt: ' exact\n', runAt: Date.parse('2031-02-03T04:05:00Z') };
  apiMock.create.mockResolvedValue(created);
  apiMock.cancel.mockResolvedValue({ ...scheduled, state: 'canceled' });
  apiMock.runNow.mockResolvedValue({
    ...created, state: 'completed', platform: 'opencode', sessionId: 'session 1', startedAt: 2, finishedAt: 3,
  });

  render(<MemoryRouter><PromptSchedules directory="/repo" /></MemoryRouter>);

  expect(await screen.findByText('inspect me')).toBeInTheDocument();
  await user.type(screen.getByLabelText('Scheduled prompt'), ' exact{enter}');
  await user.type(screen.getByLabelText('Run at'), '2031-02-03T04:05');
  await user.click(screen.getByRole('button', { name: 'Schedule prompt' }));
  await waitFor(() => expect(apiMock.create).toHaveBeenCalledWith(expect.objectContaining({
    directory: '/repo', remoteId: 'local', prompt: ' exact\n', runAt: Date.parse('2031-02-03T04:05'),
  })));

  const createdRow = screen.getAllByText('scheduled')[0].closest('article');
  if (!createdRow) throw new Error('created schedule row missing');
  await user.click(within(createdRow).getByRole('button', { name: 'Run now' }));
  expect(await screen.findByRole('link', { name: 'Open session' })).toHaveAttribute('href', '/session/session%201?platform=opencode');

  const oldRow = screen.getByText('inspect me').closest('article');
  if (!oldRow) throw new Error('existing schedule row missing');
  await user.click(within(oldRow).getByRole('button', { name: 'Cancel' }));
  await waitFor(() => expect(apiMock.cancel).toHaveBeenCalledWith('ps_old'));
  expect(screen.getByText('canceled')).toBeInTheDocument();
});

it('surfaces load and mutation failures', async () => {
  apiMock.list.mockRejectedValueOnce(new Error('load failed'));
  apiMock.create.mockRejectedValueOnce(new Error('create failed'));
  const user = userEvent.setup();
  render(<MemoryRouter><PromptSchedules directory="/repo" /></MemoryRouter>);
  expect(await screen.findByRole('alert')).toHaveTextContent('load failed');
  await user.type(screen.getByLabelText('Scheduled prompt'), 'try later');
  await user.type(screen.getByLabelText('Run at'), '2031-02-03T04:05');
  await user.click(screen.getByRole('button', { name: 'Schedule prompt' }));
  expect(await screen.findByRole('alert')).toHaveTextContent('create failed');
  expect(screen.getByRole('button', { name: 'Schedule prompt' })).toBeEnabled();
});

it('refreshes background execution state', async () => {
  vi.useFakeTimers();
  apiMock.list
    .mockResolvedValueOnce([scheduled])
    .mockResolvedValueOnce([{ ...scheduled, state: 'completed', platform: 'opencode', sessionId: 'background-session' }]);
  render(<MemoryRouter><PromptSchedules directory="/repo" /></MemoryRouter>);
  await act(async () => {});
  await act(async () => { vi.advanceTimersByTime(5000); });
  expect(screen.getByRole('link', { name: 'Open session' })).toHaveAttribute('href', '/session/background-session?platform=opencode');
  vi.useRealTimers();
});

it('creates recurring schedules and toggles enabled state', async () => {
  const user = userEvent.setup();
  const recurring = {
    ...scheduled, id: 'ps_recurring', prompt: 'repeat', timingType: 'interval' as const, intervalMinutes: 15,
    timezone: 'Europe/Brussels', enabled: true, sessionMode: 'reuse' as const,
  };
  apiMock.create.mockResolvedValue(recurring);
  apiMock.setEnabled.mockResolvedValue({ ...recurring, enabled: false });
  render(<MemoryRouter><PromptSchedules directory="/repo" /></MemoryRouter>);

  await user.type(screen.getByLabelText('Scheduled prompt'), 'repeat');
  await user.selectOptions(screen.getByLabelText('Timing'), 'interval');
  await user.clear(screen.getByLabelText('Every (minutes)'));
  await user.type(screen.getByLabelText('Every (minutes)'), '15');
  await user.selectOptions(screen.getByLabelText('Session mode'), 'reuse');
  await user.click(screen.getByRole('button', { name: 'Schedule prompt' }));
  await waitFor(() => expect(apiMock.create).toHaveBeenCalledWith(expect.objectContaining({
    timingType: 'interval', intervalMinutes: 15, sessionMode: 'reuse', timezone: expect.any(String),
  })));

  const row = screen.getByText('repeat').closest('article');
  if (!row) throw new Error('recurring schedule row missing');
  await user.click(within(row).getByRole('button', { name: 'Disable' }));
  expect(apiMock.setEnabled).toHaveBeenCalledWith('ps_recurring', false);
});

it('retries a failed recurring schedule even when it is still enabled', async () => {
  const user = userEvent.setup();
  const failed = {
    ...scheduled, id: 'ps_failed', timingType: 'interval' as const, intervalMinutes: 15,
    state: 'failed' as const, enabled: true, error: 'remote unavailable',
  };
  apiMock.list.mockResolvedValue([failed]);
  apiMock.setEnabled.mockResolvedValue({ ...failed, state: 'scheduled', error: undefined });
  render(<MemoryRouter><PromptSchedules directory="/repo" /></MemoryRouter>);

  await user.click(await screen.findByRole('button', { name: 'Retry schedule' }));
  expect(apiMock.setEnabled).toHaveBeenCalledWith('ps_failed', true);
  expect(screen.getByText('scheduled')).toBeInTheDocument();
});

it('renders the next occurrence in the configured timezone', async () => {
  const runAt = Date.parse('2030-01-02T03:04:00Z');
  apiMock.list.mockResolvedValue([{ ...scheduled, runAt, timezone: 'America/New_York' }]);
  render(<MemoryRouter><PromptSchedules directory="/repo" /></MemoryRouter>);
  expect(await screen.findByText(`Next: ${new Date(runAt).toLocaleString(undefined, { timeZone: 'America/New_York' })}`)).toBeInTheDocument();
});
