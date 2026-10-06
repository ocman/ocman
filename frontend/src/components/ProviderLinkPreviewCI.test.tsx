// @vitest-environment jsdom
import { act, render, screen, waitFor } from '@testing-library/react';
import { afterEach, expect, it, vi } from 'vitest';
import { ProviderPreview } from './ProviderLinkPreview';
import { CI_POLL_MS, cachePRChecks, clearPRChecksCache, getCachedPRChecks, prChecksCacheKey } from '../lib/prChecksCache';
import { PreviewOwnerContext } from '../lib/previews';

afterEach(() => { clearPRChecksCache(); vi.unstubAllGlobals(); vi.useRealTimers(); });

it('uses the sidebar SHA cache for a conversation PR without fetching checks', async () => {
  const fetch = vi.fn();
  vi.stubGlobal('fetch', fetch);
  cachePRChecks(prChecksCacheKey('github.com', 'o/r', 'abc123'), {
    state: 'success', checks: [{ name: 'build', state: 'success' }],
  });
  render(<ProviderPreview providers={[]} preview={{
    provider: 'github', kind: 'pr', id: 'o/r#1', url: 'https://github.com/o/r/pull/1',
    title: 'Cached PR', state: 'ok', headSha: 'abc123',
  }} />);
  await waitFor(() => expect(screen.getByLabelText('All checks passed')).toBeInTheDocument());
  expect(fetch).not.toHaveBeenCalled();
});

const preview = {
  provider: 'forgejo:code.example', kind: 'pr', id: 'o/r#1', url: 'https://code.example/o/r/pulls/1',
  title: 'PR', state: 'ok' as const, headSha: 'abc123',
};
const key = prChecksCacheKey('code.example', 'o/r', 'abc123');
const done = { state: 'success', checks: [{ name: 'build', state: 'success' }] };
const response = (checks: unknown, stale = false) => ({ ok: true, json: async () => ({ previews: [{ state: 'ok', stale, checks }] }) });

it('polls unfinished checks on the conversation owner and fills the sidebar cache once settled', async () => {
  vi.useFakeTimers();
  const fetch = vi.fn()
    .mockResolvedValueOnce(response({ state: 'failure', checks: [{ name: 'build', state: 'failure' }, { name: 'test', state: 'pending' }] }))
    .mockResolvedValue(response(done));
  vi.stubGlobal('fetch', fetch);
  render(<PreviewOwnerContext.Provider value="r1"><ProviderPreview providers={[]} preview={preview} /></PreviewOwnerContext.Provider>);
  await act(async () => { await vi.advanceTimersByTimeAsync(0); });
  expect(screen.getByLabelText('Some checks failed')).toBeInTheDocument();
  expect(getCachedPRChecks(key)).toBeUndefined();
  expect(fetch.mock.calls[0][0]).toBe('/api/previews/resolve?remoteId=r1');
  expect(JSON.parse(fetch.mock.calls[0][1].body)).toEqual({ text: preview.url, checksSha: 'abc123' });
  await act(async () => { await vi.advanceTimersByTimeAsync(CI_POLL_MS); });
  expect(screen.getByLabelText('All checks passed')).toBeInTheDocument();
  expect(getCachedPRChecks(key)).toEqual(done);
  await act(async () => { await vi.advanceTimersByTimeAsync(CI_POLL_MS); });
  expect(fetch).toHaveBeenCalledTimes(2);
});

it('does not settle rate-limited stale checks and retries errors', async () => {
  vi.useFakeTimers();
  const fetch = vi.fn().mockResolvedValueOnce(response(done, true)).mockRejectedValueOnce(new Error('offline')).mockResolvedValue(response(done));
  vi.stubGlobal('fetch', fetch);
  render(<ProviderPreview providers={[]} preview={preview} />);
  await act(async () => { await vi.advanceTimersByTimeAsync(0); });
  expect(getCachedPRChecks(key)).toBeUndefined();
  expect(screen.getByLabelText('Failed to load checks')).toBeInTheDocument();
  await act(async () => { await vi.advanceTimersByTimeAsync(CI_POLL_MS); });
  expect(getCachedPRChecks(key)).toBeUndefined();
  await act(async () => { await vi.advanceTimersByTimeAsync(CI_POLL_MS); });
  expect(getCachedPRChecks(key)).toEqual(done);
});

it('fetches only while the preview is visible and aborts when it leaves the viewport', async () => {
  let onVisible: (entries: { isIntersecting: boolean }[]) => void = () => {};
  const disconnect = vi.fn();
  vi.stubGlobal('IntersectionObserver', class {
    constructor(callback: typeof onVisible) { onVisible = callback; }
    observe() {}
    disconnect = disconnect;
  });
  const fetch = vi.fn().mockResolvedValue(response({ state: 'pending', checks: [] }));
  vi.stubGlobal('fetch', fetch);
  const { unmount } = render(<ProviderPreview providers={[]} preview={preview} />);
  expect(fetch).not.toHaveBeenCalled();
  await act(async () => onVisible([{ isIntersecting: true }]));
  expect(fetch).toHaveBeenCalledTimes(1);
  await act(async () => onVisible([{ isIntersecting: false }]));
  expect(fetch.mock.calls[0][1].signal.aborted).toBe(true);
  unmount();
  expect(disconnect).toHaveBeenCalled();
});

it('never reads checks for inaccessible cards or issues', () => {
  const fetch = vi.fn();
  vi.stubGlobal('fetch', fetch);
  const { rerender } = render(<ProviderPreview providers={[]} preview={{ ...preview, state: 'denied' }} />);
  expect(screen.queryByLabelText('No CI status')).toBeNull();
  rerender(<ProviderPreview providers={[]} preview={{ ...preview, kind: 'issue' }} />);
  expect(screen.queryByLabelText('No CI status')).toBeNull();
  expect(fetch).not.toHaveBeenCalled();
});

it('refreshes a mounted conversation card when the sidebar clears the cache', async () => {
  cachePRChecks(key, { state: 'success', checks: [{ name: 'build', state: 'success' }] });
  const fetch = vi.fn().mockResolvedValue(response({ state: 'failure', checks: [{ name: 'build', state: 'failure' }] }));
  vi.stubGlobal('fetch', fetch);
  render(<ProviderPreview providers={[]} preview={preview} />);
  await screen.findByLabelText('All checks passed');
  expect(fetch).not.toHaveBeenCalled();
  await act(async () => clearPRChecksCache());
  expect(await screen.findByLabelText('Some checks failed')).toBeInTheDocument();
  expect(fetch).toHaveBeenCalledTimes(1);
  expect(JSON.parse(fetch.mock.calls[0][1].body).refreshChecks).toBe(true);
});

it('shows loading and a failed refresh instead of retained success', async () => {
  let reject: (error: Error) => void = () => {};
  const fetch = vi.fn().mockImplementation(() => new Promise((_, fail) => { reject = fail; }));
  vi.stubGlobal('fetch', fetch);
  const { unmount } = render(<ProviderPreview providers={[]} preview={preview} />);
  expect(await screen.findByLabelText('Loading checks…')).toBeInTheDocument();
  await act(async () => reject(new Error('offline')));
  expect(screen.getByLabelText('Failed to load checks')).toBeInTheDocument();
  unmount();
  cachePRChecks(key, { state: 'success', checks: [{ name: 'build', state: 'success' }] });
  render(<ProviderPreview providers={[]} preview={preview} />);
  await screen.findByLabelText('All checks passed');
  await act(async () => clearPRChecksCache());
  await act(async () => reject(new Error('offline')));
  expect(screen.getByLabelText('Failed to load checks')).toBeInTheDocument();
  expect(screen.queryByLabelText('All checks passed')).toBeNull();
});

it('sends the server refresh flag only on the first fetch of a refresh cycle', async () => {
  vi.useFakeTimers();
  cachePRChecks(key, { state: 'success', checks: [{ name: 'build', state: 'success' }] });
  const fetch = vi.fn().mockResolvedValue(response({ state: 'pending', checks: [{ name: 'build', state: 'pending' }] }));
  vi.stubGlobal('fetch', fetch);
  render(<ProviderPreview providers={[]} preview={preview} />);
  await act(async () => clearPRChecksCache());
  expect(JSON.parse(fetch.mock.calls[0][1].body).refreshChecks).toBe(true);
  await act(async () => { await vi.advanceTimersByTimeAsync(CI_POLL_MS); });
  expect(JSON.parse(fetch.mock.calls[1][1].body).refreshChecks).toBeUndefined();
});

it('renders nullable empty provider checks as normal unknown CI', async () => {
  vi.useFakeTimers();
  const fetch = vi.fn().mockResolvedValue(response({ state: 'unknown', checks: null }));
  vi.stubGlobal('fetch', fetch);
  render(<ProviderPreview providers={[]} preview={preview} />);
  await act(async () => { await vi.advanceTimersByTimeAsync(0); });
  expect(screen.getByLabelText('No CI status')).toBeInTheDocument();
  expect(screen.queryByLabelText('Failed to load checks')).toBeNull();
});

it('reuses settled results after refresh across visibility and request-key changes', async () => {
  let onVisible: (entries: { isIntersecting: boolean }[]) => void = () => {};
  vi.stubGlobal('IntersectionObserver', class {
    constructor(callback: typeof onVisible) { onVisible = callback; }
    observe() {}
    disconnect() {}
  });
  const settled = { state: 'success' as const, checks: [{ name: 'build', state: 'success' as const }] };
  cachePRChecks(key, settled);
  const fetch = vi.fn().mockResolvedValue(response(settled));
  vi.stubGlobal('fetch', fetch);
  const { rerender } = render(<ProviderPreview providers={[]} preview={preview} />);
  await act(async () => onVisible([{ isIntersecting: true }]));
  expect(fetch).not.toHaveBeenCalled();
  await act(async () => clearPRChecksCache());
  expect(fetch).toHaveBeenCalledTimes(1);
  await act(async () => onVisible([{ isIntersecting: false }]));
  await act(async () => onVisible([{ isIntersecting: true }]));
  expect(fetch).toHaveBeenCalledTimes(1);
  cachePRChecks(prChecksCacheKey('code.example', 'o/r', 'def456'), settled);
  rerender(<ProviderPreview providers={[]} preview={{ ...preview, headSha: 'def456' }} />);
  await screen.findByLabelText('All checks passed');
  expect(fetch).toHaveBeenCalledTimes(1);
});

it('holds an offscreen refresh until visible and consumes it even when the request aborts', async () => {
  let onVisible: (entries: { isIntersecting: boolean }[]) => void = () => {};
  vi.stubGlobal('IntersectionObserver', class {
    constructor(callback: typeof onVisible) { onVisible = callback; }
    observe() {}
    disconnect() {}
  });
  const fetch = vi.fn().mockImplementationOnce(() => new Promise(() => {})).mockResolvedValue(response(done));
  vi.stubGlobal('fetch', fetch);
  render(<ProviderPreview providers={[]} preview={preview} />);
  await act(async () => clearPRChecksCache());
  expect(fetch).not.toHaveBeenCalled();
  await act(async () => onVisible([{ isIntersecting: true }]));
  expect(JSON.parse(fetch.mock.calls[0][1].body).refreshChecks).toBe(true);
  await act(async () => onVisible([{ isIntersecting: false }]));
  expect(fetch.mock.calls[0][1].signal.aborted).toBe(true);
  await act(async () => onVisible([{ isIntersecting: true }]));
  expect(JSON.parse(fetch.mock.calls[1][1].body).refreshChecks).toBeUndefined();
  expect(screen.getByLabelText('All checks passed')).toBeInTheDocument();
});
