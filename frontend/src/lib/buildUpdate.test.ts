// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { BUILD_CHECK_MS, deployedBuildHash, watchBuildUpdate } from './buildUpdate';

const hash = 'a'.repeat(64);
const next = 'b'.repeat(64);
beforeEach(() => vi.useFakeTimers());
afterEach(() => { vi.useRealTimers(); vi.unstubAllGlobals(); vi.restoreAllMocks(); });

describe('deployment build check', () => {
  it('compares with the loaded build, polls every three minutes and notifies once', async () => {
    const fetch = vi.fn().mockImplementation(() => Promise.resolve(new Response(JSON.stringify({ hash }))));
    vi.stubGlobal('fetch', fetch);
    const changed = vi.fn();
    const stop = watchBuildUpdate(hash, changed);
    await vi.advanceTimersByTimeAsync(0);
    expect(changed).not.toHaveBeenCalled();
    fetch.mockImplementation(() => Promise.resolve(new Response(JSON.stringify({ hash: next }))));
    await vi.advanceTimersByTimeAsync(BUILD_CHECK_MS);
    expect(changed).toHaveBeenCalledTimes(1);
    await vi.advanceTimersByTimeAsync(BUILD_CHECK_MS);
    expect(changed).toHaveBeenCalledTimes(1);
    expect(fetch.mock.calls[0][1]).toMatchObject({ cache: 'no-store' });
    stop();
  });

  it.each(['<html>SPA fallback</html>', '{}', '{"hash":""}', '{"hash":123}'])('ignores invalid deployment probes: %s', async body => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(body)));
    expect(await deployedBuildHash(new AbortController().signal)).toBeNull();
  });

  it('ignores errors and checks on returning from a hidden tab', async () => {
    const fetch = vi.fn().mockRejectedValueOnce(new Error('offline')).mockResolvedValue(new Response(JSON.stringify({ hash: next })));
    vi.stubGlobal('fetch', fetch);
    const changed = vi.fn();
    const stop = watchBuildUpdate(hash, changed);
    await vi.advanceTimersByTimeAsync(0);
    vi.spyOn(document, 'hidden', 'get').mockReturnValue(true);
    await vi.advanceTimersByTimeAsync(BUILD_CHECK_MS);
    expect(fetch).toHaveBeenCalledTimes(1);
    vi.spyOn(document, 'hidden', 'get').mockReturnValue(false);
    document.dispatchEvent(new Event('visibilitychange'));
    await vi.advanceTimersByTimeAsync(0);
    expect(changed).toHaveBeenCalledOnce();
    stop();
  });
});
