// @vitest-environment jsdom
import { createElement, type ReactNode } from 'react';
import { act, cleanup, renderHook, waitFor } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { afterEach, expect, it, vi } from 'vitest';
import { fetchJSON, postJSON } from './api.requests';
import { useBadgeSettings, useSaveBadgeSettings } from './badgeSettings';

vi.mock('./api.requests', () => ({ fetchJSON: vi.fn(), postJSON: vi.fn() }));
afterEach(() => { cleanup(); vi.resetAllMocks(); });

it('loads shared settings and updates every consumer after saving', async () => {
  const initial = { sessions: true, primary: true, factory: true, routines: false };
  const next = { ...initial, sessions: false, routines: true };
  vi.mocked(fetchJSON).mockResolvedValue(initial);
  vi.mocked(postJSON).mockResolvedValue(next);
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const wrapper = ({ children }: { children: ReactNode }) => createElement(QueryClientProvider, { client }, children);
  const hook = renderHook(() => ({ settings: useBadgeSettings(), save: useSaveBadgeSettings() }), { wrapper });
  await waitFor(() => expect(hook.result.current.settings.data).toEqual(initial));
  expect(fetchJSON).toHaveBeenCalledWith('/api/settings/notification-badge', expect.any(AbortSignal));
  await act(async () => { await hook.result.current.save.mutateAsync(next); });
  expect(postJSON).toHaveBeenCalledWith('/api/settings/notification-badge', next);
  await waitFor(() => expect(hook.result.current.settings.data).toEqual(next));
  vi.mocked(postJSON).mockRejectedValueOnce(new Error('Save failed'));
  await act(async () => { await expect(hook.result.current.save.mutateAsync(initial)).rejects.toThrow('Save failed'); });
  expect(hook.result.current.settings.data).toEqual(next);
  hook.unmount();
  client.clear();
});
