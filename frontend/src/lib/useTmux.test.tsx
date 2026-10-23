// @vitest-environment jsdom
import { renderHook, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { queryClient } from './queryClient';

const store = vi.hoisted(() => ({
  getTmuxSessions: vi.fn(),
  getTmuxClients: vi.fn(),
  switchTmuxSession: vi.fn(),
  launchOpencodeInTmux: vi.fn(),
}));
vi.mock('./api', () => ({
  api: { tmuxSessions: store.getTmuxSessions, tmuxClients: store.getTmuxClients,
    tmuxSwitch: store.switchTmuxSession, tmuxLaunchOpencode: store.launchOpencodeInTmux },
}));

const { useTmux } = await import('./useTmux');

describe('useTmux', () => {
  beforeEach(() => {
    queryClient.clear();
    vi.clearAllMocks();
    store.getTmuxSessions.mockResolvedValue({ available: true, sessions: [{ name: 'work', resolvedPath: '/repo' }] });
    store.getTmuxClients.mockResolvedValue({ clients: [{ tty: '/dev/ttys001' }] });
  });

  it('loads sessions and clients and finds a session by directory', async () => {
    const { result } = renderHook(() => useTmux());
    await waitFor(() => expect(result.current.available).toBe(true));
    expect(result.current.isLocal).toBe(true);
    expect(result.current.clients).toEqual([{ tty: '/dev/ttys001' }]);
    expect(result.current.findSession('/repo')?.name).toBe('work');
    expect(result.current.findSession('/other')).toBeUndefined();
  });

  it('defaults missing lists to empty', async () => {
    store.getTmuxSessions.mockResolvedValue({ available: true });
    store.getTmuxClients.mockResolvedValue({});
    const { result } = renderHook(() => useTmux());
    await waitFor(() => expect(result.current.available).toBe(true));
    expect(result.current.sessions).toEqual([]);
    expect(result.current.clients).toEqual([]);
  });

  it('reports tmux as unavailable when loading fails', async () => {
    store.getTmuxSessions.mockRejectedValue(new Error('no tmux'));
    const { result } = renderHook(() => useTmux());
    await waitFor(() => expect(store.getTmuxSessions).toHaveBeenCalled());
    expect(result.current.available).toBe(false);
  });

  it('forwards switch and launch actions to the store', async () => {
    store.launchOpencodeInTmux.mockResolvedValue({ session: 'work' });
    const { result } = renderHook(() => useTmux());
    await waitFor(() => expect(result.current.available).toBe(true));
    await result.current.switchSession('work', '/dev/ttys001');
    expect(store.switchTmuxSession).toHaveBeenCalledWith('work', '/dev/ttys001');
    await expect(result.current.launchOpencode('/repo')).resolves.toEqual({ session: 'work' });
  });
});
