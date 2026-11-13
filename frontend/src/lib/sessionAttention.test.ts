import { expect, it } from 'vitest';
import { attentionSessionEntries, recentSessionAttentionEntries, sessionAttentionKey } from './sessionAttention';
import { SESSION_FILTER_DEFAULTS } from './sessionListFilters';
import type { NotifyEntry, Session } from './api';

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

it('matches recent attention by owner and retains prompts on running or viewed rows', () => {
  const entry: NotifyEntry = { id: 'same', platform: 'local', status: 'waiting', seen: false };
  const prompt = { ...entry, id: 'prompt', pendingPermission: true };
  const remote = { ...entry, platform: 'remote' };
  const rows = [
    { ...entry, status: 'busy' }, remote,
    { ...prompt, status: 'busy', seen: true, factoryAttemptId: 'attempt' },
  ] as Session[];
  expect(recentSessionAttentionEntries([entry, remote, remote, prompt], rows, SESSION_FILTER_DEFAULTS)).toEqual([remote, prompt]);
});

it('keeps finite background work out of completion counts but not services or prompts', () => {
  const entry: NotifyEntry = { id: 'task', platform: 'local', status: 'waiting', seen: false };
  const service = { ...entry, id: 'service' };
  const prompt = { ...entry, id: 'prompt', pendingQuestion: true };
  const entries = [entry, service, prompt];
  const rows = entries.map(row => ({ ...row, backgroundTaskCount: 1,
    backgroundServiceCount: row.id === 'service' ? 1 : 0 })) as Session[];
  expect(recentSessionAttentionEntries(entries, rows, SESSION_FILTER_DEFAULTS)).toEqual([service, prompt]);
});

it('honors shared child, prompt, pin and snooze visibility rules', () => {
  const entry: NotifyEntry = { id: 'child', platform: 'local', status: 'waiting', seen: false };
  const pinned = { ...entry, id: 'pinned' };
  const prompt = { ...entry, id: 'prompt', pendingPermission: true };
  const snoozed = { ...prompt, id: 'snoozed' };
  const rows = [
    { ...entry, parentId: 'parent' },
    { ...pinned, pinned: true, factoryAttemptId: 'attempt' },
    { ...prompt, factoryAttemptId: 'attempt' },
    { ...snoozed, pinned: true, snoozedUntil: 100 },
  ] as Session[];
  expect(recentSessionAttentionEntries([entry, pinned, prompt, snoozed], rows,
    { ...SESSION_FILTER_DEFAULTS, showChildren: false, alwaysShowPrompts: false })).toEqual([pinned]);
});
