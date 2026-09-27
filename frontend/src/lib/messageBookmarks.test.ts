// @vitest-environment jsdom
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import type { Message, Part, Session } from './api';
import {
  buildMessageBookmarks, groupMessageBookmarks, loadAllMessageBookmarks, loadMessageBookmarks,
  messageBookmarkKey, saveMessageBookmarks,
} from './messageBookmarks';
import type { MessageBookmark } from './messageBookmarks';

// A full Storage: the shared setup shim has no key()/length.
beforeEach(() => {
  const mem = new Map<string, string>();
  vi.stubGlobal('localStorage', {
    getItem: (k: string) => mem.get(k) ?? null,
    setItem: (k: string, v: string) => { mem.set(k, v); },
    removeItem: (k: string) => { mem.delete(k); },
    key: (i: number) => [...mem.keys()][i] ?? null,
    get length() { return mem.size; },
  });
});
afterEach(() => vi.unstubAllGlobals());

const bm = (id: string, sessionId: string, extra: Partial<MessageBookmark> = {}): MessageBookmark => ({
  id, sessionId, role: 'User', preview: id, timeCreated: 1, ...extra,
});

it('builds previews from text, tool and file parts', () => {
  const messages = [{ id: 'm1', sessionId: 's1', timeCreated: 5, data: { role: 'assistant' } }] as unknown as Message[];
  const parts = [
    { messageId: 'm1', data: JSON.stringify({ type: 'text', text: '  hello\n world ' }) },
    { messageId: 'm1', data: { type: 'tool', tool: 'bash' } },
    { messageId: 'm1', data: { type: 'file', filename: 'a.ts' } },
    { messageId: 'm1', data: '{not json' },
    { messageId: 'm1', data: { type: 'step-start' } },
  ] as unknown as Part[];
  const got = buildMessageBookmarks(messages, parts, { sessionId: undefined, directory: '/src/repo', sessionTitle: '' }).get('m1');
  expect(got).toMatchObject({
    sessionId: 's1', role: 'Assistant', preview: 'hello world [tool: bash] [file: a.ts]',
    projectDirectory: '/src/repo', sessionTitle: 'Untitled',
  });
  expect(messageBookmarkKey(got!)).toBe('s1:m1');
});

it('persists, clears and loads bookmarks across sessions', () => {
  expect(loadMessageBookmarks(undefined)).toEqual([]);
  saveMessageBookmarks(undefined, [bm('x', 's0')]);
  saveMessageBookmarks('s1', [bm('a', '')]);
  saveMessageBookmarks('s2', [bm('b', 's2')]);
  localStorage.setItem('ocman:message-bookmarks:s3', '{"not":"array"}');
  localStorage.setItem('ocman:message-bookmarks:s4', 'broken');
  localStorage.setItem('unrelated', '[]');
  expect(loadMessageBookmarks('s1')).toEqual([bm('a', 's1')]);
  expect(loadMessageBookmarks('s3')).toEqual([]);
  expect(loadMessageBookmarks('s4')).toEqual([]);

  const active = [bm('live', 's1')];
  expect(loadAllMessageBookmarks('s1', active, 0).map((b) => b.id)).toEqual(['live', 'b']);

  saveMessageBookmarks('s2', []);
  expect(localStorage.getItem('ocman:message-bookmarks:s2')).toBeNull();
});

it('groups bookmarks by project with the current project first', () => {
  const sessions = [{ id: 's2', directory: '/src/.worktrees/beta/feat', title: '**Fix** it' }] as unknown as Session[];
  const groups = groupMessageBookmarks([
    bm('a', 's1', { directory: '/src/alpha' }),
    bm('b', 's2'),
    bm('c', 's9'),
  ], '/src/beta', sessions);
  expect(groups.map((g) => [g.label, g.current, g.bookmarks.length])).toEqual([
    ['src/beta', true, 1], ['src/alpha', false, 1], ['Unknown project', false, 1],
  ]);
  expect(groups[0].bookmarks[0].sessionTitle).toBe('Fix it');
});
