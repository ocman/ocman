// @vitest-environment jsdom
import { describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen } from '@testing-library/react';
import type { TmuxState } from '../../lib/useTmux';
import { TmuxClientPopover } from './TmuxClientPopover';

describe('TmuxClientPopover', () => {
  it('lists clients and reports the selected tty', () => {
    const onSelect = vi.fn();
    const clients = [
      { tty: '/dev/ttys001', session: '/home/u/src/repo', width: '120', height: '40' },
    ] as TmuxState['clients'];
    render(<TmuxClientPopover pickerRef={{ current: null }} pos={{ top: 10, left: 20 }} clients={clients} onSelect={onSelect} />);
    expect(screen.getByText('Select tmux client')).toBeInTheDocument();
    expect(screen.getByText('120×40')).toBeInTheDocument();
    fireEvent.click(screen.getByText('/dev/ttys001'));
    expect(onSelect).toHaveBeenCalledWith('/dev/ttys001');
  });
});
