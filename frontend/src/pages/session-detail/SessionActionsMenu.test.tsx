// @vitest-environment jsdom
import { describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen } from '@testing-library/react';
import type { TmuxSession } from '../../lib/api';
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
  const utils = render(<SessionActionsMenu {...props} />);
  return { ...utils, props };
}

describe('SessionActionsMenu', () => {
  it('closes the menu and dispatches each action', () => {
    const { props } = renderMenu();
    const details = screen.getByRole('menu').closest('details')!;
    details.setAttribute('open', '');

    fireEvent.click(screen.getByRole('menuitem', { name: 'New session' }));
    expect(props.onNewSession).toHaveBeenCalled();
    expect(details.hasAttribute('open')).toBe(false);

    fireEvent.click(screen.getByRole('menuitem', { name: 'Share link…' }));
    fireEvent.click(screen.getByRole('menuitem', { name: 'Switch tmux' }));
    fireEvent.click(screen.getByRole('menuitem', { name: 'Launch opencode' }));
    fireEvent.click(screen.getByRole('menuitem', { name: 'Open in VS Code' }));
    expect(props.onShare).toHaveBeenCalled();
    expect(props.onTmuxSwitch).toHaveBeenCalledWith(expect.anything(), '~/src/repo');
    expect(props.onLaunchOpencode).toHaveBeenCalled();
    expect(props.onOpenVSCode).toHaveBeenCalled();

    expect(screen.getByRole('menuitem', { name: 'Download Markdown' }))
      .toHaveAttribute('download', 'conversation-sess-1.md');
  });

  it('hides tmux items when tmux is unavailable or the composer is live', () => {
    renderMenu({ tmuxAvailable: false });
    expect(screen.queryByRole('menuitem', { name: 'Switch tmux' })).toBeNull();
    expect(screen.queryByRole('menuitem', { name: 'Launch opencode' })).toBeNull();
  });

  it('shows the launching state', () => {
    renderMenu({ portAvailable: false, launchingOpencode: true });
    expect(screen.getByRole('menuitem', { name: 'Launching…' })).toBeDisabled();
  });
});
