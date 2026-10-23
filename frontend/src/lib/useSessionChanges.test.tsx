// @vitest-environment jsdom
import { waitFor } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import type { SessionChanges } from './api';
import { renderHookWithQuery } from '../test/renderWithQuery';

const changes: SessionChanges = { sessionId: 'sess-1', supported: true, totalAdditions: 1, totalDeletions: 0, filesChanged: 1, files: [] };
const getSessionChanges = vi.fn<(id: string, signal?: AbortSignal) => Promise<SessionChanges>>(() => Promise.resolve(changes));
vi.mock('./apiStore', () => ({
  useApiStore: (selector: (state: { getSessionChanges: typeof getSessionChanges }) => unknown) => selector({ getSessionChanges }),
}));

const { useSessionChanges } = await import('./useSessionChanges');

describe('useSessionChanges', () => {
  it('returns EMPTY_CHANGES without fetching when disabled', () => {
    getSessionChanges.mockClear();
    const { result } = renderHookWithQuery(() => useSessionChanges('sess-1', { enabled: false }));
    expect(result.current.loading).toBe(false);
    expect(result.current.data).toMatchObject({ supported: false, sessionId: '' });
    expect(getSessionChanges).not.toHaveBeenCalled();
  });

  it('fetches the session changes with an AbortSignal', async () => {
    getSessionChanges.mockClear();
    const { result } = renderHookWithQuery(() => useSessionChanges('sess-1'));
    await waitFor(() => expect(result.current.data).toEqual(changes));
    expect(getSessionChanges).toHaveBeenCalledWith('sess-1', expect.any(AbortSignal));
  });

  it('skips the fetch when no sessionId is provided', () => {
    getSessionChanges.mockClear();
    renderHookWithQuery(() => useSessionChanges(undefined));
    expect(getSessionChanges).not.toHaveBeenCalled();
  });
});
