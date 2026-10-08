// @vitest-environment jsdom
import { beforeEach, expect, it, vi } from 'vitest';
import { render, screen, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router-dom';
import { HeaderContext, type HeaderInfo } from '../lib/headerContext';
import { AppHeader } from './AppHeader';

const inventory = vi.hoisted(() => ({ projects: [] as { directory: string; remoteId?: string; remoteName?: string }[] }));
vi.mock('../lib/queries', () => ({ useProjects: () => ({ data: inventory.projects }) }));
beforeEach(() => { inventory.projects = []; });

it('uses the configured owner name rather than its ID or an identical-path local project', () => {
  inventory.projects = [
    { directory: '/repo', remoteId: 'local', remoteName: 'Wrong machine' },
    { directory: '/repo', remoteId: 'B', remoteName: 'Build machine' },
  ];
  render(<MemoryRouter initialEntries={['/project/%2Frepo?remoteId=B']}><AppHeader onOpenNav={vi.fn()} /></MemoryRouter>);
  expect(within(screen.getByRole('banner')).getByLabelText('Build machine')).toHaveAttribute('title', 'Build machine');
  expect(screen.queryByLabelText('Wrong machine')).not.toBeInTheDocument();
});

it.each(['local', 'B'])('shows the %s project owner as a header pill', (owner) => {
  render(<MemoryRouter initialEntries={[`/project/%2Frepo/settings?remoteId=${owner}`]}><AppHeader onOpenNav={vi.fn()} /></MemoryRouter>);
  const label = owner === 'local' ? 'Local' : owner;
  expect(within(screen.getByRole('banner')).getByLabelText(label)).toHaveClass('header-machine-pill');
  expect(screen.getByTitle('/repo')).toHaveTextContent('repo');
});

it.each([
  ['/sessions', { sessionId: 'old', sessionTitle: 'Old title', sessionProject: 'old-project' }, 'Sessions'],
  ['/session/new', { sessionId: 'old', sessionTitle: 'Old title', sessionProject: 'old-project' }, 'New session'],
  ['/session/current', { sessionId: 'old', sessionTitle: 'Old title', sessionProject: 'old-project' }, 'Session'],
] as const)('uses the route title without stale session metadata on %s', async (path, info, title) => {
  const onOpenNav = vi.fn();
  render(
    <MemoryRouter initialEntries={[path]}>
      <HeaderContext.Provider value={{ info, setInfo: vi.fn() }}>
        <AppHeader onOpenNav={onOpenNav} />
      </HeaderContext.Provider>
    </MemoryRouter>,
  );
  expect(screen.getByRole('banner')).toHaveClass('app-header');
  expect(screen.getByRole('heading', { level: 1 })).toHaveTextContent(title);
  expect(screen.queryByText('old-project')).not.toBeInTheDocument();
  await userEvent.click(screen.getByRole('button', { name: 'Open navigation' }));
  expect(onOpenNav).toHaveBeenCalledOnce();
});

it.each([false, true])('shows matching session metadata, with optional platform and full path: %s', (withDetails) => {
  const info: HeaderInfo = {
    sessionId: 'session one', sessionTitle: 'Working session', sessionProject: 'my-project',
    ...(withDetails ? { sessionPlatform: 'opencode', sessionProjectFull: '/repos/my-project' } : {}),
  };
  const { container } = render(
    <MemoryRouter initialEntries={['/session/session%20one']}>
      <HeaderContext.Provider value={{ info, setInfo: vi.fn() }}>
        <AppHeader onOpenNav={vi.fn()} />
      </HeaderContext.Provider>
    </MemoryRouter>,
  );
  expect(screen.getByRole('heading', { level: 1 })).toHaveTextContent('Working session');
  expect(screen.getByText('my-project')).toHaveAttribute('title', withDetails ? '/repos/my-project' : 'my-project');
  // Portal consumers depend on these IDs surviving the component extraction.
  expect(container.querySelector('#header-navigation-slot')).toBeInTheDocument();
  expect(container.querySelector('#header-mobile-title-slot')).toBeInTheDocument();
  expect(container.querySelector('#header-actions-slot')).toBeInTheDocument();
});

it('shows the project on a new conversation while keeping the route title', () => {
  const info: HeaderInfo = { sessionId: 'new', sessionProject: 'src/repo', sessionProjectFull: '/src/repo' };
  render(
    <MemoryRouter initialEntries={['/session/new?dir=%2Fsrc%2Frepo']}>
      <HeaderContext.Provider value={{ info, setInfo: vi.fn() }}>
        <AppHeader onOpenNav={vi.fn()} />
      </HeaderContext.Provider>
    </MemoryRouter>,
  );
  expect(screen.getByRole('heading', { level: 1 })).toHaveTextContent('New session');
  expect(screen.getByText('src/repo')).toHaveAttribute('title', '/src/repo');
});

it.each([
  ['/project/%2Frepos%2Focman', 'repos/ocman'],
  ['/project/%2Frepos%2Focman/worktrees', 'repos/ocman / Worktrees'],
])('labels project route %s with the project label', (path, title) => {
  render(
    <MemoryRouter initialEntries={[path]}>
      <AppHeader onOpenNav={vi.fn()} />
    </MemoryRouter>,
  );
  expect(screen.getByRole('heading', { level: 1 })).toHaveTextContent(title);
  expect(screen.getByText('repos/ocman')).toHaveAttribute('title', '/repos/ocman');
});
