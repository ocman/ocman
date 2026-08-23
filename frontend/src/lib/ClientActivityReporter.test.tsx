// @vitest-environment jsdom
import { act, render } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { api } from './api';
import { acquireActivityScope, __resetActivityScopesForTests } from './activityScopes';
import {
  ACTIVITY_HEARTBEAT_MS,
  ACTIVITY_TTL_MS,
  RECENT_INTERACTION_MS,
  ClientActivityReporter,
} from './ClientActivityReporter';

vi.mock('./api', () => ({
  api: { clientActivity: vi.fn().mockResolvedValue(undefined) },
}));

const report = vi.mocked(api.clientActivity);

async function flush() {
  await act(async () => { await Promise.resolve(); });
}

describe('ClientActivityReporter', () => {
  beforeEach(() => {
    vi.useFakeTimers();
    vi.clearAllMocks();
    __resetActivityScopesForTests();
    Object.defineProperty(document, 'hidden', { configurable: true, value: false });
    vi.spyOn(document, 'hasFocus').mockReturnValue(true);
  });

  afterEach(() => {
    __resetActivityScopesForTests();
    vi.restoreAllMocks();
    vi.useRealTimers();
  });

  it('serializes a stable client lease and immediately reports scope and browser-state changes', async () => {
    const { unmount } = render(<ClientActivityReporter />);
    await flush();

    expect(report).toHaveBeenLastCalledWith({
      clientId: expect.any(String),
      visible: true,
      focused: true,
      recentlyInteracted: false,
      scopes: [],
      ttlMs: ACTIVITY_TTL_MS,
    });
    const clientId = report.mock.calls[0][0].clientId;

    const releaseSessions = acquireActivityScope('sessions');
    const releaseProjects = acquireActivityScope('projects');
    await flush();
    expect(report).toHaveBeenLastCalledWith(expect.objectContaining({
      clientId,
      scopes: ['projects', 'sessions'],
    }));

    Object.defineProperty(document, 'hidden', { configurable: true, value: true });
    document.dispatchEvent(new Event('visibilitychange'));
    await flush();
    expect(report).toHaveBeenLastCalledWith(expect.objectContaining({ visible: false }));

    vi.mocked(document.hasFocus).mockReturnValue(false);
    window.dispatchEvent(new Event('blur'));
    await flush();
    expect(report).toHaveBeenLastCalledWith(expect.objectContaining({ focused: false }));

    releaseProjects();
    releaseSessions();
    unmount();
  });

  it('reports stale-to-recent interaction once, then reports its expiration', async () => {
    render(<ClientActivityReporter />);
    await flush();
    report.mockClear();

    window.dispatchEvent(new Event('pointerdown'));
    await flush();
    expect(report).toHaveBeenCalledTimes(1);
    expect(report).toHaveBeenLastCalledWith(expect.objectContaining({ recentlyInteracted: true }));

    window.dispatchEvent(new Event('keydown'));
    await flush();
    expect(report).toHaveBeenCalledTimes(1);

    await act(async () => { await vi.advanceTimersByTimeAsync(RECENT_INTERACTION_MS); });
    // One 25s heartbeat lands before the 30s interaction expiry.
    expect(report).toHaveBeenCalledTimes(3);
    expect(report).toHaveBeenLastCalledWith(expect.objectContaining({ recentlyInteracted: false }));
  });

  it('heartbeats and coalesces changes behind one in-flight request', async () => {
    let resolveFirst!: () => void;
    report.mockImplementationOnce(() => new Promise<void>((resolve) => { resolveFirst = resolve; }));
    const { unmount } = render(<ClientActivityReporter />);
    expect(report).toHaveBeenCalledTimes(1);

    const releaseProjects = acquireActivityScope('projects');
    const releaseSessions = acquireActivityScope('sessions');
    window.dispatchEvent(new Event('pointerdown'));
    expect(report).toHaveBeenCalledTimes(1);

    resolveFirst();
    await flush();
    expect(report).toHaveBeenCalledTimes(2);
    expect(report).toHaveBeenLastCalledWith(expect.objectContaining({
      recentlyInteracted: true,
      scopes: ['projects', 'sessions'],
    }));

    await act(async () => { await vi.advanceTimersByTimeAsync(ACTIVITY_HEARTBEAT_MS); });
    expect(report).toHaveBeenCalledTimes(3);

    releaseProjects();
    releaseSessions();
    unmount();
  });

  it('swallows failures and removes timers/listeners without sending a release', async () => {
    report.mockRejectedValueOnce(new Error('offline'));
    const removeDocument = vi.spyOn(document, 'removeEventListener');
    const removeWindow = vi.spyOn(window, 'removeEventListener');
    const { unmount } = render(<ClientActivityReporter />);
    await flush();
    expect(report).toHaveBeenCalledTimes(1);

    unmount();
    await act(async () => { await vi.advanceTimersByTimeAsync(ACTIVITY_HEARTBEAT_MS * 2); });

    expect(report).toHaveBeenCalledTimes(1);
    expect(removeDocument).toHaveBeenCalledWith('visibilitychange', expect.any(Function));
    expect(removeWindow).toHaveBeenCalledWith('pointerdown', expect.any(Function));
  });
});
