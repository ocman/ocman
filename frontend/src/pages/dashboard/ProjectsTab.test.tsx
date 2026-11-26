// @vitest-environment jsdom
//
// Onboarding is only for a successfully loaded, unfiltered empty list.

import { describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { api } from '../../lib/api';
import { MemoryRouter } from 'react-router-dom';
import type { Project } from '../../lib/api';
import type { DashboardCtx } from './context';

const dashboardCtx = vi.fn();
vi.mock('./context', () => ({
  useDashboard: () => dashboardCtx(),
}));

// Imported after the mocks so ProjectsTab binds to them.
import { ProjectsTab } from './ProjectsTab';

function makeProject(overrides: Partial<Project> = {}): Project {
  return {
    directory: '/tmp/proj',
    sessionCount: 3,
    messageCount: 10,
    totalTokensIn: 100,
    totalTokensOut: 200,
    lastUsed: Date.now(),
    ...overrides,
  } as Project;
}

function renderTab(ctx: Partial<DashboardCtx>, url = '/projects') {
  dashboardCtx.mockReturnValue({
    projects: [],
    projectsLoading: false,
    projectsFetching: false,
    projectsLoaded: !ctx.projectsLoading && !ctx.projectsError,
    projectsError: null,
    refetchProjects: vi.fn(),
    dirScope: '',
    setDirScope: vi.fn(),
    ...ctx,
  });
  return render(
    <MemoryRouter initialEntries={[url]}>
      <ProjectsTab />
    </MemoryRouter>,
  );
}

describe('ProjectsTab', () => {
  it.each([undefined, 'remote box'])('keeps the project link keyboard-accessible and owner-qualified for %s', (remoteId) => {
    renderTab({ projects: [makeProject({ directory: '/repo/source', remoteId })] });
    const link = screen.getByRole('link', { name: /source/ });
    expect(link).toHaveAttribute('href', `/project/%2Frepo%2Fsource${remoteId ? '?remoteId=remote%20box' : ''}`);
    fireEvent.click(link);
  });

  it('shows cost instead of the cache-free token pair and sorts numeric columns', () => {
    renderTab({ projects: [
      makeProject({ directory: '/cheap', messageCount: 900, totalCost: 1.5 }),
      makeProject({ directory: '/dear', messageCount: 40, totalCost: 16.2 }),
    ] });
    expect(screen.queryByRole('columnheader', { name: /Tokens/ })).not.toBeInTheDocument();
    expect(screen.getByText('$16.20')).toBeInTheDocument();
    const rows = () => screen.getAllByRole('row').slice(1).map(row => row.textContent);
    fireEvent.click(within(screen.getByRole('columnheader', { name: /Cost/ })).getByRole('button'));
    expect(rows()[0]).toContain('dear');
    fireEvent.click(within(screen.getByRole('columnheader', { name: /Messages/ })).getByRole('button'));
    expect(screen.getByRole('columnheader', { name: /Messages/ })).toHaveAttribute('aria-sort', 'descending');
    expect(rows()[0]).toContain('cheap');
  });

  it('never renders an unused project timestamp as a date', () => {
    renderTab({ projects: [makeProject({ directory: '/remote', remoteId: 'box', sessionCount: 0, lastUsed: 0 })] });
    expect(screen.getByTitle('No sessions yet')).toHaveTextContent('—');
    expect(document.body).not.toHaveTextContent(/1969|1970/);
  });

  it('labels the merge action with visible text, not just an icon', () => {
    renderTab({ projects: [makeProject({ directory: '/source' })] });
    expect(screen.getByRole('button', { name: 'Merge /source' })).toHaveTextContent('Merge…');
  });

  it('disables target selection when no other projects are available', () => {
    renderTab({ projects: [makeProject({ directory: '/source' })] });
    fireEvent.click(screen.getByRole('button', { name: 'Merge /source' }));
    expect(screen.getByRole('combobox', { name: 'Target project' })).toBeDisabled();
    expect(screen.getByRole('button', { name: 'Merge' })).toBeDisabled();
    expect(screen.getByText('No other projects available.')).toBeVisible();
  });

  it('uses final API totals and filters by the final directory', () => {
    const projects = [
      makeProject({ directory: '/target', projectKey: 'merged', sessionCount: 9 }),
    ];
    renderTab({ projects, dirScope: '/target' });
    expect(screen.getByRole('link', { name: /\/target/ })).toBeInTheDocument();
    expect(screen.getByRole('cell', { name: '9' })).toBeInTheDocument();
    expect(screen.queryByText('/source')).not.toBeInTheDocument();
    expect(screen.queryByText('No projects found')).not.toBeInTheDocument();
  });
  it('selects a different project, merges it, and refreshes the list', async () => {
    const merge = vi.spyOn(api, 'mergeProjects').mockResolvedValue({ ok: true });
    const refetchProjects = vi.fn();
    renderTab({ projects: [makeProject({ directory: '/source' }), makeProject({ directory: '/target', remoteId: 'box', remoteName: 'Remote' })], refetchProjects });
    fireEvent.click(screen.getByRole('button', { name: 'Merge /source' }));
    expect(screen.getByRole('dialog', { name: 'Merge project' })).toBeInTheDocument();
    expect(screen.queryByRole('option', { name: '/source' })).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Merge' })).toBeDisabled();
    fireEvent.click(screen.getByRole('combobox', { name: 'Target project' }));
    fireEvent.change(screen.getByRole('textbox', { name: 'Search projects' }), { target: { value: 'Remote' } });
    expect(screen.queryByRole('option', { name: '/source' })).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole('option', { name: '/target · Remote' }));
    fireEvent.click(screen.getByRole('button', { name: 'Merge' }));
    expect(screen.getByRole('combobox', { name: 'Target project' })).toBeDisabled();
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument());
    expect(merge).toHaveBeenCalledWith({ directory: '/source', remoteId: undefined }, { directory: '/target', remoteId: 'box' });
    expect(refetchProjects).toHaveBeenCalled();
    merge.mockRestore();
  });

  it('keeps the selection and displays a failed merge for retry', async () => {
    const merge = vi.spyOn(api, 'mergeProjects').mockRejectedValue(new Error('Merge failed'));
    renderTab({ projects: [makeProject({ directory: '/source' }), makeProject({ directory: '/target' })] });
    fireEvent.click(screen.getByRole('button', { name: 'Merge /source' }));
    fireEvent.click(screen.getByRole('combobox', { name: 'Target project' }));
    fireEvent.click(screen.getByRole('option', { name: '/target' }));
    fireEvent.click(screen.getByRole('button', { name: 'Merge' }));
    await waitFor(() => expect(screen.getByRole('alert')).toHaveTextContent('Merge failed'));
    expect(screen.getByRole('button', { name: 'Merge' })).toBeEnabled();
    expect(screen.getByRole('combobox', { name: 'Target project' })).toHaveTextContent('/target');
    fireEvent.click(screen.getByRole('button', { name: 'Cancel' }));
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    merge.mockRestore();
  });
  it('shows the loading state while the query is in flight', () => {
    renderTab({ projectsLoading: true });
    expect(screen.getByText(/loading projects/i)).toBeInTheDocument();
  });

  it('shows an error with a retry when the query failed', () => {
    const refetchProjects = vi.fn();
    renderTab({ projectsError: 'backend is not responding', refetchProjects }, '/projects?q=alpha');

    expect(screen.getByText(/backend is not responding/)).toBeInTheDocument();
    expect(screen.getByRole('alert')).toHaveTextContent('backend is not responding');
    expect(screen.getByRole('button', { name: /retry/i })).toHaveClass('oc-button');
    // A failure must not be reported as "you have no projects yet".
    expect(screen.queryByTestId('getting-started-empty')).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Clear filters' })).not.toBeInTheDocument();

    screen.getByRole('button', { name: /retry/i }).click();
    expect(refetchProjects).toHaveBeenCalled();
  });

  it('keeps existing projects visible and disables retry during a failed refresh', () => {
    renderTab({ projects: [makeProject({ directory: '/tmp/alpha' })], projectsLoaded: true, projectsError: 'Refresh failed', projectsFetching: true });
    expect(screen.getByRole('alert')).toHaveTextContent('Refresh failed');
    expect(screen.getByText('tmp/alpha')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /retry/i })).toBeDisabled();
    expect(screen.getByRole('button', { name: /retry/i })).toHaveAttribute('aria-busy', 'true');
    expect(screen.queryByTestId('getting-started-empty')).not.toBeInTheDocument();
  });

  it('shows the getting-started state only when the query succeeded and is empty', () => {
    renderTab({ projects: [] }, '/projects?q=%20%20&sort=-sessions&remoteId=build');
    expect(screen.getByTestId('getting-started-empty')).toBeInTheDocument();
  });

  it('keeps a previously loaded empty list visible when refresh fails', () => {
    renderTab({ projects: [], projectsLoaded: true, projectsError: 'Refresh failed' });
    expect(screen.getByRole('alert')).toHaveTextContent('Refresh failed');
    expect(screen.getByTestId('getting-started-empty')).toBeVisible();
  });

  it('restores the search from the URL and shows matches again when the field is cleared', () => {
    renderTab({ projects: [makeProject({ directory: '/alpha' }), makeProject({ directory: '/beta' })] }, '/projects?q=alpha');
    const search = screen.getByRole('searchbox', { name: 'Search projects' });
    expect(search).toHaveValue('alpha');
    expect(screen.queryByRole('link', { name: /beta/ })).not.toBeInTheDocument();
    fireEvent.change(search, { target: { value: 'beta' } });
    expect(screen.queryByRole('link', { name: /alpha/ })).not.toBeInTheDocument();
    fireEvent.change(search, { target: { value: '' } });
    expect(screen.getByRole('link', { name: /alpha/ })).toBeVisible();
    expect(screen.getByRole('link', { name: /beta/ })).toBeVisible();
  });

  it('offers to clear a directory-only filter when nothing matches', () => {
    renderTab({ projects: [makeProject({ directory: '/alpha' })], dirScope: '/missing' }, '/projects?dir=%2Fmissing');
    expect(screen.getByRole('heading', { name: 'No matching projects' })).toBeVisible();
    expect(screen.getByRole('button', { name: 'Clear filters' })).toBeVisible();
  });

  it('clears an unmatched search and restores the list', () => {
    renderTab({ projects: [makeProject({ directory: '/alpha' })] }, '/projects?q=missing');
    fireEvent.click(screen.getByRole('button', { name: 'Clear filters' }));
    expect(screen.getByRole('searchbox', { name: 'Search projects' })).toHaveValue('');
    expect(screen.getByRole('link', { name: /alpha/ })).toBeVisible();
  });

  it('does not offer to clear filters when only an unused local project exists', () => {
    renderTab({ projects: [makeProject({ sessionCount: 0 })] });
    expect(screen.getByText('No projects found')).toBeVisible();
    expect(screen.queryByRole('button', { name: 'Clear filters' })).not.toBeInTheDocument();
  });

  it('lists projects when the query succeeded with data', () => {
    renderTab({ projects: [makeProject({ directory: '/tmp/alpha' })] });
    expect(screen.getByText('tmp/alpha')).toHaveAttribute('title', '/tmp/alpha');
    expect(screen.queryByTestId('getting-started-empty')).not.toBeInTheDocument();
  });
});
