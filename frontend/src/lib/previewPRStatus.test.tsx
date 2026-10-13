// @vitest-environment jsdom
import { act, cleanup, render, screen } from '@testing-library/react';
import { afterEach, expect, it, vi } from 'vitest';
import { ProviderPreview } from '../components/ProviderLinkPreview';
import { clearPRChecksCache } from './prChecksCache';

afterEach(() => { cleanup(); clearPRChecksCache(); vi.useRealTimers(); vi.unstubAllGlobals(); });

it.each(['denied', 'not_found', 'connect', 'expired'])('stops polling terminal %s status until refresh', async (state) => {
  vi.useFakeTimers();
  const fetch = vi.fn().mockResolvedValue({ ok: true, json: async () => ({ previews: [{ state }] }) });
  vi.stubGlobal('fetch', fetch);
  render(<ProviderPreview providers={[]} preview={{ provider: 'github', kind: 'pr', id: 'o/r#42', url: 'https://github.com/o/r/pull/42', title: 'PR', state: 'ok', status: 'Open' }} />);
  await act(async () => {});
  await act(async () => vi.advanceTimersByTimeAsync(120_000));
  expect(fetch).toHaveBeenCalledOnce();
  await act(async () => clearPRChecksCache());
  expect(fetch).toHaveBeenCalledTimes(2);
  expect(screen.queryByText('Approved')).toBeNull();
});

it.each([{ state: 'error' }, { state: 'rate_limited' }, { state: 'ok', stale: true, prStatus: { mergeable: true, approved: true } }])('retries transient status $state instead of settling stale data', async (preview) => {
  vi.useFakeTimers();
  const fetch = vi.fn().mockResolvedValueOnce({ ok: true, json: async () => ({ previews: [preview] }) })
    .mockResolvedValue({ ok: true, json: async () => ({ previews: [{ state: 'ok', prStatus: { mergeable: true, approved: true } }] }) });
  vi.stubGlobal('fetch', fetch);
  render(<ProviderPreview providers={[]} preview={{ provider: 'github', kind: 'pr', id: 'o/r#42', url: 'https://github.com/o/r/pull/42', title: 'PR', state: 'ok', status: 'Open' }} />);
  await act(async () => {});
  expect(screen.queryByText('Approved')).toBeNull();
  await act(async () => vi.advanceTimersByTimeAsync(15_000));
  expect(screen.getByText('Approved')).toBeInTheDocument();
  await act(async () => vi.advanceTimersByTimeAsync(120_000));
  expect(fetch).toHaveBeenCalledTimes(2);
});
