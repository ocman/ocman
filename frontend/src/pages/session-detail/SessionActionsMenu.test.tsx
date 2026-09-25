// @vitest-environment jsdom
import { describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import type { TmuxSession } from '../../lib/api';
import { sessionExportMarkdownUrl } from '../../lib/api';
import { SessionActionsMenu, type SessionActionsMenuProps } from './SessionActionsMenu';

function renderMenu(over: Partial<SessionActionsMenuProps> = {}) {
  const props: SessionActionsMenuProps = {
    sessionId: 'sess-1',
    tmuxAvailable: true,
    matchingTmuxSession: { name: '~/src/repo' } as TmuxSession,
    portAvailable: false,
    liveConnectionHint: 'Start opencode',
    launchingOpencode: false,
    onNewSession: vi.fn(),
    onShare: vi.fn(),
    onTmuxSwitch: vi.fn(),
    onLaunchOpencode: vi.fn(),
    onOpenVSCode: vi.fn(),
    ...over,
  };
  render(<SessionActionsMenu {...props} />);
  return props;
}

function openMenu() {
  fireEvent.keyDown(screen.getByRole('button', { name: 'Session actions' }), { key: 'ArrowDown' });
}

describe('SessionActionsMenu', () => {
  it('dispatches actions once and dismisses after each selection', async () => {
    const props = renderMenu();
    for (const [name, callback] of [
      ['New session', props.onNewSession],
      ['Share link…', props.onShare],
      ['Switch tmux', props.onTmuxSwitch],
      ['Launch opencode', props.onLaunchOpencode],
      ['Open in VS Code', props.onOpenVSCode],
    ] as const) {
      openMenu();
      fireEvent.click(screen.getByRole('menuitem', { name }));
      await waitFor(() => expect(callback).toHaveBeenCalledTimes(1));
      expect(screen.queryByRole('menu')).not.toBeInTheDocument();
    }
    expect(props.onTmuxSwitch).toHaveBeenCalledWith(expect.anything(), '~/src/repo');
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
    if (over.tmuxAvailable === false) {
      expect(screen.queryByRole('menuitem', { name: 'Switch tmux' })).toBeNull();
    }
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

  it('supports keyboard tmux selection with a usable mouse-event anchor', async () => {
    const user = userEvent.setup();
    const onTmuxSwitch = vi.fn((event: React.MouseEvent) => {
      expect(event.currentTarget).toHaveTextContent('Switch tmux');
    });
    renderMenu({ onTmuxSwitch });
    screen.getByRole('button', { name: 'Session actions' }).focus();
    await user.keyboard('{Enter}{ArrowDown}{ArrowDown}{ArrowDown}{ArrowDown}{Enter}');
    expect(onTmuxSwitch).toHaveBeenCalledTimes(1);
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
