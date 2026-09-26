// @vitest-environment jsdom
import { expect, it, vi } from 'vitest';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router-dom';
import { HeaderContext, type HeaderInfo } from '../lib/headerContext';
import { AppHeader } from './AppHeader';

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
