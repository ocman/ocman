// @vitest-environment jsdom
import { describe, expect, it, vi, beforeEach } from 'vitest';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router-dom';
import { api, type Session } from '../lib/api';
import { SnoozeButton } from './SnoozeButton';
import { snoozeDeadline, SNOOZE_OPTIONS } from '../lib/snooze';

vi.mock('../lib/api', () => ({ api: { snoozeSession: vi.fn() } }));
const patchRecentSession = vi.fn();
vi.mock('../lib/apiStore', () => ({ useApiStore: { getState: () => ({ patchRecentSession }) } }));
const session = { id: 's1', platform: 'r-box:claudecode' } as Session;

beforeEach(() => { vi.clearAllMocks(); vi.mocked(api.snoozeSession).mockResolvedValue({ ok: true }); });

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
    expect(patchRecentSession).toHaveBeenCalledWith('s1', { archived: true });
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
    expect(patchRecentSession).not.toHaveBeenCalled();
    await user.click(screen.getByRole('button', { name: 'Snooze' }));
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument());
    expect(api.snoozeSession).toHaveBeenLastCalledWith(session.platform, 's1', new Date('2099-01-02T14:30').getTime());
  });
});
