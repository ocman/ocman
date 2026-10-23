// @vitest-environment jsdom
import { waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import type { WorkingTreeDiff } from './api';
import { renderHookWithQuery } from '../test/renderWithQuery';

const diff: WorkingTreeDiff = { repo: '/x', branch: 'main', ahead: 0, behind: 0, files: [], truncated: false };
const getGitDiff = vi.fn<(dir: string, opts?: { fresh?: boolean }, signal?: AbortSignal) => Promise<WorkingTreeDiff>>(() => Promise.resolve(diff));
vi.mock('./apiStore', () => ({
  useApiStore: (selector: (state: { getGitDiff: typeof getGitDiff }) => unknown) => selector({ getGitDiff }),
}));

const { useWorkingTreeDiff } = await import('./useWorkingTreeDiff');

describe('useWorkingTreeDiff', () => {
  beforeEach(() => {
    getGitDiff.mockReset();
    getGitDiff.mockResolvedValue(diff);
  });

  it('returns the empty diff without fetching when disabled', () => {
    const { result } = renderHookWithQuery(() => useWorkingTreeDiff('/repo', { enabled: false }));
    expect(result.current).toMatchObject({ loading: false, notRepo: false, data: { repo: '', branch: '', files: [] } });
    expect(getGitDiff).not.toHaveBeenCalled();
  });

  it('fetches a fresh diff with an AbortSignal', async () => {
    const { result } = renderHookWithQuery(() => useWorkingTreeDiff('/repo'));
    expect(result.current.loading).toBe(true);
    await waitFor(() => expect(result.current.data).toEqual(diff));
    expect(getGitDiff).toHaveBeenCalledWith('/repo', { fresh: true }, expect.any(AbortSignal));
  });

  it('skips the fetch when no dir is provided', () => {
    renderHookWithQuery(() => useWorkingTreeDiff(undefined));
    expect(getGitDiff).not.toHaveBeenCalled();
  });

  it('sets notRepo on a 404 error and reports no error', async () => {
    getGitDiff.mockRejectedValue(new Error('404 not found'));
    const { result } = renderHookWithQuery(() => useWorkingTreeDiff('/repo'));
    await waitFor(() => expect(result.current.notRepo).toBe(true));
    expect(result.current.error).toBeNull();
    expect(result.current.data).toMatchObject({ repo: '', files: [] });
  });

  it('surfaces a non-404 fetch error without setting notRepo', async () => {
    getGitDiff.mockRejectedValue(new Error('boom'));
    const { result } = renderHookWithQuery(() => useWorkingTreeDiff('/repo'));
    await waitFor(() => expect(result.current.error).toBe('boom'));
    expect(result.current.notRepo).toBe(false);
  });
});
