// @vitest-environment jsdom
import { fireEvent, render, screen } from '@testing-library/react';
import { expect, it, vi } from 'vitest';
import { SidebarRow } from './SidebarRow';

it('shares selected, grouped, flat and archiving presentation', () => {
  const view = render(<SidebarRow active inGroup flat archiving className="child" data-session-key="session">Title</SidebarRow>);
  const row = screen.getByRole('button');
  expect(row).toHaveClass('session-sidebar-item', 'active', 'in-group', 'flat', 'archiving', 'child');
  expect(row).toHaveAttribute('data-session-key', 'session');
  expect(row).toHaveAttribute('aria-selected', 'true');
  view.rerender(<SidebarRow active={false}>Title</SidebarRow>);
  expect(row.className.trim()).toBe('session-sidebar-item');
  expect(row).toHaveAttribute('aria-selected', 'false');
});

it('opens on Enter and Space, and leaves child controls and other keys alone', () => {
  const open = vi.fn();
  render(<SidebarRow active={false} onClick={open}>Title<button>Action</button></SidebarRow>);
  const row = screen.getByRole('button', { name: 'Title Action' });
  fireEvent.keyDown(row, { key: 'Enter' });
  fireEvent.keyDown(row, { key: ' ' });
  fireEvent.keyDown(row, { key: 'Escape' });
  fireEvent.keyDown(screen.getByRole('button', { name: 'Action' }), { key: 'Enter' });
  expect(open).toHaveBeenCalledTimes(2);
});

it('preserves session-specific keyboard handlers', () => {
  const keyDown = vi.fn();
  const open = vi.fn();
  render(<SidebarRow active onKeyDown={keyDown} onClick={open}>Title</SidebarRow>);
  fireEvent.keyDown(screen.getByRole('button'), { key: 'Enter' });
  expect(keyDown).toHaveBeenCalledOnce();
  expect(open).not.toHaveBeenCalled();
});
