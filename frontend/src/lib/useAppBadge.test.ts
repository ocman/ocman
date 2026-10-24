// @vitest-environment jsdom
import { cleanup, renderHook, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { attentionSessionCount, updateAppBadge, useAppBadge } from './useAppBadge';
import { useNotifyData } from './useNotifyData';
import { useInbox } from './queries';
import { useBadgeSettings } from './badgeSettings';

vi.mock('./useNotifyData', () => {
  const useNotifyData = vi.fn();
  return { useNotifyData, useNotifySnapshot: () => {
    const entries = useNotifyData();
    return entries ? { entries, fetchedAt: 100 } : undefined;
  } };
});
vi.mock('./queries', () => ({ useInbox: vi.fn() }));
vi.mock('./badgeSettings', () => ({ useBadgeSettings: vi.fn() }));
beforeEach(() => {
  vi.mocked(useInbox).mockReturnValue({ data: { items: [], unreadTotal: 0 } } as unknown as ReturnType<typeof useInbox>);
  vi.mocked(useBadgeSettings).mockReturnValue({ data: { sessions: true, primary: true, factory: true, routines: false } } as ReturnType<typeof useBadgeSettings>);
});
afterEach(() => { cleanup(); vi.unstubAllGlobals(); vi.resetAllMocks(); });

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

it('combines selected unread Inbox categories with sessions, excluding permission items', async () => {
  const setAppBadge = vi.fn().mockResolvedValue(undefined);
  vi.stubGlobal('navigator', { setAppBadge });
  vi.mocked(useNotifyData).mockReturnValue([{ id: 'session', status: 'busy', seen: true, pendingQuestion: true }]);
  vi.mocked(useInbox).mockReturnValue({ data: { items: [
    { id: 'same', remoteId: 'local', category: 'general' }, { id: 'same', remoteId: 'remote' },
    { id: 'factory', category: 'factory' }, { id: 'routine', category: 'routine' },
    { id: 'permission', category: 'permission' }, { id: 'legacy-permission', permission: {} },
    { id: 'read', readAt: 1 }, { id: 'archived', archivedAt: 1 },
  ], unreadTotal: 6 } } as ReturnType<typeof useInbox>);
  const hook = renderHook(() => useAppBadge());
  await waitFor(() => expect(setAppBadge).toHaveBeenLastCalledWith(4));
  vi.mocked(useBadgeSettings).mockReturnValue({ data: { sessions: false, primary: false, factory: true, routines: true } } as ReturnType<typeof useBadgeSettings>);
  hook.rerender();
  await waitFor(() => expect(setAppBadge).toHaveBeenLastCalledWith(2));
  vi.mocked(useInbox).mockReturnValue({ data: { items: [], unreadTotal: 0 } } as unknown as ReturnType<typeof useInbox>);
  vi.stubGlobal('navigator', { setAppBadge, clearAppBadge: vi.fn().mockResolvedValue(undefined) });
  hook.rerender();
  await waitFor(() => expect(navigator.clearAppBadge).toHaveBeenCalledOnce());
});

it('preserves the current badge until settings and Inbox data are loaded', () => {
  const setAppBadge = vi.fn();
  vi.stubGlobal('navigator', { setAppBadge });
  vi.mocked(useNotifyData).mockReturnValue([]);
  vi.mocked(useInbox).mockReturnValue({ data: undefined } as ReturnType<typeof useInbox>);
  const hook = renderHook(() => useAppBadge());
  expect(setAppBadge).not.toHaveBeenCalled();
  vi.mocked(useInbox).mockReturnValue({ data: { items: [], unreadTotal: 0 } } as unknown as ReturnType<typeof useInbox>);
  vi.mocked(useBadgeSettings).mockReturnValue({ data: undefined } as ReturnType<typeof useBadgeSettings>);
  hook.rerender();
  expect(setAppBadge).not.toHaveBeenCalled();
});

it('clears the badge when all sources are disabled even if snapshots are unavailable', async () => {
  const clearAppBadge = vi.fn().mockResolvedValue(undefined);
  vi.stubGlobal('navigator', { setAppBadge: vi.fn(), clearAppBadge });
  vi.mocked(useNotifyData).mockReturnValue(null);
  vi.mocked(useInbox).mockReturnValue({ data: undefined } as ReturnType<typeof useInbox>);
  vi.mocked(useBadgeSettings).mockReturnValue({ data: { sessions: false, primary: false, factory: false, routines: false } } as ReturnType<typeof useBadgeSettings>);
  renderHook(() => useAppBadge());
  await waitFor(() => expect(clearAppBadge).toHaveBeenCalledOnce());
});
