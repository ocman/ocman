// @vitest-environment jsdom
import { describe, it, expect, beforeEach, vi } from 'vitest';
import { renderHook, act } from '@testing-library/react';
import { saveDraft, clearDraft, discardDraft, getDraft, getDraftEntryId, useDraftSessionIds } from './composerDraft';

describe('composerDraft', () => {
  beforeEach(() => {
    // jsdom's localStorage is only partially implemented here; plant a
    // full in-memory stub (same trick as useComposerDrafts.test.ts).
    const data = new Map<string, string>();
    Object.defineProperty(window, 'localStorage', {
      configurable: true,
      value: {
        get length() { return data.size; },
        key: (index: number) => [...data.keys()][index] ?? null,
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

  it('does not let a stale clear reverse a newer explicit discard', () => {
    saveDraft('clear-order', 'old text');
    const older = getDraftEntryId('clear-order');
    saveDraft('clear-order', 'new discarded text');
    discardDraft('clear-order');
    clearDraft('clear-order', older);
    expect(getDraft('clear-order')).toBe('');
  });

  it('reclaims actual prompt bytes when a fresh draft is discarded', () => {
    saveDraft('reclaimed', 'large unique prompt payload');
    discardDraft('reclaimed');
    expect(getDraft('reclaimed')).toBe('');
    for (let i = 0; i < localStorage.length; i++) {
      expect(localStorage.getItem(localStorage.key(i)!)).not.toContain('large unique prompt payload');
    }
  });

  it('preserves the previous head and reclaims an unpublished body when head publication fails', () => {
    saveDraft('failed-head', 'retained previous text');
    const original = localStorage.setItem;
    const write = vi.spyOn(localStorage, 'setItem').mockImplementation((key, value) => {
      if (key === 'ocman.composerDraftHead.v1:failed-head') throw new Error('head quota');
      original(key, value);
    });
    try {
      saveDraft('failed-head', 'unpublished replacement text');
      expect(getDraft('failed-head')).toBe('retained previous text');
      for (let i = 0; i < localStorage.length; i++) expect(localStorage.getItem(localStorage.key(i)!)).not.toContain('unpublished replacement text');
    } finally { write.mockRestore(); }
  });

  it('keeps an edit cleared if physical body reclamation is temporarily blocked', () => {
    saveDraft('blocked-delete', 'logically cleared text');
    const original = localStorage.removeItem;
    const remove = vi.spyOn(localStorage, 'removeItem').mockImplementation((key) => {
      if (key.startsWith('ocman.composerDraftText.v1:blocked-delete:')) throw new Error('delete blocked');
      original(key);
    });
    try { clearDraft('blocked-delete'); expect(getDraft('blocked-delete')).toBe(''); }
    finally { remove.mockRestore(); }
    clearDraft('blocked-delete');
    for (let i = 0; i < localStorage.length; i++) expect(localStorage.getItem(localStorage.key(i)!)).not.toContain('logically cleared text');
  });

  it('reads and reclaims an older inline record without deleting a newer immutable body', () => {
    localStorage.setItem('ocman.composerDrafts.v1:inline', JSON.stringify({ kind: 'ocman/composer-text', id: 'old-inline', text: 'old inline payload' }));
    expect(getDraft('inline')).toBe('old inline payload');
    const old = getDraftEntryId('inline');
    saveDraft('inline', 'new immutable payload');
    clearDraft('inline', old);
    expect(getDraft('inline')).toBe('new immutable payload');
    clearDraft('inline');
    for (let i = 0; i < localStorage.length; i++) {
      expect(localStorage.getItem(localStorage.key(i)!)).not.toContain('old inline payload');
      expect(localStorage.getItem(localStorage.key(i)!)).not.toContain('new immutable payload');
    }
  });

  it('reads legacy text and reclaims only explicitly cleared legacy entries', () => {
    const legacy = JSON.stringify({ legacy: 'old text', other: 'untouched' });
    localStorage.setItem('ocman.composerDrafts.v1', legacy);
    expect(getDraft('legacy')).toBe('old text');
    clearDraft('legacy');
    expect(getDraft('legacy')).toBe('');
    saveDraft('new', 'independent text');
    expect(JSON.parse(localStorage.getItem('ocman.composerDrafts.v1')!)).toEqual({ other: 'untouched' });
    expect(getDraft('other')).toBe('untouched');
  });

  it.each(['null', 'invalid'])('ignores malformed legacy text maps: %s', (raw) => {
    localStorage.setItem('ocman.composerDrafts.v1', raw);
    expect(getDraft('missing')).toBe('');
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
