// @vitest-environment jsdom
import { act, renderHook } from '@testing-library/react';
import { expect, it, vi } from 'vitest';
import type { Project } from './api';
import { postJSON } from './api.requests';
import { useOneOffProjects } from './useOneOffProjects';

vi.mock('./api.requests', () => ({ postJSON: vi.fn() }));
const project: Project = { directory: '/repo', sessionCount: 2, lastUsed: 10,
  messageCount: 0, totalTokensIn: 0, totalTokensOut: 0 };

it('keeps loading state and regular projects, collapsing temporary folders per owner', () => {
  const onError = vi.fn();
  const { result, rerender } = renderHook(({ data }: { data: Project[] | undefined }) =>
    useOneOffProjects(data, 'local', onError), { initialProps: { data: undefined as Project[] | undefined } });
  expect(result.current.projects).toBeUndefined();
  rerender({ data: [project,
    { ...project, directory: '/tmp/ocman-one-off-a' },
    { ...project, directory: '/tmp/ocman-one-off-b', lastUsed: 20 },
    { ...project, directory: '/remote/tmp/ocman-one-off-c', remoteId: 'box', remoteName: 'Box' },
  ] });
  expect(result.current.projects).toEqual([project,
    expect.objectContaining({ directory: 'one-off', projectKey: 'one-off:local', sessionCount: 4, lastUsed: 20 }),
    expect.objectContaining({ directory: 'one-off', projectKey: 'one-off:box', remoteId: 'box', remoteName: 'Box' }),
  ]);
});

it('does not allocate for regular projects and prevents duplicate one-off allocations', async () => {
  const onError = vi.fn();
  const open = vi.fn();
  const { result } = renderHook(() => useOneOffProjects([], 'local', onError));
  await act(() => result.current.start(project, open));
  expect(open).toHaveBeenCalledWith('/repo', project);
  expect(postJSON).not.toHaveBeenCalled();
  let resolve!: (value: { directory: string }) => void;
  vi.mocked(postJSON).mockImplementationOnce(() => new Promise((done) => { resolve = done; }));
  const virtual = result.current.projects![0];
  let pending!: Promise<void>;
  act(() => { pending = result.current.start(virtual, open); });
  await act(() => result.current.start(virtual, open));
  expect(postJSON).toHaveBeenCalledOnce();
  await act(async () => { resolve({ directory: '/tmp/fresh' }); await pending; });
  expect(postJSON).toHaveBeenCalledWith('/api/sessions/one-off', { remoteId: 'local' });
  expect(open).toHaveBeenLastCalledWith('/tmp/fresh', virtual);
});

it('shows non-Error allocation failures without opening a draft', async () => {
  vi.mocked(postJSON).mockRejectedValueOnce('Unavailable');
  const onError = vi.fn();
  const open = vi.fn();
  const { result } = renderHook(() => useOneOffProjects([], 'box', onError));
  await act(() => result.current.start(result.current.projects![0], open));
  expect(onError).toHaveBeenLastCalledWith('Unavailable');
  expect(open).not.toHaveBeenCalled();
});
