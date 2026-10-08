// @vitest-environment jsdom
import { beforeEach, expect, it, vi } from 'vitest';
import { waitFor } from '@testing-library/react';
import { clearDraft, discardDraft, getDraft, resetDraftTextsForTests, saveDraft } from './composerDraft';
import {
  beginConversationStart, completeConversationStart, endConversationStart, failConversationStart, forgetConversationDraft,
  getConversationDraft, hydrateDrafts, reconcileConversationStart, rememberConversationDraft, retryDraftRelocation,
  selectionsKey, useNewConversationDrafts,
} from './newConversationDrafts';
import { getPendingDraftPayload, updateDraftAttachments } from './pendingDraftPayloads';
import { readDraftStart } from './draftStartClaims';
import { transact } from './draftDb';

vi.mock('./remoteLog', () => ({ remoteLog: { error: vi.fn(), warn: vi.fn() } }));

const created = { sessionId: 'created', platform: 'opencode', remoteId: 'local', directory: '/repo' };
const route = 'local:/repo:opencode:Draft';
const draft = (draftId: string, extra = {}) => ({ draftId, directory: '/repo', remoteId: 'local', platform: 'opencode', title: 'Draft', ...extra });
const storedDraft = (id: string) => transact(['drafts'], 'readonly', (tx) => tx.get<Record<string, unknown>>('drafts', id));
const settle = () => waitFor(async () => expect(await transact(['drafts'], 'readonly', () => true)).toBe(true));

/** Submit like the composer: claim, then clear the sent prompt. */
async function submit(id: string, text: string, extra = {}) {
  rememberConversationDraft(draft(id, extra));
  saveDraft(id, text);
  const revision = (await beginConversationStart(id, route))!;
  clearDraft(id); // The composer clears the sent prompt without a new revision.
  return { revision: revision, routeKey: route, selections: selectionsKey(getConversationDraft(id)!) };
}

async function otherTab() {
  vi.resetModules();
  const peer = await import('./newConversationDrafts');
  await peer.hydrateDrafts();
  return { peer, texts: await import('./composerDraft') };
}

beforeEach(() => {
  localStorage.clear();
  resetDraftTextsForTests();
  useNewConversationDrafts.setState({ drafts: [], starts: {} });
});

it('persists independent targets and selections and only discards the selected draft', async () => {
  rememberConversationDraft(draft('first', { model: 'p/m', agent: 'plan', target: 'current' }));
  rememberConversationDraft(draft('second', { remoteId: 'box' }));
  rememberConversationDraft({ ...draft('first'), reasoning: 'high' });
  saveDraft('first', 'one');
  saveDraft('second', 'two');
  await settle();
  const { peer, texts } = await otherTab();
  expect(peer.getConversationDraft('first')).toMatchObject({ model: 'p/m', agent: 'plan', reasoning: 'high', target: 'current' });
  forgetConversationDraft('first');
  await waitFor(() => expect(peer.useNewConversationDrafts.getState().drafts.map((entry) => entry.draftId)).toEqual(['second']));
  await waitFor(() => expect(texts.getDraft('first')).toBe(''));
  expect(texts.getDraft('second')).toBe('two');
});

it('lets only one tab start a draft and repairs a failure this tab could not store', async () => {
  rememberConversationDraft(draft('pending'));
  expect(await beginConversationStart('pending', route)).toBe(0);
  const { peer } = await otherTab();
  expect(await peer.beginConversationStart('pending', route)).toBeNull();
  // The failure could not be written: the live receipt is repaired inside the next claim.
  useNewConversationDrafts.setState((state) => ({ starts: { pending: { ...state.starts.pending, error: 'lost', persistenceError: 'quota' } } }));
  expect(await beginConversationStart('pending', route)).toBe(0);
  endConversationStart('pending');
  expect(useNewConversationDrafts.getState().starts.pending).toBeUndefined();
});

it('retires an unchanged submitted draft and records the session', async () => {
  const submitted = await submit('retire', 'prompt');
  await completeConversationStart('retire', created, submitted);
  expect(getConversationDraft('retire')).toBeUndefined();
  expect(useNewConversationDrafts.getState().starts.retire).toMatchObject({ sessionId: 'created' });
  expect(useNewConversationDrafts.getState().starts.retire.replacementDraftId).toBeUndefined();
  expect(await storedDraft('retire')).toMatchObject({ deleted: true });
  expect((await readDraftStart('retire'))?.sessionId).toBe('created');
  expect(await beginConversationStart('retire', route)).toBeNull();
});

it.each([
  ['text', (id: string) => saveDraft(id, 'newer task')],
  ['selections', (id: string) => rememberConversationDraft({ ...draft(id), agent: 'build' })],
  ['owner', (id: string) => rememberConversationDraft({ ...draft(id), remoteId: 'box' })],
])('moves a newer %s edit to a fresh identity in the same transaction', async (_change, edit) => {
  const id = `edit-${_change}`;
  const submitted = await submit(id, 'prompt', { agent: 'plan' });
  updateDraftAttachments(id, { images: [], files: [{ path: '', name: 'kept.txt', mime: 'text/plain' }] });
  edit(id);
  await completeConversationStart(id, created, submitted);
  const replacement = useNewConversationDrafts.getState().starts[id].replacementDraftId!;
  expect(replacement).toBeTruthy();
  expect(getConversationDraft(id)).toBeUndefined();
  expect(getConversationDraft(replacement)).toBeTruthy();
  expect(getPendingDraftPayload(replacement)?.files).toHaveLength(1);
  if (_change === 'text') expect(getDraft(replacement)).toBe('newer task');
  expect(await storedDraft(replacement)).toMatchObject({ draftId: replacement });
});

it('applies a peer edit committed before completion and leaves the peer pointed at the replacement', async () => {
  const submitted = await submit('peer-edit', 'prompt');
  await settle();
  const { peer, texts } = await otherTab();
  texts.saveDraft('peer-edit', 'typed in another tab');
  await settle();
  vi.resetModules();
  await completeConversationStart('peer-edit', created, submitted);
  const replacement = useNewConversationDrafts.getState().starts['peer-edit'].replacementDraftId!;
  expect(getDraft(replacement)).toBe('typed in another tab');
  await waitFor(() => expect(peer.useNewConversationDrafts.getState().starts['peer-edit']?.replacementDraftId).toBe(replacement));
  await waitFor(() => expect(texts.getDraft(replacement)).toBe('typed in another tab'));
});

it('restores a failed prompt only when no newer edit or discard happened', async () => {
  const kept = await submit('fail-restore', 'restore me');
  await failConversationStart('fail-restore', 'boom', { text: 'restore me', revision: kept.revision });
  expect(getDraft('fail-restore')).toBe('restore me');
  expect(useNewConversationDrafts.getState().starts['fail-restore'].error).toBe('boom');
  const discarded = await submit('fail-discard', 'do not restore');
  discardDraft('fail-discard');
  await failConversationStart('fail-discard', 'boom', { text: 'do not restore', revision: discarded.revision });
  expect(getDraft('fail-discard')).toBe('');
  expect(await beginConversationStart('fail-restore', route)).toEqual(expect.any(Number));
});

it('never turns a created session back into a failure', async () => {
  const submitted = await submit('final', 'prompt');
  await completeConversationStart('final', created, submitted);
  await failConversationStart('final', 'late failure');
  expect((await readDraftStart('final'))).toMatchObject({ sessionId: 'created', error: undefined });
});

it('does not let a late autosave resurrect a retired or discarded draft', async () => {
  const submitted = await submit('retired', 'prompt');
  await completeConversationStart('retired', created, submitted);
  forgetConversationDraft('gone');
  rememberConversationDraft(draft('retired', { agent: 'late' }));
  rememberConversationDraft(draft('gone'));
  await settle();
  await waitFor(async () => expect(await storedDraft('retired')).toMatchObject({ deleted: true }));
  expect(await storedDraft('gone')).toMatchObject({ deleted: true });
  await hydrateDrafts();
  expect(getConversationDraft('retired')).toBeUndefined();
  expect(getConversationDraft('gone')).toBeUndefined();
});

it('records the session alone when the atomic completion fails, then completes on retry with current edits', async () => {
  const submitted = await submit('quota', 'prompt');
  saveDraft('quota', 'retained text');
  await settle();
  const original = IDBObjectStore.prototype.put;
  const fail = vi.spyOn(IDBObjectStore.prototype, 'put').mockImplementation(function (this: IDBObjectStore, value, key) {
    if (this.name === 'texts') throw new DOMException('quota', 'QuotaExceededError');
    return original.call(this, value, key);
  });
  try { await completeConversationStart('quota', created, submitted); } finally { fail.mockRestore(); }
  // The receipt says a session exists; the draft and its text are untouched.
  expect(useNewConversationDrafts.getState().starts.quota).toMatchObject({ sessionId: 'created', relocationError: expect.any(String) });
  expect(await readDraftStart('quota')).toMatchObject({ sessionId: 'created', relocationError: expect.any(String) });
  expect(getDraft('quota')).toBe('retained text');
  expect(await beginConversationStart('quota', route)).toBeNull();
  // A selection change before retry is part of what the retry moves.
  rememberConversationDraft({ ...draft('quota'), agent: 'build' });
  await retryDraftRelocation('quota');
  const replacement = useNewConversationDrafts.getState().starts.quota.replacementDraftId!;
  expect(useNewConversationDrafts.getState().starts.quota.relocationError).toBeUndefined();
  expect(getDraft(replacement)).toBe('retained text');
  expect(getConversationDraft(replacement)).toMatchObject({ agent: 'build' });
  // Retrying again is idempotent: no second replacement.
  await retryDraftRelocation('quota');
  expect(useNewConversationDrafts.getState().drafts.map((entry) => entry.draftId)).toEqual([replacement]);
});

it('keeps a receipt this tab could not store and repairs it on reconciliation', async () => {
  const submitted = await submit('unstored', 'prompt');
  const fail = vi.spyOn(IDBObjectStore.prototype, 'put').mockImplementation(() => { throw new DOMException('quota', 'QuotaExceededError'); });
  try { await completeConversationStart('unstored', created, submitted); } finally { fail.mockRestore(); }
  expect(useNewConversationDrafts.getState().starts.unstored).toMatchObject({ sessionId: 'created', persistenceError: expect.any(String) });
  await reconcileConversationStart('unstored');
  expect(await readDraftStart('unstored')).toMatchObject({ sessionId: 'created' });
  expect(getConversationDraft('unstored')).toBeUndefined();
});

it('surfaces a failed reconciliation repair', async () => {
  useNewConversationDrafts.setState({ starts: { broken: { version: 0, text: '', error: 'failed', persistenceError: 'quota' } } });
  const fail = vi.spyOn(IDBObjectStore.prototype, 'put').mockImplementation(() => { throw new DOMException('quota', 'QuotaExceededError'); });
  try { await expect(reconcileConversationStart('broken')).rejects.toThrow(); } finally { fail.mockRestore(); }
});

it('drops this tab\'s attachments when a peer discards the draft', async () => {
  rememberConversationDraft(draft('peer-discard'));
  updateDraftAttachments('peer-discard', { images: [], files: [{ path: '', name: 'local.txt', mime: 'text/plain' }] });
  await settle();
  const { peer } = await otherTab();
  peer.forgetConversationDraft('peer-discard');
  await waitFor(() => expect(getConversationDraft('peer-discard')).toBeUndefined());
  await waitFor(() => expect(getPendingDraftPayload('peer-discard')).toBeUndefined());
});
