// @vitest-environment jsdom
import { beforeEach, describe, expect, it } from 'vitest';
import { cachePRChecks, clearPRChecksCache, getCachedPRChecks, resetPRChecksMemoryForTest } from './prChecksCache';

const ok = { state: 'success' as const, checks: [] };

describe('prChecksCache', () => {
  beforeEach(() => {
    localStorage.clear();
    clearPRChecksCache();
  });

  it('stores only final states and persists them across reloads', () => {
    cachePRChecks('a', ok);
    cachePRChecks('b', { state: 'pending', checks: [] });
    cachePRChecks('c', { state: 'unknown', checks: [] });
    cachePRChecks('d', { state: 'failure', checks: [], rateLimit: { limited: false } });
    resetPRChecksMemoryForTest();
    expect(getCachedPRChecks('a')).toEqual(ok);
    expect(getCachedPRChecks('b')).toBeUndefined();
    expect(getCachedPRChecks('c')).toBeUndefined();
    expect(getCachedPRChecks('d')).toEqual({ state: 'failure', checks: [] });
  });

  it('keeps the newest 1000 SHAs', () => {
    for (let i = 0; i < 1001; i++) cachePRChecks(`sha${i}`, ok);
    resetPRChecksMemoryForTest();
    expect(getCachedPRChecks('sha0')).toBeUndefined();
    expect(getCachedPRChecks('sha1')).toEqual(ok);
    expect(getCachedPRChecks('sha1000')).toEqual(ok);
  });

  it('clear drops memory and storage', () => {
    cachePRChecks('a', ok);
    clearPRChecksCache();
    expect(getCachedPRChecks('a')).toBeUndefined();
    resetPRChecksMemoryForTest();
    expect(getCachedPRChecks('a')).toBeUndefined();
  });

  it('survives corrupt storage', () => {
    localStorage.setItem('ocman.prChecks.v1', '{not json');
    resetPRChecksMemoryForTest();
    expect(getCachedPRChecks('a')).toBeUndefined();
    cachePRChecks('a', ok);
    expect(getCachedPRChecks('a')).toEqual(ok);
  });
});
