// @vitest-environment jsdom
import { describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { sessionExportMarkdownUrl } from '../../lib/api';
import { SessionActionsMenu, type SessionActionsMenuProps } from './SessionActionsMenu';

function renderMenu(over: Partial<SessionActionsMenuProps> = {}) {
  const props: SessionActionsMenuProps = {
    sessionId: 'sess-1',
    tmuxAvailable: true,
    portAvailable: false,
    liveConnectionHint: 'Start opencode',
    launchingOpencode: false,
    onNewSession: vi.fn(),
    onShare: vi.fn(),
    onLaunchOpencode: vi.fn(),
    ...over,
  };
  render(<SessionActionsMenu {...props} />);
  return props;
}

function openMenu() {
  fireEvent.keyDown(screen.getByRole('button', { name: 'Session actions' }), { key: 'ArrowDown' });
}

describe('SessionActionsMenu', () => {
  it('does not offer tmux or VS Code actions', () => {
    renderMenu();
    openMenu();
    expect(screen.queryByRole('menuitem', { name: 'Switch tmux' })).not.toBeInTheDocument();
    expect(screen.queryByRole('menuitem', { name: 'Open in VS Code' })).not.toBeInTheDocument();
  });
  it('dispatches actions once and dismisses after each selection', async () => {
    const props = renderMenu();
    for (const [name, callback] of [
      ['New session', props.onNewSession],
      ['Share link…', props.onShare],
      ['Launch opencode', props.onLaunchOpencode],
    ] as const) {
      openMenu();
      fireEvent.click(screen.getByRole('menuitem', { name }));
      await waitFor(() => expect(callback).toHaveBeenCalledTimes(1));
      expect(screen.queryByRole('menu')).not.toBeInTheDocument();
    }
  });

  it('keeps the Markdown download URL and filename', () => {
    renderMenu();
    openMenu();
    const link = screen.getByRole('menuitem', { name: 'Download Markdown' });
    expect(link).toHaveAttribute('href', sessionExportMarkdownUrl('sess-1'));
    expect(link).toHaveAttribute('download', 'conversation-sess-1.md');
    fireEvent.click(link);
    expect(screen.queryByRole('menu')).not.toBeInTheDocument();
  });

  it.each([
    { tmuxAvailable: false },
    { portAvailable: true },
    { liveConnectionHint: undefined },
  ])('hides unavailable launch action: %j', (over) => {
    renderMenu(over);
    openMenu();
    expect(screen.queryByRole('menuitem', { name: 'Launch opencode' })).toBeNull();
  });

  it('does not dispatch the disabled launching item', () => {
    const props = renderMenu({ launchingOpencode: true });
    openMenu();
    const item = screen.getByRole('menuitem', { name: 'Launching…' });
    expect(item).toHaveAttribute('aria-disabled', 'true');
    fireEvent.click(item);
    expect(props.onLaunchOpencode).not.toHaveBeenCalled();
    expect(screen.getByRole('menu')).toBeInTheDocument();
  });

  it('supports keyboard selection and restores the trigger focus', async () => {
    const user = userEvent.setup();
    const onNewSession = vi.fn();
    renderMenu({ onNewSession });
    screen.getByRole('button', { name: 'Session actions' }).focus();
    await user.keyboard('{Enter}{Enter}');
    await waitFor(() => expect(onNewSession).toHaveBeenCalledTimes(1));
    await waitFor(() => expect(screen.getByRole('button', { name: 'Session actions' })).toHaveFocus());
  });

  it('dismisses before printing', async () => {
    const print = vi.spyOn(window, 'print').mockImplementation(() => {
      expect(screen.queryByRole('menu')).not.toBeInTheDocument();
    });
    renderMenu();
    openMenu();
    fireEvent.click(screen.getByRole('menuitem', { name: 'Print / Save as PDF' }));
    await waitFor(() => expect(print).toHaveBeenCalledOnce());
    print.mockRestore();
  });
});
