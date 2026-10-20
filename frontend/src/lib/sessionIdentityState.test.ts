// @vitest-environment jsdom
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { waitFor } from '@testing-library/react';
import { ensureSessionBrowserState, migrateSessionBrowserState, resetSessionIdentityStateForTests } from './sessionIdentityState';
import { getDraft, resetDraftTextsForTests, saveDraft } from './composerDraft';
import { listFailedSends, recordFailedSend } from './failedSends';
import { transact } from './draftDb';
import type { DraftStart } from './draftStartClaims';
import { loadAllMessageBookmarks } from './messageBookmarks';

const mocks = vi.hoisted(() => ({ lookup: vi.fn() }));
vi.mock('./sessionIdentity', () => ({ lookupSessionIdentity: mocks.lookup }));
const identity = { id: 'ocm_local', nativeId: 'legacy', platform: 'test', ownerId: 'owner' };
const bookmarks = (id: string) => `ocman:message-bookmarks:${id}`;

beforeEach(() => { localStorage.clear(); resetSessionIdentityStateForTests(); resetDraftTextsForTests(); mocks.lookup.mockReset(); });

describe('session identity browser-state adoption', () => {
  it('keeps message IDs and payloads while relocating session-owned recovery state', async () => {
    localStorage.setItem(bookmarks('legacy'), JSON.stringify([{ id: 'message', sessionId: 'legacy', preview: 'legacy' }]));
    recordFailedSend('legacy', { id: 'failed', text: 'legacy', error: 'offline', failedAt: 1 });
    saveDraft('legacy', 'unsent');
    await waitFor(async () => expect(await transact(['texts'], 'readonly', (tx) => tx.get('texts', 'legacy'))).toBeDefined());
    const pending: DraftStart = { version: 4, text: 'first prompt', attemptId: 'attempt', deliveryOwner: 'old-tab', deliveryState: 'pending' };
    await transact(['starts'], 'readwrite', (tx) => tx.put('starts', 'first-delivery:legacy', pending));

    await migrateSessionBrowserState(identity);
    expect(JSON.parse(localStorage.getItem(bookmarks('ocm_local'))!)).toEqual([{ id: 'message', sessionId: 'ocm_local', preview: 'legacy' }]);
    expect(listFailedSends('ocm_local')).toEqual(listFailedSends('legacy'));
    expect(getDraft('ocm_local')).toBe('unsent');
    expect(getDraft('legacy')).toBe('');
    expect(await transact(['starts'], 'readonly', (tx) => tx.get('starts', 'first-delivery:ocm_local'))).toEqual(pending);
    expect(await transact(['starts'], 'readonly', (tx) => tx.get('starts', 'first-delivery:legacy'))).toEqual(pending);
    expect(loadAllMessageBookmarks(undefined, [], 0).map((b) => b.sessionId)).toEqual(['ocm_local']);
  });

  it('does not overwrite canonical bookmarks or composer text, or resurrect a removed bookmark', async () => {
    localStorage.setItem(bookmarks('legacy'), JSON.stringify([{ id: 'same', sessionId: 'legacy', preview: 'old' }]));
    localStorage.setItem(bookmarks('ocm_local'), JSON.stringify([{ id: 'same', sessionId: 'ocm_local', preview: 'new' }]));
    saveDraft('legacy', 'old text'); saveDraft('ocm_local', 'new text');
    await migrateSessionBrowserState(identity);
    expect(JSON.parse(localStorage.getItem(bookmarks('ocm_local'))!)[0].preview).toBe('new');
    expect(getDraft('legacy')).toBe('old text'); expect(getDraft('ocm_local')).toBe('new text');
    localStorage.removeItem(bookmarks('ocm_local'));
    resetSessionIdentityStateForTests();
    await migrateSessionBrowserState(identity);
    expect(localStorage.getItem(bookmarks('ocm_local'))).toBeNull();
  });

  it('never assigns an unqualified native cache to a remote owner with the same ID', async () => {
    localStorage.setItem(bookmarks('legacy'), JSON.stringify([{ id: 'local-message', sessionId: 'legacy' }]));
    localStorage.setItem(bookmarks('r-machine:test:legacy'), JSON.stringify([{ id: 'remote-message', sessionId: 'legacy' }]));
    await migrateSessionBrowserState({ ...identity, id: 'ocm_remote', ownerId: 'machine', platform: 'r-machine:test' });
    expect(JSON.parse(localStorage.getItem(bookmarks('ocm_remote'))!)).toEqual([{ id: 'remote-message', sessionId: 'ocm_remote' }]);
    expect(localStorage.getItem(bookmarks('legacy'))).toContain('local-message');
  });

  it('does not reassign legacy copies to a different physical source with a colliding native ID', async () => {
    localStorage.setItem(bookmarks('legacy'), JSON.stringify([{ id: 'old-source-message', sessionId: 'legacy' }]));
    await migrateSessionBrowserState(identity);
    await migrateSessionBrowserState({ ...identity, id: 'ocm_new_source' });
    expect(localStorage.getItem(bookmarks('ocm_new_source'))).toBeNull();
    expect(localStorage.getItem(bookmarks('ocm_local'))).toContain('old-source-message');
  });

  it('preserves source data and allows retry when storage is unavailable', async () => {
    localStorage.setItem(bookmarks('legacy'), JSON.stringify([{ id: 'message', sessionId: 'legacy' }]));
    const write = vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => { throw new Error('quota'); });
    await expect(migrateSessionBrowserState(identity)).rejects.toThrow('quota');
    expect(localStorage.getItem(bookmarks('legacy'))).toContain('message');
    write.mockRestore();
    await migrateSessionBrowserState(identity);
    expect(localStorage.getItem(bookmarks('ocm_local'))).toContain('ocm_local');
  });

  it('resolves canonical deep links once before exposing migrated first-delivery state', async () => {
    mocks.lookup.mockResolvedValue(identity);
    await Promise.all([ensureSessionBrowserState('ocm_local'), ensureSessionBrowserState('ocm_local')]);
    await ensureSessionBrowserState('ocm_local');
    expect(mocks.lookup).toHaveBeenCalledTimes(1);
    await ensureSessionBrowserState('native');
    expect(mocks.lookup).toHaveBeenCalledTimes(1);
  });

  it('retries failed-send persistence without resetting memory or overwriting canonical payloads', async () => {
    const id = 'quota-retry';
    recordFailedSend('legacy', { id, text: 'legacy payload', error: 'offline', failedAt: 1 });
    const original = Storage.prototype.setItem;
    const write = vi.spyOn(Storage.prototype, 'setItem').mockImplementation(function (this: Storage, key, value) {
      if (key === 'ocman.failedSends.v1') throw new Error('quota');
      original.call(this, key, value);
    });
    recordFailedSend('ocm_local', { id, text: 'newer canonical payload', error: 'offline', failedAt: 2 });
    await expect(migrateSessionBrowserState(identity)).rejects.toThrow('Failed-send recovery');
    write.mockRestore();
    await migrateSessionBrowserState(identity);
    const saved = JSON.parse(localStorage.getItem('ocman.failedSends.v1')!);
    expect(saved.ocm_local.find((entry: { id: string }) => entry.id === id).text).toBe('newer canonical payload');
  });
});
