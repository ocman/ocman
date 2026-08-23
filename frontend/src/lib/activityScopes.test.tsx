// @vitest-environment jsdom
import { act, renderHook } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import {
  __resetActivityScopesForTests,
  acquireActivityScope,
  activityScopeSnapshot,
  subscribeActivityScopes,
  useActivityScope,
} from './activityScopes';

describe('activity scope registry', () => {
  beforeEach(__resetActivityScopesForTests);
  afterEach(__resetActivityScopesForTests);

  it('ref-counts scopes, sorts snapshots, and makes releases idempotent', () => {
    const listener = vi.fn();
    const unsubscribe = subscribeActivityScopes(listener);
    const releaseB = acquireActivityScope('sessions');
    const releaseA1 = acquireActivityScope('projects');
    const releaseA2 = acquireActivityScope('projects');

    expect(activityScopeSnapshot()).toEqual(['projects', 'sessions']);
    expect(listener).toHaveBeenCalledTimes(2);

    releaseA1();
    releaseA1();
    expect(activityScopeSnapshot()).toEqual(['projects', 'sessions']);
    expect(listener).toHaveBeenCalledTimes(2);

    releaseA2();
    expect(activityScopeSnapshot()).toEqual(['sessions']);
    expect(listener).toHaveBeenCalledTimes(3);

    releaseB();
    unsubscribe();
    expect(activityScopeSnapshot()).toEqual([]);
  });

  it('ignores empty scopes', () => {
    const release = acquireActivityScope('');
    expect(activityScopeSnapshot()).toEqual([]);
    release();
  });

  it('moves a live hook subscription when its scope changes and releases on unmount', () => {
    const { rerender, unmount } = renderHook(
      ({ scope }) => useActivityScope(scope),
      { initialProps: { scope: 'session:one' as string | undefined } },
    );

    expect(activityScopeSnapshot()).toEqual(['session:one']);
    act(() => rerender({ scope: 'session:two' }));
    expect(activityScopeSnapshot()).toEqual(['session:two']);
    act(() => rerender({ scope: undefined }));
    expect(activityScopeSnapshot()).toEqual([]);

    unmount();
    expect(activityScopeSnapshot()).toEqual([]);
  });
});
