// @vitest-environment jsdom
import { beforeEach, expect, it, vi } from 'vitest';
import { getDraft, saveDraft } from './composerDraft';
import { beginConversationStart, completeConversationStart, endConversationStart, forgetConversationDraft, reconcileConversationStart, rememberConversationDraft, useNewConversationDrafts } from './newConversationDrafts';
vi.mock('./draftStartClaims', () => ({
  claimDraftStart: async (_id: string, start: import('./draftStartClaims').DraftStart) => ({ claimed: true, start }),
  persistDraftStart: async (_id: string, start: import('./draftStartClaims').DraftStart) => start,
  readDraftStart: async (id: string) => useNewConversationDrafts.getState().starts[id],
}));

beforeEach(() => {
  localStorage.clear();
  window.dispatchEvent(new StorageEvent('storage', { key: null }));
  useNewConversationDrafts.setState({ drafts: [], starts: {} });
});

it('persists independent targets and selections and only discards the selected draft', async () => {
  rememberConversationDraft({ draftId: 'first', directory: '/repo', model: 'p/m', agent: 'plan', target: 'current' });
  rememberConversationDraft({ draftId: 'second', directory: '/repo', remoteId: 'box' });
  rememberConversationDraft({ draftId: 'first', directory: '/other', reasoning: 'high' });
  saveDraft('first', 'one');
  saveDraft('second', 'two');
  expect(useNewConversationDrafts.getState().drafts[0]).toEqual({
    draftId: 'first', directory: '/other', model: 'p/m', agent: 'plan', reasoning: 'high', target: 'current', createdAt: expect.any(Number),
  });
  vi.resetModules();
  const restored = await import('./newConversationDrafts');
  expect(restored.useNewConversationDrafts.getState().drafts).toEqual(useNewConversationDrafts.getState().drafts);
  forgetConversationDraft('first');
  expect(useNewConversationDrafts.getState().drafts.map((draft) => draft.draftId)).toEqual(['second']);
  expect(getDraft('first')).toBe('');
  expect(getDraft('second')).toBe('two');
});

it('keeps live drafts when storage refuses writes', () => {
  const write = vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => { throw new Error('quota'); });
  rememberConversationDraft({ draftId: 'first', directory: '/repo' });
  expect(useNewConversationDrafts.getState().drafts).toHaveLength(1);
  write.mockRestore();
  rememberConversationDraft({ draftId: 'second', directory: '/repo' });
  expect(useNewConversationDrafts.getState().drafts.map((draft) => draft.draftId)).toEqual(['first', 'second']);
});

it('holds the start guard by draft identity, releases failures and remembers completed sessions', async () => {
  rememberConversationDraft({ draftId: 'pending', directory: '/repo' });
  expect(await beginConversationStart('pending', 'prompt')).toEqual(expect.any(Number));
  expect(await beginConversationStart('pending', 'duplicate')).toBeNull();
  endConversationStart('pending');
  expect(await beginConversationStart('pending', 'retry')).toEqual(expect.any(Number));
  await completeConversationStart('pending', { sessionId: 'session', platform: 'opencode', remoteId: 'local', directory: '/repo' });
  endConversationStart('pending');
  expect(useNewConversationDrafts.getState().drafts).toEqual([]);
  expect(useNewConversationDrafts.getState().starts.pending.sessionId).toBe('session');
  expect(await beginConversationStart('pending', 'duplicate')).toBeNull();
});

it('merges another tab before writing and observes cross-tab discards', async () => {
  vi.resetModules();
  const otherTab = await import('./newConversationDrafts');
  rememberConversationDraft({ draftId: 'first', directory: '/repo' });
  otherTab.rememberConversationDraft({ draftId: 'second', directory: '/repo' });
  rememberConversationDraft({ draftId: 'first', directory: '/updated' });
  expect(useNewConversationDrafts.getState().drafts.map((draft) => draft.draftId)).toEqual(['first', 'second']);
  otherTab.forgetConversationDraft('first');
  window.dispatchEvent(new StorageEvent('storage', { key: 'ocman.newConversationDrafts.v1:first' }));
  expect(useNewConversationDrafts.getState().drafts.map((draft) => draft.draftId)).toEqual(['second']);
  rememberConversationDraft({ draftId: 'third', directory: '/repo' });
  expect(useNewConversationDrafts.getState().drafts.map((draft) => draft.draftId)).toEqual(['second', 'third']);
});

it('keeps both drafts when two tabs interleave their metadata writes', async () => {
  vi.resetModules();
  const otherTab = await import('./newConversationDrafts');
  const original = Storage.prototype.setItem;
  let interleaved = false;
  const write = vi.spyOn(Storage.prototype, 'setItem').mockImplementation(function (this: Storage, key, value) {
    if (key.startsWith('ocman.newConversationDrafts') && !interleaved) {
      interleaved = true;
      otherTab.rememberConversationDraft({ draftId: 'second', directory: '/repo' });
    }
    original.call(this, key, value);
  });
  try {
    rememberConversationDraft({ draftId: 'first', directory: '/repo' });
    vi.resetModules();
    const restored = await import('./newConversationDrafts');
    expect(restored.useNewConversationDrafts.getState().drafts.map((draft) => draft.draftId).sort()).toEqual(['first', 'second']);
  } finally { write.mockRestore(); }
});

it('reconciles a stale mirror from the authoritative failure and restores missing text', async () => {
  rememberConversationDraft({ draftId: 'reload', directory: '/repo' });
  useNewConversationDrafts.setState({ starts: { reload: { version: 0, text: 'prompt' } } });
  const claims = await import('./draftStartClaims');
  const read = vi.spyOn(claims, 'readDraftStart').mockResolvedValue({ version: 0, text: 'prompt', error: 'failed' });
  try {
    await reconcileConversationStart('reload');
    expect(useNewConversationDrafts.getState().starts.reload.error).toBe('failed');
    expect(getDraft('reload')).toBe('prompt');
    read.mockResolvedValue(undefined);
    await reconcileConversationStart('reload');
    expect(useNewConversationDrafts.getState().starts.reload).toBeUndefined();
  } finally { read.mockRestore(); }
});

it('does not replace a newer local receipt with an outstanding read', async () => {
  const claims = await import('./draftStartClaims');
  let finish!: (value: import('./draftStartClaims').DraftStart) => void;
  const read = vi.spyOn(claims, 'readDraftStart').mockReturnValue(new Promise((resolve) => { finish = resolve; }));
  try {
    const pending = reconcileConversationStart('draft');
    const completed = { version: 0, text: '', sessionId: 'session' };
    useNewConversationDrafts.setState({ starts: { draft: completed } });
    finish({ version: 0, text: 'old prompt' });
    await pending;
    expect(useNewConversationDrafts.getState().starts.draft).toBe(completed);
  } finally { read.mockRestore(); }
});

it.each(['null', '{}', '[null, {}, {"draftId": 1, "directory": "/repo"}]', 'invalid'])('ignores malformed storage %s', async (raw) => {
  localStorage.setItem('ocman.newConversationDrafts.v1:invalid', raw);
  vi.resetModules();
  const restored = await import('./newConversationDrafts');
  expect(restored.useNewConversationDrafts.getState().drafts).toEqual([]);
});
