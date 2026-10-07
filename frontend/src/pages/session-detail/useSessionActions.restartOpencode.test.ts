// @vitest-environment jsdom
//
// Tests for the `/restart-opencode` command path in useSessionActions.
// On success it walks the toast through "Restarting..." -> "Restarted"
// and clears the pending indicator; on failure it hides the toast and
// reports the error via pending.fail.

import { describe, it, expect, beforeEach, vi } from 'vitest';
import { renderHook, act, waitFor } from '@testing-library/react';
import { createRef } from 'react';
import type { MutableRefObject } from 'react';
import type { Session } from '../../lib/api';
import { useSessionActions, type UseSessionActionsOptions } from './useSessionActions';
import { api } from '../../lib/api';
import { useSlashMenu } from '../../components/assistant/useSlashMenu';

vi.mock('../../lib/apiStore', () => ({
  useApiStore: Object.assign(
    (selector: (s: Record<string, unknown>) => unknown) =>
      selector({
        sendMessage: vi.fn().mockResolvedValue(undefined),
        abortSession: vi.fn().mockResolvedValue(undefined),
        archiveSession: vi.fn().mockResolvedValue(undefined),
      }),
    { getState: () => ({ pushClosedSession: vi.fn() }) },
  ),
}));

vi.mock('../../lib/api', () => ({
  api: { restartOpencode: vi.fn(), reloadOpencode: vi.fn(), commands: vi.fn(), debugLog: vi.fn().mockResolvedValue(undefined) },
}));

const restartOpencode = vi.mocked(api.restartOpencode);

function makeOptions(over: Partial<UseSessionActionsOptions> = {}): UseSessionActionsOptions {
  const pending = { pending: null, begin: vi.fn(), fail: vi.fn(), clear: vi.fn() };
  return {
    session: { id: 'sess-1', platform: 'opencode', directory: '/p', timeUpdated: 0 },
    portAvailable: true,
    caps: { shellExec: true } as UseSessionActionsOptions['caps'],
    pendingPermission: null,
    pendingQuestion: null,
    selectedModel: '',
    selectedAgent: '',
    selectedReasoning: '',
    activeAgent: '',
    recentSessionsRef: createRef<Session[]>() as MutableRefObject<Session[]>,
    messagesRef: { current: [] },
    partsRef: { current: [] },
    isRunningRef: { current: false },
    failedSends: [],
    setFailedSends: vi.fn(),
    pending: pending as unknown as UseSessionActionsOptions['pending'],
    navigate: vi.fn(),
    navigateToSession: vi.fn(),
    openWorktreeForm: vi.fn(),
    handleCompact: vi.fn().mockResolvedValue(undefined),
    handleNewSession: vi.fn().mockResolvedValue(undefined),
    handleTmuxShortcut: vi.fn(),
    setShowRenameModal: vi.fn(),
    setShowForkPicker: vi.fn(),
    setShowMovePicker: vi.fn(),
    setShowRenameToast: vi.fn(),
    setShowDisconnectedToast: vi.fn(),
    setRestartToastMessage: vi.fn(),
    setCopyToastMessage: vi.fn(),
    ...over,
  };
}

beforeEach(() => {
  restartOpencode.mockReset();
  vi.mocked(api.reloadOpencode).mockReset();
});

describe('useSessionActions — /reload-opencode', () => {
  it.each(['resolve', 'reject'])('ignores a late reload %s after navigating to another session', async (outcome) => {
    let resolve!: () => void;
    let reject!: (error: Error) => void;
    vi.mocked(api.reloadOpencode).mockImplementationOnce(() => new Promise<void>((ok, fail) => { resolve = ok; reject = fail; }));
    const reloadCapabilities = vi.fn();
    const opts = makeOptions({ reloadCapabilities });
    const { result, rerender } = renderHook((options) => useSessionActions(options), { initialProps: opts });
    let delivery!: Promise<void>;
    act(() => { delivery = result.current.handleCommand('reload-opencode', ''); });
    rerender(makeOptions({ session: { id: 'sess-2', platform: 'r-owner:opencode', directory: '/other', timeUpdated: 0 } }));
    await act(async () => {
      if (outcome === 'resolve') resolve(); else reject(new Error('old reload failed'));
      await delivery;
    });
    expect(reloadCapabilities).not.toHaveBeenCalled();
    expect(opts.pending.clear).not.toHaveBeenCalled();
    expect(opts.pending.fail).not.toHaveBeenCalled();
    expect(opts.setRestartToastMessage).toHaveBeenCalledTimes(1);
  });

  it('only applies the latest overlapping reload completion', async () => {
    let finishOld!: () => void;
    vi.mocked(api.reloadOpencode).mockImplementationOnce(() => new Promise<void>((resolve) => { finishOld = resolve; }));
    vi.mocked(api.reloadOpencode).mockResolvedValue(undefined);
    const reloadCapabilities = vi.fn();
    const opts = makeOptions({ reloadCapabilities });
    const { result } = renderHook(() => useSessionActions(opts));
    let old!: Promise<void>;
    act(() => { old = result.current.handleCommand('reload-opencode', ''); });
    await act(async () => { await result.current.handleCommand('reload-opencode', ''); });
    await act(async () => { finishOld(); await old; });
    expect(reloadCapabilities).toHaveBeenCalledTimes(1);
    expect(opts.pending.clear).toHaveBeenCalledTimes(1);
  });

  it('ignores reload completion when the route changes before the new session loads', async () => {
    let finish!: () => void;
    vi.mocked(api.reloadOpencode).mockImplementationOnce(() => new Promise<void>((resolve) => { finish = resolve; }));
    const reloadCapabilities = vi.fn();
    const opts = makeOptions({ routeSessionId: 'sess-1', reloadCapabilities });
    const { result, rerender } = renderHook((options) => useSessionActions(options), { initialProps: opts });
    let reload!: Promise<void>;
    act(() => { reload = result.current.handleCommand('reload-opencode', ''); });
    rerender({ ...opts, routeSessionId: 'sess-2' });
    await act(async () => { finish(); await reload; });
    expect(reloadCapabilities).not.toHaveBeenCalled();
    expect(opts.pending.clear).not.toHaveBeenCalled();
  });

  it('reloads configuration and refreshes the catalog while a turn is running', async () => {
    vi.mocked(api.reloadOpencode).mockResolvedValue(undefined);
    vi.mocked(api.commands).mockResolvedValueOnce([{ name: 'old-skill', source: 'skill' }]);
    const menu = renderHook(() => useSlashMenu('sess-1', { hasAgents: true, hasModels: true, activeAgent: 'build', hasVariants: false }));
    await waitFor(() => expect(menu.result.current.commands.some((c) => c.name === 'old-skill')).toBe(true));
    vi.mocked(api.commands).mockResolvedValue([{ name: 'new-skill', source: 'skill' }]);
    const reloadCapabilities = vi.fn();
    const opts = makeOptions({ isRunningRef: { current: true }, reloadCapabilities });
    const { result } = renderHook(() => useSessionActions(opts));
    await act(async () => { await result.current.handleCommand('reload-opencode', ''); });
    expect(api.reloadOpencode).toHaveBeenCalledWith('sess-1', 'opencode');
    expect(restartOpencode).not.toHaveBeenCalled();
    expect(opts.setRestartToastMessage).toHaveBeenLastCalledWith('Reloaded OpenCode configuration');
    expect(opts.pending.clear).toHaveBeenCalled();
    expect(reloadCapabilities).toHaveBeenCalledOnce();
    await waitFor(() => expect(menu.result.current.commands.some((c) => c.name === 'new-skill')).toBe(true));
    expect(menu.result.current.commands.some((c) => c.name === 'old-skill')).toBe(false);
  });

  it('reports reload failures without restarting or refreshing the catalog', async () => {
    vi.mocked(api.reloadOpencode).mockRejectedValue(new Error('Requires v2'));
    const reloadCapabilities = vi.fn();
    const opts = makeOptions({ reloadCapabilities });
    const { result } = renderHook(() => useSessionActions(opts));
    await act(async () => { await result.current.handleCommand('reload-opencode', ''); });
    expect(opts.pending.fail).toHaveBeenCalledWith('Requires v2');
    expect(opts.setRestartToastMessage).toHaveBeenLastCalledWith(null);
    expect(restartOpencode).not.toHaveBeenCalled();
    expect(reloadCapabilities).not.toHaveBeenCalled();
  });

  it('rejects arguments before calling the server', async () => {
    const opts = makeOptions();
    const { result } = renderHook(() => useSessionActions(opts));
    await act(async () => { await result.current.handleCommand('reload-opencode', 'all'); });
    expect(opts.pending.fail).toHaveBeenCalledWith('Usage: /reload-opencode');
    expect(api.reloadOpencode).not.toHaveBeenCalled();
  });
});

describe('useSessionActions — /restart-opencode', () => {
  it('shows progress then success toast on a successful restart', async () => {
    restartOpencode.mockResolvedValue({ restarted: 1 });
    const setRestartToastMessage = vi.fn();
    const pending = { pending: null, begin: vi.fn(), fail: vi.fn(), clear: vi.fn() };
    const opts = makeOptions({
      setRestartToastMessage,
      pending: pending as unknown as UseSessionActionsOptions['pending'],
    });
    const { result } = renderHook(() => useSessionActions(opts));

    await act(async () => {
      await result.current.handleCommand('restart-opencode', '');
    });

    expect(restartOpencode).toHaveBeenCalledWith('sess-1', { all: false, force: false });
    expect(setRestartToastMessage.mock.calls).toEqual([
      ['Restarting OpenCode when sessions are idle...'],
      ['Restarted OpenCode'],
    ]);
    expect(pending.clear).toHaveBeenCalled();
    expect(pending.fail).not.toHaveBeenCalled();
  });

  it('reloads the agent + model catalog after a successful restart', async () => {
    restartOpencode.mockResolvedValue({ restarted: 1 });
    const reloadCapabilities = vi.fn();
    const opts = makeOptions({ reloadCapabilities });
    const { result } = renderHook(() => useSessionActions(opts));

    await act(async () => {
      await result.current.handleCommand('restart-opencode', '');
    });

    expect(reloadCapabilities).toHaveBeenCalledTimes(1);
  });

  it('does not reload the catalog when the restart fails', async () => {
    restartOpencode.mockRejectedValue(new Error('no pane'));
    const reloadCapabilities = vi.fn();
    const opts = makeOptions({ reloadCapabilities });
    const { result } = renderHook(() => useSessionActions(opts));

    await act(async () => {
      await result.current.handleCommand('restart-opencode', '');
    });

    expect(reloadCapabilities).not.toHaveBeenCalled();
  });

  it('hides the toast and reports the error when the restart fails', async () => {
    restartOpencode.mockRejectedValue(new Error('no pane'));
    const setRestartToastMessage = vi.fn();
    const pending = { pending: null, begin: vi.fn(), fail: vi.fn(), clear: vi.fn() };
    const opts = makeOptions({
      setRestartToastMessage,
      pending: pending as unknown as UseSessionActionsOptions['pending'],
    });
    const { result } = renderHook(() => useSessionActions(opts));

    await act(async () => {
      await result.current.handleCommand('restart-opencode', '');
    });

    expect(setRestartToastMessage.mock.calls).toEqual([
      ['Restarting OpenCode when sessions are idle...'],
      [null],
    ]);
    expect(pending.fail).toHaveBeenCalledWith('no pane');
    expect(pending.clear).not.toHaveBeenCalled();
  });

  it('confirms before forcing a busy instance to restart', async () => {
    restartOpencode.mockResolvedValueOnce({ confirmationRequired: true, busySessions: ['sess-1'] }).mockResolvedValueOnce({ restarted: 1 });
    vi.spyOn(window, 'confirm').mockReturnValue(true);
    const opts = makeOptions();
    const { result } = renderHook(() => useSessionActions(opts));

    await act(async () => {
      await result.current.handleCommand('restart-opencode', 'now');
    });

    expect(window.confirm).toHaveBeenCalledWith('Running OpenCode instances and sessions will be stopped. Force restart this managed OpenCode instance?');
    expect(restartOpencode.mock.calls).toEqual([
      ['sess-1', { all: false, force: true }],
      ['sess-1', { all: false, force: true, confirmed: true }],
    ]);
  });

  it('does not force restart when the busy-instance confirmation is cancelled', async () => {
    restartOpencode.mockResolvedValue({ confirmationRequired: true, busySessions: ['sess-1'] });
    vi.spyOn(window, 'confirm').mockReturnValue(false);
    const opts = makeOptions();
    const { result } = renderHook(() => useSessionActions(opts));

    await act(async () => {
      await result.current.handleCommand('restart-opencode', 'now');
    });

    expect(restartOpencode.mock.calls).toEqual([['sess-1', { all: false, force: true }]]);
    expect(opts.pending.clear).toHaveBeenCalled();
  });

  it('rejects unsupported arguments without calling the API', async () => {
    const opts = makeOptions();
    const { result } = renderHook(() => useSessionActions(opts));

    await act(async () => {
      await result.current.handleCommand('restart-opencode', 'later');
    });

    expect(restartOpencode).not.toHaveBeenCalled();
    expect(opts.pending.fail).toHaveBeenCalledWith('Usage: /restart-opencode [all] [now]');
  });
});
