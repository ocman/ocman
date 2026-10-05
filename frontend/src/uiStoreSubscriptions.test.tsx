// @vitest-environment jsdom
//
// GlobalHotkeys and CommandPalette are always mounted. Subscribing to
// the whole uiStore re-rendered them on every unrelated write, such as
// each sidebar-width update.
import { act, render } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { MemoryRouter } from 'react-router-dom';
import { describe, expect, it, vi } from 'vitest';
import { useUiStore } from './lib/uiStore';

const renders = vi.hoisted(() => ({ hotkeys: 0, palette: 0 }));

vi.mock('./lib/remoteLog');
vi.mock('./lib/shortcutRegistry', () => ({
  useShortcut: vi.fn(),
  useShortcutDispatcher: () => { renders.hotkeys += 1; },
}));
vi.mock('./components/CommandPalette', () => ({ CommandPalette: () => null }));
vi.mock('./components/WorktreeFormModal', () => ({ WorktreeFormModal: () => null }));
vi.mock('./components/KeyboardShortcutsDialog', () => ({ KeyboardShortcutsDialog: () => null }));
vi.mock('./lib/useCapabilities', () => ({ useOpencodeLaunch: () => false }));
vi.mock('./lib/usePluginActions', () => ({
  usePluginActions: () => {
    renders.palette += 1;
    return { actions: [], context: undefined, unavailable: false };
  },
}));

import { GlobalHotkeys } from './App';
const { CommandPalette } = await vi.importActual<typeof import('./components/CommandPalette')>(
  './components/CommandPalette',
);

function renderInRouter(node: React.ReactNode) {
  const client = new QueryClient();
  return render(
    <QueryClientProvider client={client}>
      <MemoryRouter>{node}</MemoryRouter>
    </QueryClientProvider>,
  );
}

describe('uiStore subscriptions', () => {
  it('GlobalHotkeys does not re-render on an unrelated uiStore change', () => {
    renderInRouter(<GlobalHotkeys />);
    const before = renders.hotkeys;
    act(() => { useUiStore.getState().setSidebarWidth(useUiStore.getState().sidebarWidth + 10); });
    expect(renders.hotkeys).toBe(before);
  });

  it('CommandPalette does not re-render on an unrelated uiStore change', () => {
    renderInRouter(<CommandPalette />);
    const before = renders.palette;
    act(() => { useUiStore.getState().setSidebarWidth(useUiStore.getState().sidebarWidth + 10); });
    expect(renders.palette).toBe(before);
  });
});
