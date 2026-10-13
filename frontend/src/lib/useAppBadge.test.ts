// @vitest-environment jsdom
import { renderHook, waitFor } from '@testing-library/react';
import { afterEach, expect, it, vi } from 'vitest';
import { attentionSessionCount, updateAppBadge, useAppBadge } from './useAppBadge';
import { useNotifyData } from './useNotifyData';

vi.mock('./useNotifyData', () => ({ useNotifyData: vi.fn(), useNotifyStore: (select: (state: { lastFetched: number }) => number) => select({ lastFetched: 100 }) }));
afterEach(() => { vi.unstubAllGlobals(); vi.resetAllMocks(); });

it('counts distinct unread or attention sessions, including seen sessions needing input', () => {
  expect(attentionSessionCount([
    { id: 'one', platform: 'opencode', status: 'waiting', seen: false, pendingPermission: true, pendingQuestion: true },
    { id: 'one', platform: 'opencode', status: 'waiting', seen: false },
    { id: 'one', platform: 'r-owner:opencode', status: 'waiting', seen: false },
    { id: 'seen-prompt', status: 'busy', seen: true, pendingQuestion: true },
    { id: 'seen', status: 'waiting', seen: true },
    { id: 'judging', status: 'waiting', seen: false, suppressTerminal: true },
  ])).toBe(3);
});

it('updates the icon count and clears it when attention is resolved', async () => {
  const setAppBadge = vi.fn().mockResolvedValue(undefined);
  const clearAppBadge = vi.fn().mockResolvedValue(undefined);
  vi.stubGlobal('navigator', { setAppBadge, clearAppBadge });
  vi.mocked(useNotifyData).mockReturnValue([{ id: 'session', status: 'waiting', seen: false }]);
  const hook = renderHook(() => useAppBadge());
  await waitFor(() => expect(setAppBadge).toHaveBeenCalledWith(1));
  vi.mocked(useNotifyData).mockReturnValue([]);
  hook.rerender();
  await waitFor(() => expect(clearAppBadge).toHaveBeenCalledOnce());
});

it('does not clear an existing badge while the initial snapshot is loading', () => {
  const clearAppBadge = vi.fn(); vi.stubGlobal('navigator', { clearAppBadge, setAppBadge: vi.fn() });
  vi.mocked(useNotifyData).mockReturnValue(null);
  renderHook(() => useAppBadge());
  expect(clearAppBadge).not.toHaveBeenCalled();
});

it('handles unsupported browsers and rejected OS updates', async () => {
  vi.stubGlobal('navigator', {}); await updateAppBadge(1);
  vi.stubGlobal('navigator', { setAppBadge: vi.fn().mockRejectedValue(new Error('denied')) });
  await expect(updateAppBadge(1)).resolves.toBeUndefined();
});
