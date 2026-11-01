// @vitest-environment jsdom
import { act, renderHook, waitFor } from '@testing-library/react';
import { beforeEach, expect, it, vi } from 'vitest';
import { api, type Project } from './api';
import { useProjectIdentityIndex } from './useProjectIdentityIndex';

const target = { directory: '/final', remoteId: 'box', remoteName: 'Box', projectKey: 'final' };
const resolved = { key: 'final', directory: '/final', remoteId: 'box', remoteName: 'Box' };
beforeEach(() => {
  vi.restoreAllMocks();
  vi.spyOn(api, 'resolveProjectIdentities').mockImplementation(async (checkouts) => checkouts.map(() => resolved));
});

it('resolves only unknown owner-qualified roots, deduplicated and in stable order', async () => {
  const inputs = [{ directory: '/source' }, { directory: '/source' }, { directory: '/remote/clone', remoteId: 'other' }, { directory: '/final', remoteId: 'box' }, { directory: '' }];
  const { result, rerender } = renderHook(({ checkouts }) => useProjectIdentityIndex([target], checkouts), { initialProps: { checkouts: inputs } });
  await waitFor(() => expect(result.current('/source')).toEqual(resolved));
  expect(api.resolveProjectIdentities).toHaveBeenCalledWith([
    { directory: '/source', remoteId: undefined },
    { directory: '/remote/clone', remoteId: 'other' },
  ], expect.any(AbortSignal));
  expect(result.current('/remote/clone', 'other')).toEqual(resolved);
  expect(result.current('/source', 'other').key).not.toBe('final');
  rerender({ checkouts: [...inputs].reverse() });
  expect(api.resolveProjectIdentities).toHaveBeenCalledTimes(1);
});

it('does not resolve known roots or disabled consumers', () => {
  const { result } = renderHook(() => useProjectIdentityIndex([target], [{ directory: '/source' }], false));
  expect(result.current('/source').directory).toBe('/source');
  renderHook(() => useProjectIdentityIndex([target], [{ directory: '/final', remoteId: 'box' }]));
  expect(api.resolveProjectIdentities).not.toHaveBeenCalled();
});

it('refetches final identities when the project catalog changes', async () => {
  const { result, rerender } = renderHook(({ projects }) => useProjectIdentityIndex(projects, [{ directory: '/source' }]), { initialProps: { projects: [target] as Array<Partial<Project> & { directory: string }> } });
  await waitFor(() => expect(result.current('/source').key).toBe('final'));
  vi.mocked(api.resolveProjectIdentities).mockResolvedValue([{ key: 'next', directory: '/next' }]);
  rerender({ projects: [{ directory: '/next', projectKey: 'next' }] });
  await waitFor(() => expect(result.current('/source').key).toBe('next'));
  expect(api.resolveProjectIdentities).toHaveBeenCalledTimes(2);
});

it('folds worktrees before resolving their project without changing the input', async () => {
  const checkouts = [{ directory: '/local/.worktrees/repo/topic', remoteId: 'local' }];
  const { result } = renderHook(() => useProjectIdentityIndex([], checkouts));
  await waitFor(() => expect(result.current(checkouts[0].directory).key).toBe('final'));
  expect(api.resolveProjectIdentities).toHaveBeenCalledWith([{ directory: '/local/repo', remoteId: 'local' }], expect.any(AbortSignal));
  expect(checkouts[0].directory).toBe('/local/.worktrees/repo/topic');
});

it('retains directory identities while resolution is pending and cancels unused requests', async () => {
  vi.mocked(api.resolveProjectIdentities).mockImplementation(() => new Promise(() => {}));
  const { result, unmount } = renderHook(() => useProjectIdentityIndex([], [{ directory: '/source' }]));
  expect(result.current('/source').directory).toBe('/source');
  const signal = vi.mocked(api.resolveProjectIdentities).mock.calls[0][1]!;
  await act(async () => unmount());
  expect(signal.aborted).toBe(true);
});
