// @vitest-environment jsdom
import type { ReactNode } from 'react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { renderHook } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { __resetActivityScopesForTests, activityScopeSnapshot } from './activityScopes';
import {
  useActivity,
  useHourly,
  useHourlyTokens,
  useMetrics,
  useModels,
  usePermissionStats,
  useProjects,
  useSessions,
} from './queries';
import { useGitInfo } from './useGitInfo';
import { __resetForTests as resetNotify, useNotifyStore } from './useNotifyData';
import { useSession } from '../pages/session-detail/useSession';

vi.mock('./api', () => ({
  api: {
    sessions: vi.fn(() => new Promise(() => {})),
    projects: vi.fn(() => new Promise(() => {})),
    metrics: vi.fn(() => new Promise(() => {})),
    permissionStats: vi.fn(() => new Promise(() => {})),
    activity: vi.fn(() => new Promise(() => {})),
    models: vi.fn(() => new Promise(() => {})),
    hourly: vi.fn(() => new Promise(() => {})),
    hourlyTokens: vi.fn(() => new Promise(() => {})),
  },
  fetchJSON: vi.fn(() => new Promise(() => {})),
}));

class NoopEventSource {
  onopen = null;
  onmessage = null;
  onerror = null;
  addEventListener() {}
  close() {}
}

globalThis.EventSource = NoopEventSource as unknown as typeof EventSource;

function wrapper({ children }: { children: ReactNode }) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return <QueryClientProvider client={client}>{children}</QueryClientProvider>;
}

describe('activity scope wiring', () => {
  beforeEach(() => {
    resetNotify();
    __resetActivityScopesForTests();
  });
  afterEach(() => {
    resetNotify();
    __resetActivityScopesForTests();
  });

  it('tracks enabled central query owners and releases them on unmount', () => {
    const { unmount } = renderHook(() => {
      useSessions();
      useProjects();
      useMetrics();
      useSessions(undefined, { enabled: false });
    }, { wrapper });

    expect(activityScopeSnapshot()).toEqual(['metrics', 'projects', 'sessions']);
    unmount();
    expect(activityScopeSnapshot()).toEqual([]);
  });

  it('tracks permission stats as metrics demand and releases it on unmount', () => {
    const { unmount } = renderHook(() => {
      usePermissionStats();
      usePermissionStats(undefined, { enabled: false });
    }, { wrapper });

    expect(activityScopeSnapshot()).toEqual(['metrics']);
    unmount();
    expect(activityScopeSnapshot()).toEqual([]);
  });

  it('tracks usage queries as metrics demand until the last owner unmounts', () => {
    const { unmount } = renderHook(() => {
      useActivity();
      useModels();
      useHourly();
      useHourlyTokens();
      useActivity(undefined, { enabled: false });
    }, { wrapper });

    expect(activityScopeSnapshot()).toEqual(['metrics']);
    unmount();
    expect(activityScopeSnapshot()).toEqual([]);
  });

  it('tracks one git-status scope per canonical directory', () => {
    const { rerender, unmount } = renderHook(
      ({ dirs }) => useGitInfo(dirs, 'local'),
      { initialProps: { dirs: ['/repo/b', ' /repo/a ', '/repo/b'] } },
    );

    expect(activityScopeSnapshot()).toEqual([
      'git-status:/repo/a',
      'git-status:/repo/b',
    ]);

    rerender({ dirs: ['/repo/c'] });
    expect(activityScopeSnapshot()).toEqual(['git-status:/repo/c']);
    unmount();
    expect(activityScopeSnapshot()).toEqual([]);
  });

  it('tracks the shared notify poller only while it has consumers', () => {
    useNotifyStore.getState().subscribe();
    useNotifyStore.getState().subscribe();
    expect(activityScopeSnapshot()).toEqual(['sessions']);

    useNotifyStore.getState().unsubscribe();
    expect(activityScopeSnapshot()).toEqual(['sessions']);
    useNotifyStore.getState().unsubscribe();
    expect(activityScopeSnapshot()).toEqual([]);
  });

  it('tracks the concrete session owned by useSession', () => {
    const fetchSession = vi.fn(() => new Promise<never>(() => {}));
    const { rerender, unmount } = renderHook(
      ({ id }) => useSession(id, { fetchSession }),
      { initialProps: { id: 'one' as string | undefined } },
    );
    expect(activityScopeSnapshot()).toEqual(['session:one']);

    rerender({ id: 'two' });
    expect(activityScopeSnapshot()).toEqual(['session:two']);
    rerender({ id: 'new' });
    expect(activityScopeSnapshot()).toEqual([]);
    unmount();
  });
});
