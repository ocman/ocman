// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { act, renderHook } from '@testing-library/react';
import { useRef } from 'react';
import { useComposerDrafts } from './useComposerDrafts';
import { getDraft, saveDraft } from '../../lib/composerDraft';

function setup(sessionId: string | undefined, el: HTMLTextAreaElement) {
  return renderHook(
    ({ sid }: { sid: string | undefined }) => {
      const inputRef = useRef<HTMLTextAreaElement | null>(el);
      const sessionIdRef = useRef<string | undefined>(sid);
      sessionIdRef.current = sid;
      return useComposerDrafts(inputRef, sid, sessionIdRef);
    },
    { initialProps: { sid: sessionId } },
  );
}

describe('useComposerDrafts', () => {
  let el: HTMLTextAreaElement;

  beforeEach(() => {
    vi.useFakeTimers();
    // jsdom's localStorage is only partially implemented in this setup;
    // plant a full in-memory stub so getDraft/saveDraft work.
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
    el = document.createElement('textarea');
    document.body.appendChild(el);
  });

  afterEach(() => {
    vi.runOnlyPendingTimers();
    vi.useRealTimers();
    el.remove();
  });

  it('loads the saved draft into the textarea on mount', () => {
    saveDraft('s1', 'hello world');
    setup('s1', el);
    expect(el.value).toBe('hello world');
  });

  it('reloads the draft when the session changes', () => {
    saveDraft('s1', 'draft one');
    saveDraft('s2', 'draft two');
    const { rerender } = setup('s1', el);
    expect(el.value).toBe('draft one');
    act(() => rerender({ sid: 's2' }));
    expect(el.value).toBe('draft two');
  });

  it('debounces autosave and persists non-empty text', () => {
    const { result } = setup('s1', el);
    act(() => result.current.scheduleDraftSave('s1', () => 'typed text'));
    // Not saved before the debounce window elapses.
    expect(getDraft('s1')).toBe('');
    act(() => vi.advanceTimersByTime(300));
    expect(getDraft('s1')).toBe('typed text');
  });

  it('a later scheduleDraftSave cancels the earlier pending one', () => {
    const { result } = setup('s1', el);
    act(() => result.current.scheduleDraftSave('s1', () => 'first'));
    act(() => vi.advanceTimersByTime(150));
    act(() => result.current.scheduleDraftSave('s1', () => 'second'));
    act(() => vi.advanceTimersByTime(300));
    expect(getDraft('s1')).toBe('second');
  });

  it('clearDraftNow removes the draft and cancels pending saves', () => {
    saveDraft('s1', 'existing');
    const { result } = setup('s1', el);
    act(() => result.current.scheduleDraftSave('s1', () => 'pending'));
    act(() => result.current.clearDraftNow('s1'));
    act(() => vi.advanceTimersByTime(300));
    expect(getDraft('s1')).toBe('');
  });

  it('flushes the current textarea text to storage on unmount', () => {
    const { unmount } = setup('s1', el);
    el.value = 'unsaved on unmount';
    act(() => unmount());
    expect(getDraft('s1')).toBe('unsaved on unmount');
  });

  it('clears the stored draft on unmount when the textarea is empty', () => {
    saveDraft('s1', 'stale');
    const { unmount } = setup('s1', el);
    el.value = '   ';
    act(() => unmount());
    expect(getDraft('s1')).toBe('');
  });
});
