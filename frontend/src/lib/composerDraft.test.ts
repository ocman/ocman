// @vitest-environment jsdom
import { describe, it, expect, beforeEach } from 'vitest';
import { renderHook, act } from '@testing-library/react';
import { saveDraft, clearDraft, getDraft, useDraftSessionIds } from './composerDraft';

describe('composerDraft', () => {
  beforeEach(() => {
    // jsdom's localStorage is only partially implemented here; plant a
    // full in-memory stub (same trick as useComposerDrafts.test.ts).
    const data = new Map<string, string>();
    Object.defineProperty(window, 'localStorage', {
      configurable: true,
      value: {
        getItem: (k: string) => (data.has(k) ? data.get(k)! : null),
        setItem: (k: string, v: string) => { data.set(k, String(v)); },
        removeItem: (k: string) => { data.delete(k); },
        clear: () => { data.clear(); },
      },
    });
    clearDraft('__reset__'); // resync the module snapshot after the swap
  });

  it('stores and clears drafts', () => {
    saveDraft('s1', 'hello');
    expect(getDraft('s1')).toBe('hello');
    saveDraft('s1', '');
    expect(getDraft('s1')).toBe('');
  });

  it('tracks which sessions hold a draft', () => {
    const { result } = renderHook(() => useDraftSessionIds());
    expect(result.current.has('s1')).toBe(false);

    act(() => saveDraft('s1', 'draft text'));
    expect(result.current.has('s1')).toBe(true);

    act(() => clearDraft('s1'));
    expect(result.current.has('s1')).toBe(false);
  });
});
