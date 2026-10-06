// @vitest-environment jsdom
import { beforeEach, describe, expect, it } from 'vitest';
import {
  cachePRChecks, clearPRChecksCache, getCachedPRChecks, isSettled, prChecksCacheKey, resetPRChecksMemoryForTest,
} from './prChecksCache';
import type { PRChecks } from './upstreamApi';

const ok: PRChecks = { state: 'success', checks: [{ name: 'build', state: 'success' }] };

describe('prChecksCache', () => {
  beforeEach(() => {
    localStorage.clear();
    clearPRChecksCache();
  });

  it.each<[string, PRChecks, boolean]>([
    ['all success', ok, true],
    ['all finished with a failure', { state: 'failure', checks: [{ name: 'a', state: 'success' }, { name: 'b', state: 'failure' }] }, true],
    ['failure while another check runs', { state: 'failure', checks: [{ name: 'a', state: 'failure' }, { name: 'b', state: 'pending' }] }, false],
    ['pending', { state: 'pending', checks: [{ name: 'a', state: 'pending' }] }, false],
    ['no checks yet', { state: 'unknown', checks: [] }, false],
    ['rate-limited partial success', { ...ok, rateLimit: { limited: true } }, false],
  ])('isSettled: %s', (_name, checks, want) => {
    expect(isSettled(checks)).toBe(want);
  });

  it('stores only settled results and persists them across reloads', () => {
    cachePRChecks('a', ok);
    cachePRChecks('b', { state: 'failure', checks: [{ name: 'x', state: 'failure' }, { name: 'y', state: 'pending' }] });
    cachePRChecks('c', { ...ok, rateLimit: { limited: false } });
    resetPRChecksMemoryForTest();
    expect(getCachedPRChecks('a')).toEqual(ok);
    expect(getCachedPRChecks('b')).toBeUndefined();
    expect(getCachedPRChecks('c')).toEqual(ok);
  });

  it('scopes keys by host and repository', () => {
    expect(prChecksCacheKey('github.com', 'o/r', 'abc')).not.toBe(prChecksCacheKey('code.example', 'o/r', 'abc'));
    expect(prChecksCacheKey('github.com', 'o/r', 'abc')).not.toBe(prChecksCacheKey('github.com', 'fork/r', 'abc'));
  });

  it('keeps the newest 1000 entries', () => {
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

  it('ignores old SHA-only and potentially incomplete storage and survives corrupt storage', () => {
    localStorage.setItem('ocman.prChecks.v1', JSON.stringify([['a', ok]]));
    localStorage.setItem('ocman.prChecks.v2', JSON.stringify([['a', ok]]));
    resetPRChecksMemoryForTest();
    expect(getCachedPRChecks('a')).toBeUndefined();
    localStorage.setItem('ocman.prChecks.v3', '{not json');
    resetPRChecksMemoryForTest();
    expect(getCachedPRChecks('a')).toBeUndefined();
    cachePRChecks('a', ok);
    expect(getCachedPRChecks('a')).toEqual(ok);
  });
});
