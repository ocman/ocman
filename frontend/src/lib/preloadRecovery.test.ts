// @vitest-environment jsdom
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { installPreloadRecovery } from './preloadRecovery';
import { getDraft, resetDraftTextsForTests, saveDraft } from './composerDraft';
import { rememberPendingDraftPayload, resetDraftPayloadsForTests } from './pendingDraftPayloads';

const a = 'a'.repeat(64), b = 'b'.repeat(64), c = 'c'.repeat(64);
let stop: (() => void) | undefined;
let reload: ReturnType<typeof vi.fn<() => void>>;
const fail = () => window.dispatchEvent(new Event('vite:preloadError', { cancelable: true }));
const settle = () => new Promise(resolve => setTimeout(resolve, 20));
function boot(hash: string) {
  stop?.();
  stop = installPreloadRecovery(hash, reload);
}
function deploy(hash: string) {
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue({ ok: true, json: async () => ({ hash }) }));
}
beforeEach(() => { sessionStorage.clear(); resetDraftTextsForTests(); resetDraftPayloadsForTests(); reload = vi.fn(); deploy(b); });
afterEach(() => { stop?.(); vi.unstubAllGlobals(); vi.restoreAllMocks(); vi.useRealTimers(); });

it('recovers again after a second deployment in the same tab, including the legacy marker', async () => {
  sessionStorage.setItem('ocman:preload-reloaded', '1');
  boot(a); fail(); await settle();
  expect(reload).toHaveBeenCalledTimes(1);
  boot(b); deploy(c); fail(); await settle();
  expect(reload).toHaveBeenCalledTimes(2);
});

it('does not reload for a broken chunk in the current build', async () => {
  boot(b); fail(); await settle();
  expect(reload).not.toHaveBeenCalled();
});

it('keeps composer text rather than reloading while it may still be saving', async () => {
  saveDraft('draft', 'keep this prompt');
  boot(a); fail(); await settle();
  expect(reload).not.toHaveBeenCalled();
  expect(getDraft('draft')).toBe('keep this prompt');
});

it('keeps browser-only file attachments', async () => {
  rememberPendingDraftPayload('draft', [], [new File(['content'], 'notes.txt')]);
  boot(a); fail(); await settle();
  expect(reload).not.toHaveBeenCalled();
});

it('recovers across deployments without a legacy marker', async () => {
  boot(a); fail(); await settle();
  boot(b); deploy(c); fail(); await settle();
  expect(reload).toHaveBeenCalledTimes(2);
  expect(fetch).toHaveBeenCalledWith('/build-hash.json', expect.objectContaining({ cache: 'no-store' }));
});

it('does not loop if the reload serves the old HTML again', async () => {
  boot(a); fail(); fail(); await settle();
  expect(reload).toHaveBeenCalledTimes(1);
  boot(a); fail(); await settle();
  expect(reload).toHaveBeenCalledTimes(1);
});

it('does not retry the same target or oscillate between deployments', async () => {
  boot(a); fail(); await settle();
  boot(c); fail(); await settle();
  expect(reload).toHaveBeenCalledTimes(1);
  boot(b); deploy(a); fail(); await settle();
  boot(a); deploy(b); fail(); await settle();
  expect(reload).toHaveBeenCalledTimes(2);
});

it('caps rapid distinct deployments but permits later new builds', async () => {
  let now = 100_000;
  vi.spyOn(Date, 'now').mockImplementation(() => now);
  boot(a); fail(); await settle();
  boot(b); deploy(c); fail(); await settle();
  boot(c); deploy('d'.repeat(64)); fail(); await settle();
  expect(reload).toHaveBeenCalledTimes(2);
  now += 60_000;
  fail(); await settle();
  expect(reload).toHaveBeenCalledTimes(3);
});

it.each(['not json', '{}', '[{}]', JSON.stringify(Array(9).fill({ from: a, to: b, at: 1 }))])(
  'fails closed with a malformed or oversized persisted guard: %s', async value => {
    sessionStorage.setItem('ocman:preload-recovery:v1', value);
    boot(a); fail(); await settle();
    expect(reload).not.toHaveBeenCalled();
  },
);

it.each(['getItem', 'setItem'] as const)('fails closed when storage %s throws', async method => {
  vi.spyOn(Storage.prototype, method).mockImplementation(() => { throw new Error('storage disabled'); });
  boot(a); fail(); await settle();
  expect(reload).not.toHaveBeenCalled();
});

it.each([
  { ok: false },
  { ok: true, json: async () => ({ hash: 'bad' }) },
  { ok: true, json: async () => { throw new Error('HTML fallback'); } },
])('does not reload when the deployment probe is unavailable or invalid', async response => {
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(response));
  boot(a); fail(); await settle();
  expect(reload).not.toHaveBeenCalled();
});

it('leaves failed imports visible to ErrorBoundary, including when offline', async () => {
  vi.stubGlobal('fetch', vi.fn().mockRejectedValue(new Error('offline')));
  boot(a);
  expect(fail()).toBe(true);
  await settle();
  expect(reload).not.toHaveBeenCalled();
});

it('does not probe without a production build identity or after disposal', async () => {
  boot(''); fail(); await settle();
  expect(fetch).not.toHaveBeenCalled();
  boot(a); stop?.(); fail(); await settle();
  expect(fetch).not.toHaveBeenCalled();
});

it('rechecks draft safety after the asynchronous probe', async () => {
  let respond!: (response: unknown) => void;
  vi.stubGlobal('fetch', vi.fn(() => new Promise(resolve => { respond = resolve; })));
  boot(a); fail();
  saveDraft('new-edit', 'typed during request');
  respond({ ok: true, json: async () => ({ hash: b }) });
  await settle();
  expect(reload).not.toHaveBeenCalled();
  expect(getDraft('new-edit')).toBe('typed during request');
});

it('aborts the probe on disposal without reloading', async () => {
  let respond!: (response: unknown) => void;
  vi.stubGlobal('fetch', vi.fn((_url, options: RequestInit) => new Promise(resolve => {
    expect(options.signal?.aborted).toBe(false);
    respond = resolve;
  })));
  boot(a); fail(); stop?.();
  expect(vi.mocked(fetch).mock.calls[0][1]?.signal?.aborted).toBe(true);
  respond({ ok: true, json: async () => ({ hash: b }) });
  await settle();
  expect(reload).not.toHaveBeenCalled();
});

it('bounds a stalled probe and allows a subsequent attempt', async () => {
  vi.useFakeTimers();
  vi.stubGlobal('fetch', vi.fn((_url, options: RequestInit) => new Promise((_resolve, reject) => {
    options.signal?.addEventListener('abort', () => reject(new Error('aborted')));
  })));
  boot(a); fail(); fail();
  expect(fetch).toHaveBeenCalledTimes(1);
  await vi.advanceTimersByTimeAsync(10_000);
  expect(reload).not.toHaveBeenCalled();
  deploy(b); fail();
  await vi.advanceTimersByTimeAsync(0);
  expect(reload).toHaveBeenCalledTimes(1);
});
