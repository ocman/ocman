// @vitest-environment jsdom
import { fireEvent, render, screen } from '@testing-library/react';
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

import { CommandPalette } from './CommandPalette';

describe('CommandPalette project mode', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    Element.prototype.scrollIntoView = vi.fn();
    mocks.uiState.paletteOpen = true;
    mocks.uiState.paletteMode = 'project';
    mocks.apiState.cachedSessions = [];
    mocks.apiState.getProjects.mockResolvedValue([]);
    mocks.apiState.getTmuxSessions.mockResolvedValue({ available: false, sessions: [] });
    mocks.apiState.getTmuxClients.mockResolvedValue({ available: false, clients: [] });
  });

  it('renders a create-session option for a typed absolute directory', async () => {
    render(
      <MemoryRouter>
        <CommandPalette />
      </MemoryRouter>,
    );

    fireEvent.change(screen.getByPlaceholderText(/absolute directory/i), {
      target: { value: '/Users/peter/workspace/new-project' },
    });

    expect(await screen.findByText('Create session in this directory')).toBeInTheDocument();
    expect(screen.getByText('/Users/peter/workspace/new-project')).toBeInTheDocument();
  });
});
