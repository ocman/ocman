// @vitest-environment jsdom
import { useState } from 'react';
import { describe, expect, it } from 'vitest';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { KeyboardShortcutsDialog } from './KeyboardShortcutsDialog';

function Example() {
  const [open, setOpen] = useState(false);
  return <><button onClick={() => setOpen(true)}>Show shortcuts</button><KeyboardShortcutsDialog open={open} onClose={() => setOpen(false)} /></>;
}

describe('KeyboardShortcutsDialog', () => {
  it.each(['button', 'Escape'])('closes via %s and returns focus to its trigger', async (method) => {
    const user = userEvent.setup();
    render(<Example />);
    const trigger = screen.getByRole('button', { name: 'Show shortcuts' });
    await user.click(trigger);
    expect(screen.getByRole('dialog', { name: 'Keyboard shortcuts' })).toBeInTheDocument();
    expect(screen.getByRole('heading', { name: 'Keyboard shortcuts' })).toBeInTheDocument();
    expect(screen.getByText('Site-wide shortcuts and actions available on the current page.')).toBeInTheDocument();
    const close = screen.getByRole('button', { name: 'Close keyboard shortcuts' });
    expect(close).toHaveFocus();
    await user.tab();
    expect(close).toHaveFocus();
    if (method === 'button') await user.click(close);
    else await user.keyboard('{Escape}');
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    expect(trigger).toHaveFocus();
  });
});
