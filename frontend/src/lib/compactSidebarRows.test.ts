import { expect, it } from 'vitest';
import type { Session } from './api';
import { beginCompactSidebarRead, finishCompactSidebarRead, markCompactSidebarUpdate, preserveCompactSidebarRows } from './compactSidebarRows';

const row = (id: string, platform = 'local') => ({ id, platform, title: 'old', timeCreated: 1 } as Session);

it('preserves changed rows and insertions, not stale HTTP values', () => {
  const old = row('one');
  const read = beginCompactSidebarRead();
  try {
    const next = { ...old, title: 'new' };
    const added = { ...row('two'), lastUserPromptAt: 10 };
    markCompactSidebarUpdate(next);
    markCompactSidebarUpdate(added);
    expect(preserveCompactSidebarRows([old], [next, added], read)).toEqual([added, next]);
  } finally { finishCompactSidebarRead(read); }
});
it('keeps event removals removed without conflating owners', () => {
  const old = row('same');
  const remote = row('same', 'remote');
  const read = beginCompactSidebarRead();
  try {
    markCompactSidebarUpdate(old);
    expect(preserveCompactSidebarRows([old, remote], [remote], read)).toEqual([remote]);
  } finally { finishCompactSidebarRead(read); }
});
it('keeps newer partial patches and lets later HTTP reads reconcile', () => {
  const old = row('one');
  const read = beginCompactSidebarRead();
  const next = { ...old, title: 'new' };
  markCompactSidebarUpdate(next);
  const patched = { ...next, status: 'busy' } as Session;
  expect(preserveCompactSidebarRows([old], [patched], read)).toEqual([patched]);
  finishCompactSidebarRead(read);
  const later = beginCompactSidebarRead();
  expect(preserveCompactSidebarRows([old], [patched], later)).toEqual([old]);
  finishCompactSidebarRead(later);
});
it('releases cancelled reads even when the transport ignores cancellation', () => {
  const controller = new AbortController();
  const read = beginCompactSidebarRead(controller.signal);
  controller.abort();
  markCompactSidebarUpdate(row('ignored'));
  expect(read.size).toBe(0);
  const alreadyAborted = beginCompactSidebarRead(controller.signal);
  markCompactSidebarUpdate(row('also-ignored'));
  expect(alreadyAborted.size).toBe(0);
});
