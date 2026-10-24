import { expect, it } from 'vitest';
import { attentionSessionEntries, sessionAttentionKey } from './sessionAttention';
import type { NotifyEntry } from './api';

it('shares badge eligibility and deduplicates by owner without losing the first prompt', () => {
  const entry: NotifyEntry = { id: 'same', platform: 'local', status: 'interrupted', seen: false };
  const prompt = { ...entry, pendingPermission: true };
  expect(attentionSessionEntries([
    prompt, entry,
    { ...entry, platform: 'remote' },
    { ...entry, id: 'seen', seen: true },
    { ...entry, id: 'judging', suppressTerminal: true },
    { ...entry, id: 'question', seen: true, pendingQuestion: true },
  ])).toEqual([prompt, { ...entry, platform: 'remote' }, { ...entry, id: 'question', seen: true, pendingQuestion: true }]);
  expect(sessionAttentionKey({ id: 'old' })).toBe(':old');
});
