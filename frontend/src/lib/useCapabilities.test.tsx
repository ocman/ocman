// @vitest-environment jsdom
import { act, renderHook, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { api, type CapabilitiesResponse } from './api';
import { queryClient } from './queryClient';
import {
  useCapabilities, usePlatformCapabilities, useMultiPlatform,
  useOpencodeLaunch, useLocalOpenCode, useMultiHost, useMachinePlatforms,
} from './useCapabilities';

vi.mock('./useGlobalEvents', () => ({ onSseConnect: () => () => {} }));

const platformCaps = {
  composer: true, respondPermission: true, respondQuestion: true, abort: true,
  compact: true, fork: true, forkAtMessage: true, move: true, events: true,
  agentCatalog: true, modelCatalog: true, slashCommands: true, shellExec: true,
  fileChanges: true, sessionInfo: true, liveConnectionHint: 'launch the agent',
  autoApprove: true, permissionRules: true, revert: true, runtimeRestart: true,
};
const hostCaps = { gitDiff: true, worktrees: true, tmux: false, projects: true, whisper: true, opencodeLaunch: true, opencode: true };
function response(): CapabilitiesResponse {
  return {
    platforms: [{ id: 'opencode', displayName: 'OpenCode', available: true, capabilities: platformCaps }],
    worktreeSessions: true,
    hosts: [{ remoteId: 'local', remoteName: 'This machine', capabilities: hostCaps }],
  };
}

beforeEach(() => {
  vi.restoreAllMocks();
  queryClient.clear();
  vi.spyOn(api, 'capabilities').mockResolvedValue(response());
});

describe('capability queries', () => {
  it('deduplicates concurrent consumers and serves warm data without another fetch', async () => {
    const first = renderHook(() => useCapabilities());
    const second = renderHook(() => useCapabilities());
    expect(first.result.current).toBeNull();
    await waitFor(() => expect(first.result.current).toEqual(response()));
    await waitFor(() => expect(second.result.current).toEqual(response()));
    const warm = renderHook(() => useCapabilities());
    expect(warm.result.current).toEqual(response());
    expect(api.capabilities).toHaveBeenCalledTimes(1);
  });

  it('fails closed after an initial network error and recovers on a later mount', async () => {
    vi.mocked(api.capabilities).mockRejectedValueOnce(new Error('offline'));
    const failed = renderHook(() => useCapabilities());
    await waitFor(() => expect(queryClient.getQueryState(['capabilities'])?.status).toBe('error'));
    expect(failed.result.current).toBeNull();
    failed.unmount();
    const retry = renderHook(() => useCapabilities());
    await waitFor(() => expect(retry.result.current).toEqual(response()));
  });

  it('returns disabled flags until loaded and when no platform is selected', async () => {
    const selected = renderHook(() => usePlatformCapabilities('opencode'));
    expect(selected.result.current.composer).toBe(false);
    await waitFor(() => expect(selected.result.current).toEqual(platformCaps));
    const absent = renderHook(() => usePlatformCapabilities(undefined));
    expect(absent.result.current.composer).toBe(false);
    expect(absent.result.current.events).toBe(false);
  });

  it('retries a missing platform once and does not loop for an unknown ID', async () => {
    const remote = { ...response().platforms[0], id: 'r-abc:opencode' };
    vi.mocked(api.capabilities).mockResolvedValueOnce(response()).mockResolvedValue({ ...response(), platforms: [...response().platforms, remote] });
    const hook = renderHook(() => usePlatformCapabilities(remote.id));
    await waitFor(() => expect(hook.result.current.composer).toBe(true));
    expect(api.capabilities).toHaveBeenCalledTimes(2);
    const unknown = renderHook(() => usePlatformCapabilities('unknown-test-platform'));
    await waitFor(() => expect(api.capabilities).toHaveBeenCalledTimes(3));
    await act(async () => { await Promise.resolve(); });
    unknown.rerender();
    expect(unknown.result.current.composer).toBe(false);
    expect(api.capabilities).toHaveBeenCalledTimes(3);
  });

  it.each([
    [['opencode'], false],
    [['opencode', 'claude'], true],
    [['opencode', 'r-one:opencode', 'r-two:opencode'], false],
    [['r-without-separator'], false],
  ] as const)('counts base platforms in %j', async (ids, expected) => {
    vi.mocked(api.capabilities).mockResolvedValue({ ...response(), platforms: ids.map(id => ({ ...response().platforms[0], id })) });
    const hook = renderHook(() => useMultiPlatform());
    await waitFor(() => expect(queryClient.getQueryData(['capabilities'])).toBeDefined());
    await waitFor(() => expect(hook.result.current).toBe(expected));
  });

  it('uses the named owner and never borrows local launch capabilities', async () => {
    queryClient.setQueryData(['capabilities'], {
      ...response(), hosts: [...response().hosts!, { remoteId: 'remote', remoteName: 'Remote', capabilities: { ...hostCaps, opencodeLaunch: false } }],
    });
    expect(renderHook(() => useOpencodeLaunch('local')).result.current).toBe(true);
    expect(renderHook(() => useOpencodeLaunch('remote')).result.current).toBe(false);
    expect(renderHook(() => useOpencodeLaunch('missing-owner')).result.current).toBe(false);
  });

  it('reads local OpenCode availability independently of other owners', () => {
    queryClient.setQueryData(['capabilities'], { ...response(), hosts: [
      { remoteId: 'remote', remoteName: 'Remote', capabilities: hostCaps },
      { remoteId: 'local', remoteName: 'Local', capabilities: { ...hostCaps, opencode: false } },
    ] });
    expect(renderHook(() => useLocalOpenCode()).result.current).toBe(false);
  });

  it('supports the first-host fallback and empty host lists', () => {
    queryClient.setQueryData(['capabilities'], { ...response(), hosts: [{ remoteId: 'remote', remoteName: 'Remote', capabilities: hostCaps }] });
    expect(renderHook(() => useOpencodeLaunch()).result.current).toBe(true);
    expect(renderHook(() => useLocalOpenCode()).result.current).toBe(true);
    queryClient.setQueryData(['capabilities'], { ...response(), hosts: [] });
    expect(renderHook(() => useOpencodeLaunch()).result.current).toBe(false);
    expect(renderHook(() => useLocalOpenCode()).result.current).toBe(false);
  });

  it('counts hosts and filters platforms by their owner', () => {
    queryClient.setQueryData(['capabilities'], { ...response(), platforms: [response().platforms[0],
      { ...response().platforms[0], id: 'r-box:opencode', remoteId: 'box' }],
    hosts: [...response().hosts!, { remoteId: 'box', remoteName: 'Box', capabilities: hostCaps }] });
    expect(renderHook(() => useMultiHost()).result.current).toBe(true);
    expect(renderHook(() => useMachinePlatforms()).result.current.map(p => p.id)).toEqual(['opencode']);
    expect(renderHook(() => useMachinePlatforms('box')).result.current.map(p => p.id)).toEqual(['r-box:opencode']);
  });
});
