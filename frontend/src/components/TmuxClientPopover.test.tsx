// @vitest-environment jsdom
import { describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import type { TmuxClient } from '../lib/api';
import { TmuxClientPopover } from './TmuxClientPopover';

describe('TmuxClientPopover', () => {
  it('lists clients and reports the selected tty', () => {
    const onSelect = vi.fn();
    const clients = [
      { tty: '/dev/ttys001', session: '/home/u/src/repo', width: '120', height: '40' },
    ] as TmuxClient[];
    render(<TmuxClientPopover pickerRef={{ current: null }} pos={{ top: 10, left: 20 }} clients={clients} onSelect={onSelect} />);
    expect(screen.getByText('Select tmux client')).toBeInTheDocument();
    expect(screen.getByText('120×40')).toBeInTheDocument();
    fireEvent.click(screen.getByText('/dev/ttys001'));
    expect(onSelect).toHaveBeenCalledWith('/dev/ttys001');
  });
  it('lets keyboard users select a client with Tab and Enter', async () => {
    const user = userEvent.setup();
    const onSelect = vi.fn();
    const clients = [
      { tty: '/dev/ttys001', session: '/home/u/src/repo', width: '120', height: '40' },
      { tty: '/dev/ttys002', session: '/home/u/src/other', width: '80', height: '24' },
    ] as TmuxClient[];
    render(<TmuxClientPopover pickerRef={{ current: null }} pos={{ top: 10, left: 900 }} clients={clients} onSelect={onSelect} />);
    await user.tab();
    expect(screen.getByRole('button', { name: /ttys001/ })).toHaveFocus();
    await user.tab();
    await user.keyboard('{Enter}');
    expect(onSelect).toHaveBeenCalledOnce();
    expect(onSelect).toHaveBeenCalledWith('/dev/ttys002');
  });
});
