// @vitest-environment jsdom
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { MemoryRouter, useLocation } from 'react-router-dom';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { Project } from '../lib/api';

function LocationProbe() {
  const location = useLocation();
  return <output data-testid="location">{location.pathname + location.search}</output>;
}

function expectDraftLocation(expectedQuery: string) {
  const location = screen.getByTestId('location').textContent!;
  const url = new URL(location, 'http://localhost');
  expect(url.pathname).toMatch(/^\/session\/ocm_[0-9a-f-]{36}$/);
  expect(url.pathname).toBe(`/session/${url.searchParams.get('draftId')}`);
  expect(url.search).toContain(expectedQuery);
}

const mocks = vi.hoisted(() => {
  const uiState = {
    paletteOpen: true,
    paletteMode: 'project' as 'command' | 'search' | 'project' | 'project-session',
    projectSessionInitialDirectory: undefined as string | undefined,
    projectSessionInitialRemoteId: undefined as string | undefined,
    closePalette: vi.fn(),
    openProjectPalette: vi.fn(() => {
      uiState.paletteMode = 'project';
    }),
    openProjectSessionPalette: vi.fn(),
    openShortcuts: vi.fn(),
    openWorktreeForm: vi.fn(),
    dispatchCommand: vi.fn(),
  };
  const apiState = {
    cachedSessions: [],
    getProjects: vi.fn(async (): Promise<Project[]> => []),
    resolveProjectIdentities: vi.fn(),
    browseDirectories: vi.fn(),
    searchDirectories: vi.fn(),
    refreshCachedSessions: vi.fn(async () => []),
    getTmuxSessions: vi.fn(async () => ({ available: false, sessions: [] })),
    getTmuxClients: vi.fn(async () => ({ available: false, clients: [] })),
    switchTmuxSession: vi.fn(),
  };
  return { uiState, apiState };
});

vi.mock('../lib/remoteLog');

vi.mock('../lib/uiStore', () => {
  const useUiStore = Object.assign(
    (selector?: (state: typeof mocks.uiState) => unknown) => (
      selector ? selector(mocks.uiState) : mocks.uiState
    ),
    { getState: () => mocks.uiState },
  );
  return { useUiStore };
});

vi.mock('../lib/api', async () => {
  const real = await vi.importActual<typeof import('../lib/api')>('../lib/api');
  return { ...real, api: { ...real.api, projects: mocks.apiState.getProjects,
    resolveProjectIdentities: mocks.apiState.resolveProjectIdentities,
    browseDirectories: mocks.apiState.browseDirectories, searchDirectories: mocks.apiState.searchDirectories,
    tmuxSessions: mocks.apiState.getTmuxSessions, tmuxClients: mocks.apiState.getTmuxClients,
    tmuxSwitch: mocks.apiState.switchTmuxSession } };
});

vi.mock('../lib/useCapabilities', () => ({
  useWorktreeLaunch: () => false,
}));


import { CommandPalette } from './CommandPalette';

function renderPalette() {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  const tree = () => (
    <QueryClientProvider client={queryClient}>
      <MemoryRouter>
        <CommandPalette />
        <LocationProbe />
      </MemoryRouter>
    </QueryClientProvider>
  );
  const view = render(tree());
  return { queryClient, rerenderPalette: () => view.rerender(tree()) };
}

describe('CommandPalette project mode', () => {
  afterEach(() => vi.restoreAllMocks());
  beforeEach(() => {
    vi.clearAllMocks();
    Element.prototype.scrollIntoView = vi.fn();
    mocks.uiState.paletteOpen = true;
    mocks.uiState.paletteMode = 'project';
    mocks.uiState.projectSessionInitialDirectory = undefined;
    mocks.uiState.projectSessionInitialRemoteId = undefined;
    mocks.apiState.cachedSessions = [];
    mocks.apiState.getProjects.mockResolvedValue([
      {
        directory: '/Users/peter/workspace/ocman',
        sessionCount: 3,
        messageCount: 12,
        totalTokensIn: 100,
        totalTokensOut: 20,
        lastUsed: 3000,
      },
      {
        directory: '/Users/peter/workspace/research',
        sessionCount: 1,
        messageCount: 2,
        totalTokensIn: 50,
        totalTokensOut: 10,
        lastUsed: 2000,
      },
      {
        directory: '/Users/peter/workspace/banana-frontend',
        sessionCount: 1,
        messageCount: 1,
        totalTokensIn: 10,
        totalTokensOut: 5,
        lastUsed: 1000,
      },
    ]);
    mocks.apiState.browseDirectories.mockImplementation(async (dir?: string) => {
      if (dir === '/Users/peter/workspace/research') {
        return {
          directory: '/Users/peter/workspace/research',
          parent: '/Users/peter/workspace',
          home: '/Users/peter',
          entries: [{ name: 'datastack', path: '/Users/peter/workspace/research/datastack' }],
        };
      }
      if (dir === '/Users/peter/workspace/ocman') {
        return {
          directory: '/Users/peter/workspace/ocman',
          parent: '/Users/peter/workspace',
          home: '/Users/peter',
          entries: [],
        };
      }
      if (dir === '/Users/peter/workspace') {
        return {
          directory: '/Users/peter/workspace',
          parent: '/Users/peter',
          home: '/Users/peter',
          entries: [{ name: 'ocman', path: '/Users/peter/workspace/ocman' }],
        };
      }
      return {
        directory: '/Users/peter',
        parent: '/Users',
        home: '/Users/peter',
        entries: [{ name: 'workspace', path: '/Users/peter/workspace' }],
      };
    });
    mocks.apiState.searchDirectories.mockImplementation(async (root: string | undefined, query: string) => {
      if (root === '/Users/peter/workspace/research' && query === '/Users/peter/workspace/research/data') {
        return {
          root,
          query,
          entries: [
            {
              name: 'datastack',
              path: '/Users/peter/workspace/research/datastack',
              project: false,
              depth: 1,
            },
          ],
        };
      }
      if (query === 'research') {
        return {
          root: root ?? '/Users/peter',
          query,
          entries: [
            {
              name: 'research',
              path: '/Users/peter/workspace/research',
              project: true,
              depth: 2,
            },
          ],
        };
      }
      return {
        root: root ?? '/Users/peter',
        query,
        entries: [
          {
            name: 'ocman',
            path: '/Users/peter/workspace/ocman',
            project: true,
            depth: 2,
          },
        ],
      };
    });
    mocks.apiState.getTmuxSessions.mockResolvedValue({ available: false, sessions: [] });
    mocks.apiState.getTmuxClients.mockResolvedValue({ available: false, clients: [] });
  });

  it('opens project mode as a filesystem browser instead of loading known projects', async () => {
    renderPalette();

    expect(await screen.findByPlaceholderText('Browse project directories...')).toBeInTheDocument();
    expect(await screen.findByRole('button', { name: 'Use this directory' })).toBeInTheDocument();
    expect(screen.queryByLabelText('Browse this machine')).not.toBeInTheDocument();
    expect(screen.queryByText('Use current directory')).not.toBeInTheDocument();
    expect(mocks.apiState.browseDirectories).toHaveBeenCalledWith(undefined, expect.any(AbortSignal));
    expect(mocks.apiState.getProjects).not.toHaveBeenCalled();
  });

  it('browses local directories from project mode', async () => {
    renderPalette();

    expect(await screen.findByRole('button', { name: 'Use this directory' })).toBeInTheDocument();
    expect(screen.queryByText('Use current directory')).not.toBeInTheDocument();
    expect(screen.getByText('workspace')).toBeInTheDocument();

    fireEvent.click(screen.getByText('workspace'));

    expect(await screen.findByText('ocman')).toBeInTheDocument();
    expect(mocks.apiState.browseDirectories.mock.calls[1][0]).toBe('/Users/peter/workspace');
  });

  // Nothing is created yet: the browsed directory opens the new-conversation
  // route, and the first prompt creates the session where the composer points.
  it('opens a new conversation for the selected browsed directory', async () => {
    renderPalette();

    fireEvent.click(await screen.findByText('Use this directory'));

    await waitFor(() => {
      expectDraftLocation('?dir=%2FUsers%2Fpeter');
    });
    expect(mocks.uiState.closePalette).toHaveBeenCalled();
  });

  it('searches below the current browsed directory while typing', async () => {
    renderPalette();

    expect(await screen.findByText('workspace')).toBeInTheDocument();

    fireEvent.change(screen.getByPlaceholderText('Browse project directories...'), {
      target: { value: 'oc' },
    });

    expect(await screen.findByText('Likely project · /Users/peter/workspace/ocman')).toBeInTheDocument();
    expect(mocks.apiState.searchDirectories).toHaveBeenCalledWith('/Users/peter', 'oc', 50, expect.any(AbortSignal));

    fireEvent.click(screen.getByText('ocman'));

    await waitFor(() => {
      expect(mocks.apiState.browseDirectories).toHaveBeenCalledWith('/Users/peter/workspace/ocman', expect.any(AbortSignal));
    });
  });

  it('keeps searching usable after opening a search result directory', async () => {
    renderPalette();

    const input = await screen.findByPlaceholderText('Browse project directories...');

    fireEvent.change(input, {
      target: { value: 'research' },
    });

    expect(await screen.findByText('Likely project · /Users/peter/workspace/research')).toBeInTheDocument();
    fireEvent.click(screen.getByText('research'));

    await waitFor(() => {
      expect(mocks.apiState.browseDirectories).toHaveBeenCalledWith('/Users/peter/workspace/research', expect.any(AbortSignal));
      expect(input).toHaveFocus();
    });
    expect(input).toHaveValue('/Users/peter/workspace/research/');
    expect(screen.getByText('datastack')).toBeInTheDocument();

    fireEvent.change(input, {
      target: { value: '/Users/peter/workspace/research/data' },
    });

    expect(await screen.findByText('/Users/peter/workspace/research/datastack')).toBeInTheDocument();
    expect(mocks.apiState.searchDirectories).toHaveBeenCalledWith('/Users/peter/workspace/research', '/Users/peter/workspace/research/data', 50, expect.any(AbortSignal));
  });

  it('opens session-project mode as a known-project picker', async () => {
    mocks.uiState.paletteMode = 'project-session';

    renderPalette();

    expect(await screen.findByPlaceholderText('Select a project to start a session...')).toBeInTheDocument();
    expect(await screen.findByText('workspace/ocman')).toBeInTheDocument();
    expect(screen.getByText((text) => text.startsWith('3 sessions'))).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Use this directory' })).not.toBeInTheDocument();
    expect(mocks.apiState.getProjects).toHaveBeenCalledWith(expect.any(AbortSignal));
    expect(mocks.apiState.browseDirectories).not.toHaveBeenCalled();
  });

  it.each([undefined, 'source-box'])('uses final API projects and owner-qualified preselection: %s', async (remoteId) => {
    mocks.uiState.paletteMode = 'project-session';
    mocks.uiState.projectSessionInitialDirectory = '/local/source';
    mocks.uiState.projectSessionInitialRemoteId = remoteId;
    const stats = { sessionCount: 1, messageCount: 1, totalTokensIn: 1, totalTokensOut: 1, lastUsed: 1 };
    mocks.apiState.getProjects.mockResolvedValue([
      { ...stats, directory: '/other/repo', lastUsed: 10 },
      { ...stats, directory: '/remote/target', projectKey: 'merged', remoteId: 'box', platform: 'r-box:opencode' },
    ]);
    mocks.apiState.resolveProjectIdentities.mockResolvedValue([{ key: 'merged', directory: '/remote/target', remoteId: 'box' }]);

    renderPalette();

    const target = await screen.findByText('remote/target');
    expect(screen.queryByText('local/source')).not.toBeInTheDocument();
    expect(screen.getAllByRole('option')).toHaveLength(3);
    expect(target.closest('[role="option"]')).toHaveAttribute('aria-selected', 'true');
    fireEvent.click(target);
    await waitFor(() => {
      const location = screen.getByTestId('location').textContent!;
      expectDraftLocation('?dir=%2Fremote%2Ftarget&remoteId=box');
      expect(location).toContain('&platform=r-box%3Aopencode');
    });
    expect(mocks.apiState.resolveProjectIdentities).toHaveBeenCalledWith([{ directory: '/local/source', remoteId }], expect.any(AbortSignal));
  });

  it('preselects the requested project in session-project mode', async () => {
    mocks.uiState.paletteMode = 'project-session';
    mocks.uiState.projectSessionInitialDirectory = '/Users/peter/workspace/research';

    renderPalette();

    const project = await screen.findByText('workspace/research');
    await waitFor(() => {
      expect(project.closest('[role="option"]')).toHaveAttribute('aria-selected', 'true');
    });
  });

  it('preselects the requested owner when final projects share a path', async () => {
    mocks.uiState.paletteMode = 'project-session';
    mocks.uiState.projectSessionInitialDirectory = '/repo';
    mocks.uiState.projectSessionInitialRemoteId = 'box';
    const stats = { sessionCount: 1, messageCount: 1, totalTokensIn: 1, totalTokensOut: 1, lastUsed: 1 };
    mocks.apiState.getProjects.mockResolvedValue([
      { ...stats, directory: '/repo', projectKey: 'local' },
      { ...stats, directory: '/repo', projectKey: 'remote', remoteId: 'box', remoteName: 'Box' },
    ]);
    renderPalette();
    const box = await screen.findByRole('option', { name: /Box/ });
    await waitFor(() => expect(box).toHaveAttribute('aria-selected', 'true'));
    expect(mocks.apiState.resolveProjectIdentities).not.toHaveBeenCalled();
  });

  it('shows a project load failure instead of the picker', async () => {
    mocks.uiState.paletteMode = 'project-session';
    mocks.apiState.getProjects.mockRejectedValue(new Error('projects unavailable'));

    renderPalette();

    expect(await screen.findByText('projects unavailable')).toBeInTheDocument();
    expect(screen.queryByText('Create new project')).not.toBeInTheDocument();
  });

  it('keeps the user selection when the project list refreshes', async () => {
    mocks.uiState.paletteMode = 'project-session';
    mocks.uiState.projectSessionInitialDirectory = '/Users/peter/workspace/research';
    const { queryClient } = renderPalette();
    const requested = await screen.findByText('workspace/research');
    await waitFor(() => expect(requested.closest('[role="option"]')).toHaveAttribute('aria-selected', 'true'));

    fireEvent.keyDown(screen.getByPlaceholderText('Select a project to start a session...'), { key: 'ArrowDown' });
    const selected = screen.getByText('workspace/banana-frontend').closest('[role="option"]');
    mocks.apiState.getProjects.mockResolvedValue([
      { directory: '/Users/peter/workspace/banana-frontend', lastUsed: 9000 } as Project,
      { directory: '/Users/peter/workspace/research', lastUsed: 2000 } as Project,
    ]);
    await queryClient.refetchQueries({ queryKey: ['projects'] });

    await waitFor(() => expect(screen.getAllByRole('option')[0]).toHaveTextContent('workspace/banana-frontend'));
    await waitFor(() => expect(selected).toHaveAttribute('aria-selected', 'true'));
    expect(mocks.apiState.getProjects).toHaveBeenCalledTimes(2);
  });

  it('does not activate another project when the selected project disappears', async () => {
    mocks.uiState.paletteMode = 'project-session';
    mocks.uiState.projectSessionInitialDirectory = '/Users/peter/workspace/research';
    const { queryClient } = renderPalette();
    const requested = await screen.findByText('workspace/research');
    await waitFor(() => expect(requested.closest('[role="option"]')).toHaveAttribute('aria-selected', 'true'));
    mocks.apiState.getProjects.mockResolvedValue([{ directory: '/other', projectKey: 'other', lastUsed: 1 } as Project]);
    await queryClient.refetchQueries({ queryKey: ['projects'] });
    await waitFor(() => expect(screen.queryByText('workspace/research')).not.toBeInTheDocument());
    expect(screen.queryByRole('option', { selected: true })).not.toBeInTheDocument();
    fireEvent.keyDown(screen.getByRole('combobox'), { key: 'Enter' });
    expect(screen.getByTestId('location')).toHaveTextContent(/^\/$/);
    expect(screen.queryByRole('option', { selected: true })).not.toBeInTheDocument();
  });

  it('tracks the default projectKey across owner reordering and checkout changes', async () => {
    mocks.uiState.paletteMode = 'project-session';
    const local = { projectKey: 'local', directory: '/repo', lastUsed: 2 } as Project;
    const remote = { projectKey: 'remote', directory: '/repo', remoteId: 'box', remoteName: 'Box', lastUsed: 1 } as Project;
    mocks.apiState.getProjects.mockResolvedValue([local, remote]);
    const { queryClient } = renderPalette();
    await screen.findByRole('option', { name: /Box/ });
    mocks.apiState.getProjects.mockResolvedValue([
      { ...remote, lastUsed: 3 },
      { ...local, directory: '/renamed/repo' },
    ]);
    await queryClient.refetchQueries({ queryKey: ['projects'] });
    const selected = await screen.findByRole('option', { name: /renamed\/repo/ });
    expect(selected).toHaveAttribute('aria-selected', 'true');
    fireEvent.keyDown(screen.getByRole('combobox'), { key: 'Enter' });
    await waitFor(() => expectDraftLocation('?dir=%2Frenamed%2Frepo'));
  });

  it('does not override an explicit selection when initial resolution completes later', async () => {
    mocks.uiState.paletteMode = 'project-session';
    mocks.uiState.projectSessionInitialDirectory = '/merged/checkout';
    let resolve!: (identities: unknown[]) => void;
    mocks.apiState.resolveProjectIdentities.mockReturnValue(new Promise((done) => { resolve = done; }));
    mocks.apiState.getProjects.mockResolvedValue([
      { projectKey: 'first', directory: '/first', lastUsed: 2 } as Project,
      { projectKey: 'second', directory: '/second', lastUsed: 1 } as Project,
    ]);
    renderPalette();
    const first = await screen.findByRole('option', { name: /first/ });
    fireEvent.keyDown(screen.getByRole('combobox'), { key: 'ArrowUp' });
    await act(async () => resolve([{ key: 'second' }]));
    await waitFor(() => expect(first).toHaveAttribute('aria-selected', 'true'));
    fireEvent.keyDown(screen.getByRole('combobox'), { key: 'Enter' });
    await waitFor(() => expectDraftLocation('?dir=%2Ffirst'));
  });

  it('reports a resolved identity missing from the project list without selecting a fallback', async () => {
    mocks.uiState.paletteMode = 'project-session';
    mocks.uiState.projectSessionInitialDirectory = '/missing';
    mocks.apiState.resolveProjectIdentities.mockResolvedValue([{ key: 'missing' }]);
    renderPalette();
    expect(await screen.findByText('Could not resolve the requested project. Select a project to continue.')).toBeInTheDocument();
    fireEvent.keyDown(screen.getByRole('combobox'), { key: 'Enter' });
    expect(screen.getByTestId('location')).toHaveTextContent(/^\/$/);
    fireEvent.click(screen.getByText('workspace/research'));
    await waitFor(() => expectDraftLocation('?dir=%2FUsers%2Fpeter%2Fworkspace%2Fresearch'));
  });

  it('surfaces resolution failures and requires an explicit target before Enter', async () => {
    mocks.uiState.paletteMode = 'project-session';
    mocks.uiState.projectSessionInitialDirectory = '/merged/checkout';
    let reject!: (error: Error) => void;
    mocks.apiState.resolveProjectIdentities.mockReturnValue(new Promise((_, fail) => { reject = fail; }));
    renderPalette();
    await screen.findByText('workspace/ocman');
    const input = screen.getByRole('combobox');
    fireEvent.keyDown(input, { key: 'Enter' });
    expect(screen.getByTestId('location')).toHaveTextContent(/^\/$/);
    reject(new Error('Identity service unavailable'));
    expect(await screen.findByText('Identity service unavailable')).toBeInTheDocument();
    fireEvent.keyDown(input, { key: 'Enter' });
    expect(screen.getByTestId('location')).toHaveTextContent(/^\/$/);
    fireEvent.keyDown(input, { key: 'ArrowDown' });
    fireEvent.keyDown(input, { key: 'Enter' });
    await waitFor(() => expectDraftLocation('?dir=%2FUsers%2Fpeter%2Fworkspace%2Focman'));
  });

  it('focuses the project picker when it opens', () => {
    mocks.uiState.paletteOpen = false;
    mocks.uiState.paletteMode = 'project-session';

    const { rerenderPalette } = renderPalette();
    mocks.uiState.paletteOpen = true;
    rerenderPalette();

    expect(screen.getByPlaceholderText('Select a project to start a session...')).toHaveFocus();
  });

  it('opens a new conversation for a local project on this machine', async () => {
    mocks.uiState.paletteMode = 'project-session';

    renderPalette();

    fireEvent.click(await screen.findByText('workspace/ocman'));

    await waitFor(() => {
      expectDraftLocation('?dir=%2FUsers%2Fpeter%2Fworkspace%2Focman');
    });
  });

  it('opens a new conversation on the selected project owner', async () => {
    mocks.uiState.paletteMode = 'project-session';
    mocks.apiState.getProjects.mockResolvedValue([{
      directory: '/remote/repo', remoteId: 'box', platform: 'r-box:opencode',
      sessionCount: 1, messageCount: 1, totalTokensIn: 1, totalTokensOut: 1, lastUsed: 1,
    }]);

    renderPalette();
    fireEvent.click(await screen.findByText('remote/repo'));

    await waitFor(() => {
      expectDraftLocation('?dir=%2Fremote%2Frepo&remoteId=box&platform=r-box%3Aopencode');
    });
  });

  it('shows "Create new project" at the end of the known-project picker, even when search matches nothing', async () => {
    mocks.uiState.paletteMode = 'project-session';

    renderPalette();

    // Present with the full list.
    expect(await screen.findByText('Create new project')).toBeInTheDocument();

    fireEvent.change(screen.getByPlaceholderText('Select a project to start a session...'), {
      target: { value: 'banfron' },
    });
    expect(screen.getByText('workspace/banana-frontend')).toBeInTheDocument();

    // Still present when the search filters every project out.
    fireEvent.change(screen.getByPlaceholderText('Select a project to start a session...'), {
      target: { value: 'zzz-no-match' },
    });
    expect(screen.queryByText('workspace/ocman')).not.toBeInTheDocument();
    expect(screen.getByText('Create new project')).toBeInTheDocument();
  });

  it('ranks known projects by match quality before recency', async () => {
    mocks.uiState.paletteMode = 'project-session';
    renderPalette();
    await screen.findByText('workspace/ocman');

    // "re" is a word-start substring only in .../research; ocman (most
    // recent) and banana-frontend match as scattered letters.
    fireEvent.change(screen.getByPlaceholderText('Select a project to start a session...'), {
      target: { value: 're' },
    });
    const labels = screen.getAllByRole('option').map((o) => o.textContent ?? '');
    expect(labels.findIndex((l) => l.includes('workspace/research'))).toBe(0);
    // Equal scattered matches keep recency order.
    expect(labels.findIndex((l) => l.includes('workspace/ocman')))
      .toBeLessThan(labels.findIndex((l) => l.includes('workspace/banana-frontend')));
  });

  it('switches to the filesystem browser when "Create new project" is selected', async () => {
    mocks.uiState.paletteMode = 'project-session';

    renderPalette();

    fireEvent.click(await screen.findByText('Create new project'));

    expect(await screen.findByPlaceholderText('Browse project directories...')).toBeInTheDocument();
    expect(mocks.apiState.browseDirectories).toHaveBeenCalledWith(undefined, expect.any(AbortSignal));
  });

  it('exposes the results as listbox options the input drives', async () => {
    mocks.uiState.paletteMode = 'command';

    renderPalette();

    const input = await screen.findByPlaceholderText('> commands, :stats, sessions...');
    expect(input).toHaveAttribute('role', 'combobox');
    expect(input).toHaveAttribute('aria-expanded', 'true');

    let options = screen.getAllByRole('option');
    expect(options.length).toBeGreaterThan(1);
    expect(options[0]).toHaveAttribute('aria-selected', 'true');
    expect(input).toHaveAttribute('aria-activedescendant', options[0].id);

    fireEvent.keyDown(input, { key: 'ArrowDown' });

    options = screen.getAllByRole('option');
    expect(options[0]).toHaveAttribute('aria-selected', 'false');
    expect(options[1]).toHaveAttribute('aria-selected', 'true');
    expect(input).toHaveAttribute('aria-activedescendant', options[1].id);
  });

  it('opens the known-project picker for the scoped new-session command', async () => {
    mocks.uiState.paletteMode = 'command';

    renderPalette();

    fireEvent.click(await screen.findByText('New session in project'));

    expect(mocks.uiState.openProjectSessionPalette).toHaveBeenCalledOnce();
    expect(mocks.uiState.openProjectPalette).not.toHaveBeenCalled();
  });
});
