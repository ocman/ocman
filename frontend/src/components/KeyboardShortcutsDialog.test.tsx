// @vitest-environment jsdom
import { useState } from 'react';
import { describe, expect, it } from 'vitest';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { KeyboardShortcutsDialog } from './KeyboardShortcutsDialog';
import { useShortcut } from '../lib/shortcutRegistry';

function RegisteredShortcuts() {
  useShortcut({ id: 'test.site', scope: 'site', keys: { code: 'Space', alt: true }, description: 'Open palette', handler: () => {} });
  useShortcut({ id: 'test.session', scope: 'session', keys: [{ code: 'KeyJ', alt: true }, { code: 'ArrowDown' }], description: 'Next session', handler: () => {} });
  return <Example />;
}

function Example() {
  const [open, setOpen] = useState(false);
  return <><button onClick={() => setOpen(true)}>Show shortcuts</button><KeyboardShortcutsDialog open={open} onClose={() => setOpen(false)} /></>;
}

describe('KeyboardShortcutsDialog', () => {
  it('shows registered shortcuts by scope and closes without a separate hotkey handler', async () => {
    const user = userEvent.setup();
    render(<RegisteredShortcuts />);
    await user.click(screen.getByRole('button', { name: 'Show shortcuts' }));
    expect(screen.getByRole('heading', { name: 'Session' })).toBeInTheDocument();
    expect(screen.getByRole('heading', { name: 'Site-wide shortcuts' })).toBeInTheDocument();
    expect(screen.getByText('Open palette')).toBeInTheDocument();
    expect(screen.getByText('Next session')).toBeInTheDocument();
    expect(screen.getByText('J', { selector: 'kbd' })).toBeInTheDocument();
    await user.keyboard('{Escape}');
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
  });
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
