// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { act, renderHook } from '@testing-library/react';
import { useLayoutEffect, useRef } from 'react';
import { useComposerDrafts } from './useComposerDrafts';
import { clearDraft, getDraft, resetDraftTextsForTests, saveDraft } from '../../lib/composerDraft';

function setup(sessionId: string | undefined, el: HTMLTextAreaElement) {
  return renderHook(
    ({ sid }: { sid: string | undefined }) => {
      const inputRef = useRef<HTMLTextAreaElement | null>(el);
      const inFlightRef = useRef<string | null>(null);
      return useComposerDrafts(inputRef, sid, inFlightRef);
    },
    { initialProps: { sid: sessionId } },
  );
}

describe('useComposerDrafts', () => {
  let el: HTMLTextAreaElement;

  beforeEach(() => {
    vi.useFakeTimers();
    resetDraftTextsForTests();
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

  it('restores the draft before an interactive editor can write new text', () => {
    saveDraft('s1', 'saved draft');
    const { unmount } = renderHook(() => {
      const inputRef = useRef<HTMLTextAreaElement | null>(el);
      const inFlightRef = useRef<string | null>(null);
      useComposerDrafts(inputRef, 's1', inFlightRef);
      useLayoutEffect(() => { el.value = 'new follow-up'; }, []);
    });
    expect(el.value).toBe('new follow-up');
    unmount();
    expect(getDraft('s1')).toBe('new follow-up');
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

  it('does not let a delayed empty autosave drop a newer edit from another composer', () => {
    saveDraft('shared', 'original');
    const peerEl = document.createElement('textarea');
    const clearer = setup('shared', el);
    const peer = setup('shared', peerEl);
    act(() => clearer.result.current.scheduleDraftSave('shared', () => ''));
    act(() => vi.advanceTimersByTime(100));
    peerEl.value = 'newer peer edit';
    act(() => peer.result.current.scheduleDraftSave('shared', () => peerEl.value));
    act(() => vi.advanceTimersByTime(300));
    expect(getDraft('shared')).toBe('newer peer edit');
    act(() => peer.unmount());
    expect(getDraft('shared')).toBe('newer peer edit');
  });

  it('shows text restored after mount in an untouched composer, but never over a user edit', () => {
    setup('recovered', el);
    // Another tab's failed start restores the prompt after this composer mounted.
    act(() => saveDraft('recovered', 'restored prompt'));
    expect(el.value).toBe('restored prompt');
    const editedEl = document.createElement('textarea');
    const edited = setup('edited', editedEl);
    editedEl.value = 'typing';
    act(() => edited.result.current.scheduleDraftSave('edited', () => editedEl.value));
    act(() => saveDraft('edited', 'restored elsewhere'));
    expect(editedEl.value).toBe('typing');
  });

  it('follows another tab\'s edits and its send while untouched, so a sent prompt cannot be sent again', () => {
    setup('peer', el);
    act(() => saveDraft('peer', 'hel'));
    expect(el.value).toBe('hel');
    act(() => saveDraft('peer', 'hello world'));
    expect(el.value).toBe('hello world');
    act(() => clearDraft('peer'));
    expect(el.value).toBe('');
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
