// @vitest-environment jsdom
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { act, renderHook, waitFor } from '@testing-library/react';
import { clearDraft, discardDraft, getDraft, getDraftVersion, migrateDraft, resetDraftTextsForTests, saveDraft, useDraftSessionIds } from './composerDraft';
import { hydrateDrafts } from './newConversationDrafts';
import { closeDraftDbForTests, transact } from './draftDb';

const stored = (id: string) => transact(['texts'], 'readonly', (tx) => tx.get<{ text: string; revision: number }>('texts', id));
/** Another tab: a fresh module instance over the same database. */
async function otherTab() {
  vi.resetModules();
  const drafts = await import('./composerDraft');
  await (await import('./newConversationDrafts')).hydrateDrafts();
  return drafts;
}

beforeEach(() => { localStorage.clear(); resetDraftTextsForTests(); });

describe('composerDraft', () => {
  it('stores, clears and persists drafts', async () => {
    saveDraft('s1', 'hello');
    expect(getDraft('s1')).toBe('hello');
    await waitFor(async () => expect(await stored('s1')).toEqual({ text: 'hello', revision: 0 }));
    saveDraft('s1', '');
    expect(getDraft('s1')).toBe('');
    await waitFor(async () => expect(await stored('s1')).toEqual({ text: '', revision: 1 }));
  });

  it('tracks which sessions hold a draft', () => {
    const { result } = renderHook(() => useDraftSessionIds());
    expect(result.current.has('s1')).toBe(false);
    act(() => saveDraft('s1', 'draft text'));
    expect(result.current.has('s1')).toBe(true);
    act(() => clearDraft('s1'));
    expect(result.current.has('s1')).toBe(false);
  });

  it('does not let a stale autosave land after another tab discards', async () => {
    saveDraft('race', 'first');
    await waitFor(async () => expect((await stored('race'))?.text).toBe('first'));
    const peer = await otherTab();
    const staleVersion = getDraftVersion('race');
    peer.discardDraft('race');
    // This tab has not observed the discard yet: its autosave uses the old revision.
    saveDraft('race', 'stale autosave', staleVersion);
    await waitFor(async () => expect(await stored('race')).toEqual({ text: '', revision: 1 }));
    await waitFor(() => expect(getDraft('race')).toBe(''));
  });

  it('clears only the text it saw, preserving a newer edit from another tab', async () => {
    saveDraft('sent', 'sent prompt');
    await waitFor(async () => expect((await stored('sent'))?.text).toBe('sent prompt'));
    const peer = await otherTab();
    // The peer's write is queued first; this tab has not observed it when it clears.
    peer.saveDraft('sent', 'newer peer edit');
    clearDraft('sent');
    await waitFor(() => expect(getDraft('sent')).toBe('newer peer edit'));
    expect((await stored('sent'))?.text).toBe('newer peer edit');
  });

  it('applies changes committed by other tabs', async () => {
    const peer = await otherTab();
    vi.resetModules();
    const self = await import('./composerDraft');
    peer.saveDraft('shared', 'from the peer');
    await waitFor(() => expect(self.getDraft('shared')).toBe('from the peer'));
  });

  it('moves text atomically and keeps a destination edit', async () => {
    saveDraft('from', 'moved text');
    saveDraft('busy', 'source text');
    saveDraft('dest', 'destination edit');
    expect(await migrateDraft('from', 'to')).toBe(true);
    expect(getDraft('to')).toBe('moved text');
    expect(getDraft('from')).toBe('');
    expect(await stored('to')).toEqual({ text: 'moved text', revision: 0 });
    expect(await migrateDraft('busy', 'dest')).toBe(true);
    expect(getDraft('dest')).toBe('destination edit');
    expect(await migrateDraft('missing', 'elsewhere')).toBe(true);
  });

  it('reports a failed move and keeps the source', async () => {
    saveDraft('keep', 'only copy');
    await waitFor(async () => expect((await stored('keep'))?.text).toBe('only copy'));
    const open = vi.spyOn(indexedDB, 'open').mockImplementation(() => { throw new Error('storage unavailable'); });
    try {
      closeDraftDbForTests();
      expect(await migrateDraft('keep', 'elsewhere')).toBe(false);
      expect(getDraft('keep')).toBe('only copy');
    } finally { open.mockRestore(); }
  });

  it('imports the pre-IndexedDB localStorage map once without overwriting stored text', async () => {
    saveDraft('existing', 'stored text');
    await waitFor(async () => expect((await stored('existing'))?.text).toBe('stored text'));
    localStorage.setItem('ocman.composerDrafts.v1', JSON.stringify({ legacy: 'old text', existing: 'older text', bad: 1 }));
    resetDraftTextsForTests();
    await hydrateDrafts();
    expect(getDraft('legacy')).toBe('old text');
    expect(getDraft('existing')).toBe('stored text');
    expect(localStorage.getItem('ocman.composerDrafts.v1')).toBeNull();
  });

  it('ignores a malformed legacy map', async () => {
    localStorage.setItem('ocman.composerDrafts.v1', 'not json');
    await hydrateDrafts();
    expect(getDraft('anything')).toBe('');
  });

  it('keeps a local edit made before hydration finishes', async () => {
    await transact(['texts'], 'readwrite', (tx) => tx.put('texts', 'early', { text: 'stored', revision: 0 }));
    const hydration = hydrateDrafts();
    saveDraft('early', 'typed during startup');
    await hydration;
    expect(getDraft('early')).toBe('typed during startup');
  });

  it('reclaims prompt bytes on discard', async () => {
    saveDraft('reclaim', 'large unique prompt payload');
    discardDraft('reclaim');
    await waitFor(async () => expect(await stored('reclaim')).toEqual({ text: '', revision: 1 }));
  });
});
