// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { act, render, screen } from '@testing-library/react';
import * as Toast from '@radix-ui/react-toast';
import { McpAuthBanner } from './McpAuthBanner';
import type { MCPServer } from '../lib/api';

const mockServers = vi.hoisted(() => ({ current: [] as MCPServer[] }));

vi.mock('../lib/useCapabilities', () => ({
  usePlatformCapabilities: () => ({ sessionInfo: true }),
}));
vi.mock('../lib/useSessionInfo', () => ({
  useSessionInfo: () => ({
    data: { mcpServers: mockServers.current },
    loading: false,
    error: null,
    refresh: vi.fn(),
  }),
}));

// jsdom's localStorage lacks working methods in this test env — stub
// an in-memory Storage so the persisted-dismissal path is exercised.
const store = vi.hoisted(() => new Map<string, string>());
beforeEach(() => {
  vi.stubGlobal('localStorage', {
    getItem: (k: string) => store.get(k) ?? null,
    setItem: (k: string, v: string) => { store.set(k, String(v)); },
    removeItem: (k: string) => { store.delete(k); },
    clear: () => { store.clear(); },
  });
});

afterEach(() => {
  vi.useRealTimers();
  vi.unstubAllGlobals();
  store.clear();
});

function renderBanner() {
  return render(
    <Toast.Provider>
      <McpAuthBanner sessionId="s1" platformId="opencode" />
      <Toast.Viewport />
    </Toast.Provider>,
  );
}

describe('McpAuthBanner', () => {
  it('renders nothing when no server needs auth', () => {
    mockServers.current = [{ name: 'weave', status: 'connected' }];
    renderBanner();
    expect(screen.queryByTestId('mcp-auth-banner')).toBeNull();
  });

  it('shows server name and auth hint when a server needs auth', () => {
    mockServers.current = [
      { name: 'devtoys', status: 'needs_auth', authHint: 'opencode mcp auth devtoys' },
      { name: 'weave', status: 'connected' },
    ];
    renderBanner();
    const banner = screen.getByTestId('mcp-auth-banner');
    expect(banner).toHaveTextContent('MCP authentication required');
    expect(banner).toHaveTextContent('devtoys');
    expect(banner).toHaveTextContent('opencode mcp auth devtoys');
    expect(banner).not.toHaveTextContent('weave');
  });

  it('auto-hides after 10 seconds', () => {
    vi.useFakeTimers({ shouldAdvanceTime: false });
    mockServers.current = [{ name: 'devtoys', status: 'needs_auth' }];
    renderBanner();
    expect(screen.getByTestId('mcp-auth-banner')).toBeInTheDocument();
    act(() => { vi.advanceTimersByTime(10_500); });
    expect(screen.queryByTestId('mcp-auth-banner')).toBeNull();
  });

  it('stays dismissed across remounts for the same server set', () => {
    vi.useFakeTimers({ shouldAdvanceTime: false });
    mockServers.current = [{ name: 'devtoys', status: 'needs_auth' }];
    const { unmount } = renderBanner();
    act(() => { vi.advanceTimersByTime(10_500); });
    unmount();

    // Remount (e.g. navigating to another session) — same server set,
    // so the persisted dismissal keeps it hidden.
    renderBanner();
    expect(screen.queryByTestId('mcp-auth-banner')).toBeNull();

    // A different server set re-surfaces the toast.
    mockServers.current = [
      { name: 'devtoys', status: 'needs_auth' },
      { name: 'weave', status: 'needs_auth' },
    ];
    renderBanner();
    expect(screen.getByTestId('mcp-auth-banner')).toBeInTheDocument();
  });
});
