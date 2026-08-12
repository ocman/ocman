// @vitest-environment jsdom
//
// The Projects tab has to tell three states apart: still loading, the
// query failed, and the query succeeded with nothing in it. Only the
// last one is onboarding. Rendering GettingStartedEmpty on a failure
// tells a user with dozens of projects that they have none.

import { describe, expect, it, vi } from 'vitest';
import { render, screen } from '@testing-library/react';
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
  it('shows the loading state while the query is in flight', () => {
    renderTab({ projectsLoading: true });
    expect(screen.getByText(/loading projects/i)).toBeInTheDocument();
  });

  it('shows an error with a retry when the query failed', () => {
    const refetchProjects = vi.fn();
    renderTab({ projectsError: 'backend is not responding', refetchProjects });

    expect(screen.getByText(/backend is not responding/)).toBeInTheDocument();
    // A failure must not be reported as "you have no projects yet".
    expect(screen.queryByTestId('getting-started-empty')).not.toBeInTheDocument();

    screen.getByRole('button', { name: /retry/i }).click();
    expect(refetchProjects).toHaveBeenCalled();
  });

  it('shows the getting-started state only when the query succeeded and is empty', () => {
    renderTab({ projects: [] });
    expect(screen.getByTestId('getting-started-empty')).toBeInTheDocument();
  });

  it('lists projects when the query succeeded with data', () => {
    renderTab({ projects: [makeProject({ directory: '/tmp/alpha' })] });
    expect(screen.getByText('/tmp/alpha')).toBeInTheDocument();
    expect(screen.queryByTestId('getting-started-empty')).not.toBeInTheDocument();
  });
});
