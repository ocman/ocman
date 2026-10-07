// @vitest-environment jsdom
import { beforeEach, expect, it, vi } from 'vitest';
import { getDraft, saveDraft } from './composerDraft';
import { forgetConversationDraft, rememberConversationDraft, useNewConversationDrafts } from './newConversationDrafts';

beforeEach(() => {
  localStorage.clear();
  useNewConversationDrafts.setState({ drafts: [] });
});

it('persists independent targets and selections and only discards the selected draft', async () => {
  rememberConversationDraft({ draftId: 'first', directory: '/repo', model: 'p/m', agent: 'plan', target: 'current' });
  rememberConversationDraft({ draftId: 'second', directory: '/repo', remoteId: 'box' });
  rememberConversationDraft({ draftId: 'first', directory: '/other', reasoning: 'high' });
  saveDraft('first', 'one');
  saveDraft('second', 'two');
  expect(useNewConversationDrafts.getState().drafts[0]).toEqual({
    draftId: 'first', directory: '/other', model: 'p/m', agent: 'plan', reasoning: 'high', target: 'current',
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
});

it('merges another tab before writing and observes cross-tab discards', async () => {
  vi.resetModules();
  const otherTab = await import('./newConversationDrafts');
  rememberConversationDraft({ draftId: 'first', directory: '/repo' });
  otherTab.rememberConversationDraft({ draftId: 'second', directory: '/repo' });
  rememberConversationDraft({ draftId: 'first', directory: '/updated' });
  expect(useNewConversationDrafts.getState().drafts.map((draft) => draft.draftId)).toEqual(['first', 'second']);
  otherTab.forgetConversationDraft('first');
  window.dispatchEvent(new StorageEvent('storage', { key: 'ocman.newConversationDrafts.v1' }));
  expect(useNewConversationDrafts.getState().drafts.map((draft) => draft.draftId)).toEqual(['second']);
  rememberConversationDraft({ draftId: 'third', directory: '/repo' });
  expect(useNewConversationDrafts.getState().drafts.map((draft) => draft.draftId)).toEqual(['second', 'third']);
});

it.each(['null', '{}', '[null, {}, {"draftId": 1, "directory": "/repo"}]', 'invalid'])('ignores malformed storage %s', async (raw) => {
  localStorage.setItem('ocman.newConversationDrafts.v1', raw);
  vi.resetModules();
  const restored = await import('./newConversationDrafts');
  expect(restored.useNewConversationDrafts.getState().drafts).toEqual([]);
});
