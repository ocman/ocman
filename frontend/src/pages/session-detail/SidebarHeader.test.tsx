// @vitest-environment jsdom
import { expect, it, vi } from 'vitest';
import { fireEvent, render, screen } from '@testing-library/react';
import { SidebarHeader, type SidebarHeaderProps } from './SidebarHeader';

it('keeps the filter button neutral with every filter changed and the menu open', () => {
  render(<SidebarHeader
    projects={[]} projectFilter="" setProjectFilter={vi.fn()}
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

it('represents a removed project selection and lets the user clear it', () => {
  const props: SidebarHeaderProps = {
    projects: [{ directory: '/repo', sessions: [], lastUpdated: 0, aggregate: { kind: 'none' } }],
    projectFilter: '/repo', setProjectFilter: vi.fn(),
    searchQuery: '', setSearchQuery: vi.fn(),
    showArchivedRecent: false, setShowArchivedRecent: vi.fn(),
    showChildren: true, setShowChildren: vi.fn(),
    showFactory: false, setShowFactory: vi.fn(),
    showRoutines: false, setShowRoutines: vi.fn(),
    sidebarView: 'recent', setSidebarView: vi.fn(), onNewSession: vi.fn(),
  };
  const { rerender } = render(<SidebarHeader {...props} />);
  fireEvent.click(screen.getByRole('button', { name: 'Filter sessions' }));
  rerender(<SidebarHeader {...props} projects={[]} />);
  const selector = screen.getByRole('combobox', { name: 'Project' });
  expect(selector).toHaveValue('/repo');
  expect(screen.getByRole('option', { name: 'Unavailable project' })).toBeDisabled();
  fireEvent.change(selector, { target: { value: '' } });
  expect(props.setProjectFilter).toHaveBeenCalledWith('');
  rerender(<SidebarHeader {...props} projects={[]} projectFilter="" />);
  expect(selector).toHaveValue('');
  expect(screen.queryByRole('option', { name: 'Unavailable project' })).not.toBeInTheDocument();
});
