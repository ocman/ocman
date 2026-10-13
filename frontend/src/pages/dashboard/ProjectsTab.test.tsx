// @vitest-environment jsdom
//
// The Projects tab has to tell three states apart: still loading, the
// query failed, and the query succeeded with nothing in it. Only the
// last one is onboarding. Rendering GettingStartedEmpty on a failure
// tells a user with dozens of projects that they have none.

import { describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { api } from '../../lib/api';
import { MemoryRouter } from 'react-router-dom';
import type { Project } from '../../lib/api';
import type { DashboardCtx } from './context';

const dashboardCtx = vi.fn();
vi.mock('./context', () => ({
  useDashboard: () => dashboardCtx(),
}));

vi.mock('./DashboardToolbar', () => ({
  DashboardToolbar: () => <div data-testid="dashboard-toolbar" />,
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

function renderTab(ctx: Partial<DashboardCtx>) {
  dashboardCtx.mockReturnValue({
    projects: [],
    projectsLoading: false,
    projectsFetching: false,
    projectsError: null,
    refetchProjects: vi.fn(),
    dirScope: '',
    setDirScope: vi.fn(),
    ...ctx,
  });
  return render(
    <MemoryRouter>
      <ProjectsTab />
    </MemoryRouter>,
  );
}

describe('ProjectsTab', () => {
  it.each(['/source', '/target'])('only totals checkout rows within the %s directory scope', (dirScope) => {
    const projects = [
      makeProject({ directory: '/source', projectKey: 'merged', mergedIntoDirectory: '/target', sessionCount: 2 }),
      makeProject({ directory: '/target', projectKey: 'merged', sessionCount: 7 }),
    ];
    renderTab({ projects, dirScope });
    expect(screen.getByText('/target')).toBeInTheDocument();
    expect(screen.getByRole('cell', { name: dirScope === '/source' ? '2' : '7' })).toBeInTheDocument();
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
    fireEvent.change(screen.getByRole('combobox', { name: 'Target project' }), { target: { value: JSON.stringify(['box', '/target']) } });
    fireEvent.click(screen.getByRole('button', { name: 'Merge' }));
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument());
    expect(merge).toHaveBeenCalledWith({ directory: '/source', remoteId: undefined }, { directory: '/target', remoteId: 'box' });
    expect(refetchProjects).toHaveBeenCalled();
    merge.mockRestore();
  });

  it('keeps the selection and displays a failed merge for retry', async () => {
    const merge = vi.spyOn(api, 'mergeProjects').mockRejectedValue(new Error('Merge failed'));
    renderTab({ projects: [makeProject({ directory: '/source' }), makeProject({ directory: '/target' })] });
    fireEvent.click(screen.getByRole('button', { name: 'Merge /source' }));
    fireEvent.change(screen.getByRole('combobox'), { target: { value: JSON.stringify(['local', '/target']) } });
    fireEvent.click(screen.getByRole('button', { name: 'Merge' }));
    await waitFor(() => expect(screen.getByRole('alert')).toHaveTextContent('Merge failed'));
    expect(screen.getByRole('button', { name: 'Merge' })).toBeEnabled();
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
    renderTab({ projectsError: 'backend is not responding', refetchProjects });

    expect(screen.getByText(/backend is not responding/)).toBeInTheDocument();
    expect(screen.getByRole('alert')).toHaveTextContent('backend is not responding');
    expect(screen.getByRole('button', { name: /retry/i })).toHaveClass('oc-button');
    // A failure must not be reported as "you have no projects yet".
    expect(screen.queryByTestId('getting-started-empty')).not.toBeInTheDocument();

    screen.getByRole('button', { name: /retry/i }).click();
    expect(refetchProjects).toHaveBeenCalled();
  });

  it('keeps existing projects visible and disables retry during a failed refresh', () => {
    renderTab({ projects: [makeProject({ directory: '/tmp/alpha' })], projectsError: 'Refresh failed', projectsLoading: false, projectsFetching: true });
    expect(screen.getByRole('alert')).toHaveTextContent('Refresh failed');
    expect(screen.getByText('tmp/alpha')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /retry/i })).toBeDisabled();
    expect(screen.getByRole('button', { name: /retry/i })).toHaveAttribute('aria-busy', 'true');
    expect(screen.queryByTestId('getting-started-empty')).not.toBeInTheDocument();
  });

  it('shows the getting-started state only when the query succeeded and is empty', () => {
    renderTab({ projects: [] });
    expect(screen.getByTestId('getting-started-empty')).toBeInTheDocument();
  });

  it('lists projects when the query succeeded with data', () => {
    renderTab({ projects: [makeProject({ directory: '/tmp/alpha' })] });
    expect(screen.getByText('tmp/alpha')).toHaveAttribute('title', '/tmp/alpha');
    expect(screen.queryByTestId('getting-started-empty')).not.toBeInTheDocument();
  });
});
