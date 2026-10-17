// @vitest-environment jsdom
import { describe, expect, it, vi, beforeEach } from 'vitest';
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter, useLocation, useNavigate } from 'react-router-dom';
import { api, type Session } from '../lib/api';
import { SnoozeButton } from './SnoozeButton';
import { snoozeDeadline, SNOOZE_OPTIONS } from '../lib/snooze';
import { SIDEBAR_SESSIONS_KEY, getSidebarSessions, patchSessionLists } from '../lib/sessionsCache';
import { queryClient } from '../lib/queryClient';

vi.mock('../lib/api', () => ({ api: { snoozeSession: vi.fn() } }));
const session = { id: 's1', platform: 'r-box:claudecode' } as Session;
const localSession = { ...session, platform: 'opencode', archived: false };

beforeEach(() => {
  vi.clearAllMocks();
  vi.mocked(api.snoozeSession).mockResolvedValue({ ok: true });
  queryClient.setQueryData(SIDEBAR_SESSIONS_KEY, [localSession, { ...session, archived: false }]);
});

describe('snooze deadlines', () => {
  it('uses exact durations and local 9 AM, including the next Monday', () => {
    const now = new Date(2026, 9, 12, 12, 30);
    expect([0, 1, 2].map((index) => snoozeDeadline(index, now) - now.getTime())).toEqual([30, 60, 180].map((minutes) => minutes * 60_000));
    expect(new Date(snoozeDeadline(3, now))).toEqual(new Date(2026, 9, 13, 9));
    expect(new Date(snoozeDeadline(4, now))).toEqual(new Date(2026, 9, 19, 9));
    expect(new Date(snoozeDeadline(4, new Date(2026, 9, 18, 20)))).toEqual(new Date(2026, 9, 19, 9));
  });
});

describe('SnoozeButton', () => {
  it('opens the duration picker from a swipe and reports pending delivery', async () => {
    let finish!: (value: { ok: boolean }) => void;
    vi.mocked(api.snoozeSession).mockReturnValue(new Promise(resolve => { finish = resolve; }));
    const onOpenChange = vi.fn();
    const onBusyChange = vi.fn();
    render(<SnoozeButton session={session} open onOpenChange={onOpenChange} onBusyChange={onBusyChange} />);
    await userEvent.click(screen.getByRole('menuitem', { name: 'In 1 hour' }));
    expect(onOpenChange).toHaveBeenCalledWith(false);
    expect(onBusyChange).toHaveBeenCalledWith(true);
    await act(async () => { finish({ ok: true }); });
    expect(onBusyChange).toHaveBeenLastCalledWith(false);
  });

  function setup() {
    const click = vi.fn();
    render(<MemoryRouter><div onClick={click}><SnoozeButton session={session} /></div></MemoryRouter>);
    return { user: userEvent.setup(), click };
  }

  it('offers every preset and snoozes without opening the row', async () => {
    const { user, click } = setup();
    await user.click(screen.getByRole('button', { name: 'Snooze conversation' }));
    for (const label of SNOOZE_OPTIONS) expect(screen.getByRole('menuitem', { name: label })).toBeInTheDocument();
    await user.click(screen.getByRole('menuitem', { name: 'In 30 mins' }));
    await waitFor(() => expect(api.snoozeSession).toHaveBeenCalledWith(session.platform, 's1', expect.any(Number)));
    // State is delivered by SSE, not overwritten by the unordered HTTP response.
    expect(getSidebarSessions().find((row) => row.platform === session.platform)?.archived).toBe(false);
    expect(getSidebarSessions().find((row) => row.platform === localSession.platform)?.archived).toBe(false);
    expect(click).not.toHaveBeenCalled();
  });

  it('uses a custom date and time, rejects past times and shows request errors', async () => {
    const { user } = setup();
    await user.click(screen.getByRole('button', { name: 'Snooze conversation' }));
    await user.click(screen.getByRole('menuitem', { name: 'Custom...' }));
    const input = screen.getByLabelText('Wake up at');
    expect(input).toHaveAttribute('type', 'datetime-local');
    const submit = screen.getByRole('button', { name: 'Snooze' });
    expect(submit).toBeDisabled();
    fireEvent.change(input, { target: { value: '2020-01-01T09:00' } });
    expect(submit).toBeDisabled();
    fireEvent.change(input, { target: { value: '2099-01-02T14:30' } });
    vi.mocked(api.snoozeSession).mockRejectedValueOnce(new Error('Offline'));
    await user.click(submit);
    expect(await screen.findByRole('alert')).toHaveTextContent('Offline');
    expect(getSidebarSessions().every((row) => !row.archived)).toBe(true);
    await user.click(screen.getByRole('button', { name: 'Snooze' }));
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument());
    expect(api.snoozeSession).toHaveBeenLastCalledWith(session.platform, 's1', new Date('2099-01-02T14:30').getTime());
  });

  it.each(['/session/next', '/session/selected'])('preserves navigation to %s while the snooze response is pending', async (destination) => {
    let resolve!: (value: { ok: boolean }) => void;
    vi.mocked(api.snoozeSession).mockReturnValue(new Promise((done) => { resolve = done; }));
    function Harness() {
      const navigate = useNavigate();
      const location = useLocation();
      return <><SnoozeButton session={session} /><button onClick={() => navigate(destination)}>Switch conversation</button><output>{location.pathname}</output></>;
    }
    const user = userEvent.setup();
    render(<MemoryRouter initialEntries={['/session/s1']}><Harness /></MemoryRouter>);
    await user.click(screen.getByRole('button', { name: 'Snooze conversation' }));
    await user.click(screen.getByRole('menuitem', { name: 'In 30 mins' }));
    expect(screen.getByRole('button', { name: 'Snooze conversation' })).toBeDisabled();
    await user.click(screen.getByRole('button', { name: 'Switch conversation' }));
    await act(async () => { resolve({ ok: true }); });
    expect(screen.getByRole('status')).toHaveTextContent(destination);
  });

  it('preserves a cancellation received before the snooze response', async () => {
    let resolve!: (value: { ok: boolean }) => void;
    vi.mocked(api.snoozeSession).mockReturnValue(new Promise((done) => { resolve = done; }));
    const { user } = setup();
    await user.click(screen.getByRole('button', { name: 'Snooze conversation' }));
    await user.click(screen.getByRole('menuitem', { name: 'In 30 mins' }));
    act(() => {
      patchSessionLists(session.id, { archived: true }, session.platform);
      patchSessionLists(session.id, { archived: false }, session.platform);
    });
    await act(async () => { resolve({ ok: true }); });
    expect(getSidebarSessions().every((row) => !row.archived)).toBe(true);
  });

  it('dismisses the custom dialog with Escape without activating its row', async () => {
    const { user, click } = setup();
    await user.click(screen.getByRole('button', { name: 'Snooze conversation' }));
    await user.click(screen.getByRole('menuitem', { name: 'Custom...' }));
    expect(screen.getByRole('dialog')).toBeInTheDocument();
    await user.keyboard('{Escape}');
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    expect(click).not.toHaveBeenCalled();
    expect(api.snoozeSession).not.toHaveBeenCalled();
  });
});
