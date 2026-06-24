// @vitest-environment jsdom
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { MemoryRouter } from 'react-router-dom';
import { beforeEach, describe, expect, it, vi } from 'vitest';

const mocks = vi.hoisted(() => {
  const uiState = {
    paletteOpen: true,
    paletteMode: 'project' as const,
    closePalette: vi.fn(),
    openProjectPalette: vi.fn(),
    openShortcuts: vi.fn(),
    openWorktreeForm: vi.fn(),
    dispatchCommand: vi.fn(),
  };
  const apiState = {
    cachedSessions: [],
    getProjects: vi.fn(async () => []),
    browseDirectories: vi.fn(),
    searchDirectories: vi.fn(),
    createSession: vi.fn(),
    launchOpencodeInTmux: vi.fn(),
    seedNewSession: vi.fn(),
    refreshCachedSessions: vi.fn(async () => []),
    getTmuxSessions: vi.fn(async () => ({ available: false, sessions: [] })),
    getTmuxClients: vi.fn(async () => ({ available: false, clients: [] })),
    switchTmuxSession: vi.fn(),
  };
  return { uiState, apiState };
});

vi.mock('../lib/uiStore', () => {
  const useUiStore = Object.assign(
    (selector?: (state: typeof mocks.uiState) => unknown) => (
      selector ? selector(mocks.uiState) : mocks.uiState
    ),
    { getState: () => mocks.uiState },
  );
  return { useUiStore };
});

vi.mock('../lib/apiStore', () => ({
  useApiStore: (selector: (state: typeof mocks.apiState) => unknown) => selector(mocks.apiState),
}));

vi.mock('../lib/useCapabilities', () => ({
  useWorktreeSessions: () => false,
  useAgentLoops: () => false,
}));

vi.mock('../lib/machinePicker', () => ({
  resolveTargetForDir: vi.fn(async () => ''),
}));

import { CommandPalette } from './CommandPalette';

function renderPalette() {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  const invalidateQueries = vi.spyOn(queryClient, 'invalidateQueries');
  render(
    <QueryClientProvider client={queryClient}>
      <MemoryRouter>
        <CommandPalette />
      </MemoryRouter>
    </QueryClientProvider>,
  );
  return { queryClient, invalidateQueries };
}

describe('CommandPalette project mode', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    Element.prototype.scrollIntoView = vi.fn();
    mocks.uiState.paletteOpen = true;
    mocks.uiState.paletteMode = 'project';
    mocks.apiState.cachedSessions = [];
    mocks.apiState.getProjects.mockResolvedValue([]);
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
    mocks.apiState.createSession.mockResolvedValue({ id: 'new-session' });
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

  it('creates a session and refreshes projects for the selected browsed directory', async () => {
    const { invalidateQueries } = renderPalette();

    fireEvent.click(await screen.findByText('Use this directory'));

    await waitFor(() => {
      expect(mocks.apiState.createSession).toHaveBeenCalledWith('/Users/peter', 'opencode', undefined);
    });
    expect(mocks.apiState.seedNewSession).toHaveBeenCalledWith('new-session', '/Users/peter', 'opencode');
    expect(invalidateQueries).toHaveBeenCalledWith({ queryKey: ['projects'] });
    expect(invalidateQueries).toHaveBeenCalledWith({ queryKey: ['sessions'] });
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
});
