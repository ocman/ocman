// @vitest-environment jsdom
import { describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { IconButton } from './IconButton';
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuSeparator, DropdownMenuTrigger } from './DropdownMenu';

function renderMenu(disabled = false) {
  const select = vi.fn();
  const blocked = vi.fn();
  const result = render(
    <>
      <DropdownMenu>
        <DropdownMenuTrigger asChild disabled={disabled}>
          <IconButton label="Actions" icon="bi-three-dots" />
        </DropdownMenuTrigger>
        <DropdownMenuContent>
          <DropdownMenuItem onSelect={select}>Alpha</DropdownMenuItem>
          <DropdownMenuItem disabled onSelect={blocked}>Blocked</DropdownMenuItem>
          <DropdownMenuSeparator />
          <DropdownMenuItem onSelect={select}>Charlie</DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenu>
      <button>Outside</button>
    </>,
  );
  return { ...result, select, blocked };
}

describe('DropdownMenu', () => {
  it('portals content, skips disabled items, and restores focus after keyboard selection', async () => {
    const user = userEvent.setup();
    const { container, select, blocked } = renderMenu();
    const trigger = screen.getByRole('button', { name: 'Actions' });
    trigger.focus();
    await user.keyboard('{ArrowDown}');
    expect(trigger).toHaveAttribute('aria-expanded', 'true');
    expect(container).not.toContainElement(screen.getByRole('menu'));
    expect(screen.getByRole('menuitem', { name: 'Alpha' })).toHaveFocus();
    fireEvent.click(screen.getByRole('menuitem', { name: 'Blocked' }));
    expect(blocked).not.toHaveBeenCalled();
    await user.keyboard('{ArrowDown}');
    expect(screen.getByRole('menuitem', { name: 'Charlie' })).toHaveFocus();
    await user.keyboard('{Home}');
    expect(screen.getByRole('menuitem', { name: 'Alpha' })).toHaveFocus();
    await user.keyboard('{End}{Enter}');
    expect(select).toHaveBeenCalledOnce();
    expect(screen.queryByRole('menu')).not.toBeInTheDocument();
    await waitFor(() => expect(trigger).toHaveFocus());
  });

  it('dismisses with Escape or an outside pointer without selecting', async () => {
    const user = userEvent.setup();
    const { select } = renderMenu();
    const trigger = screen.getByRole('button', { name: 'Actions' });
    await user.click(trigger);
    await user.keyboard('{Escape}');
    await waitFor(() => expect(trigger).toHaveFocus());
    await user.click(trigger);
    fireEvent.pointerDown(screen.getByRole('button', { name: 'Outside', hidden: true }));
    await waitFor(() => expect(screen.queryByRole('menu')).not.toBeInTheDocument());
    expect(select).not.toHaveBeenCalled();
  });

  it('does not open a disabled trigger', async () => {
    const user = userEvent.setup();
    renderMenu(true);
    await user.click(screen.getByRole('button', { name: 'Actions' }));
    expect(screen.queryByRole('menu')).not.toBeInTheDocument();
  });
});
