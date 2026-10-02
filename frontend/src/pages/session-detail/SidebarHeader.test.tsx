// @vitest-environment jsdom
import { expect, it, vi } from 'vitest';
import { fireEvent, render, screen } from '@testing-library/react';
import { SidebarHeader } from './SidebarHeader';

it('keeps the filter button neutral with every filter changed and the menu open', () => {
  render(<SidebarHeader
    searchQuery="" setSearchQuery={vi.fn()}
    showArchivedRecent setShowArchivedRecent={vi.fn()}
    showChildren={false} setShowChildren={vi.fn()}
    showFactory setShowFactory={vi.fn()}
    showRoutines setShowRoutines={vi.fn()}
    sidebarView="recent" setSidebarView={vi.fn()} onNewSession={vi.fn()}
  />);
  const button = screen.getByRole('button', { name: 'Filter sessions' });
  expect(button).not.toHaveClass('active');
  fireEvent.click(button);
  expect(button).toHaveAttribute('aria-expanded', 'true');
  expect(button).not.toHaveClass('active');
});
