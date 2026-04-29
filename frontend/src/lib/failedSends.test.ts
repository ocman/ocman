import { describe, it, expect, beforeEach } from 'vitest';
import {
  listFailedSends,
  recordFailedSend,
  removeFailedSend,
  clearFailedSends,
  type FailedSend,
} from './failedSends';

const SESSION = 'ses_test';

// Lightweight localStorage stub so this suite doesn't depend on jsdom
// (the project intentionally runs vitest without a DOM environment — see
// usePwaInstall.test.ts / linkHardener.test.ts).
function installLocalStorageStub() {
  const data = new Map<string, string>();
  const stub = {
    getItem: (k: string) => (data.has(k) ? data.get(k)! : null),
    setItem: (k: string, v: string) => { data.set(k, String(v)); },
    removeItem: (k: string) => { data.delete(k); },
    clear: () => { data.clear(); },
    key: (i: number) => Array.from(data.keys())[i] ?? null,
    get length() { return data.size; },
  };
  // Plant `window.localStorage`. Both globals are read by the module under
  // test (`typeof window === 'undefined'` short-circuits in node-only
  // environments).
  (globalThis as unknown as { window: unknown }).window = { localStorage: stub };
  return stub;
}

function makeEntry(over: Partial<FailedSend> = {}): FailedSend {
  return {
    id: 'fs-1',
    text: 'hello',
    failedAt: 1000,
    error: 'boom',
    ...over,
  };
}

describe('failedSends storage', () => {
  let storage: ReturnType<typeof installLocalStorageStub>;
  beforeEach(() => {
    storage = installLocalStorageStub();
  });

  it('returns an empty array when nothing is stored', () => {
    expect(listFailedSends(SESSION)).toEqual([]);
  });

  it('persists a recorded entry across calls', () => {
    recordFailedSend(SESSION, makeEntry());
    expect(listFailedSends(SESSION)).toEqual([makeEntry()]);
  });

  it('appends new entries after existing ones', () => {
    recordFailedSend(SESSION, makeEntry({ id: 'a', text: 'first' }));
    recordFailedSend(SESSION, makeEntry({ id: 'b', text: 'second' }));
    const list = listFailedSends(SESSION);
    expect(list.map((e) => e.id)).toEqual(['a', 'b']);
  });

  it('replaces an existing entry with the same id', () => {
    recordFailedSend(SESSION, makeEntry({ id: 'a', text: 'first', error: 'e1' }));
    recordFailedSend(SESSION, makeEntry({ id: 'a', text: 'first', error: 'e2' }));
    const list = listFailedSends(SESSION);
    expect(list).toHaveLength(1);
    expect(list[0].error).toBe('e2');
  });

  it('removes a single entry by id', () => {
    recordFailedSend(SESSION, makeEntry({ id: 'a' }));
    recordFailedSend(SESSION, makeEntry({ id: 'b' }));
    removeFailedSend(SESSION, 'a');
    expect(listFailedSends(SESSION).map((e) => e.id)).toEqual(['b']);
  });

  it('clears all entries for a session', () => {
    recordFailedSend(SESSION, makeEntry({ id: 'a' }));
    recordFailedSend(SESSION, makeEntry({ id: 'b' }));
    clearFailedSends(SESSION);
    expect(listFailedSends(SESSION)).toEqual([]);
  });

  it('keeps entries scoped per session', () => {
    recordFailedSend('s1', makeEntry({ id: 'a' }));
    recordFailedSend('s2', makeEntry({ id: 'b' }));
    expect(listFailedSends('s1').map((e) => e.id)).toEqual(['a']);
    expect(listFailedSends('s2').map((e) => e.id)).toEqual(['b']);
  });

  it('preserves images on a recorded entry', () => {
    const images = [{ url: 'data:image/png;base64,AAA', mime: 'image/png' }];
    recordFailedSend(SESSION, makeEntry({ images }));
    expect(listFailedSends(SESSION)[0].images).toEqual(images);
  });

  it('drops images and marks imagesDropped when the entry exceeds the size cap', () => {
    // Build a >4MB data URL by repeating a chunk.
    const big = 'a'.repeat(5 * 1024 * 1024);
    const images = [{ url: `data:image/png;base64,${big}`, mime: 'image/png' }];
    recordFailedSend(SESSION, makeEntry({ images }));
    const list = listFailedSends(SESSION);
    expect(list).toHaveLength(1);
    expect(list[0].images).toBeUndefined();
    expect(list[0].imagesDropped).toBe(true);
    expect(list[0].text).toBe('hello');
  });

  it('survives malformed json in storage', () => {
    storage.setItem('ocman.failedSends.v1', 'not json');
    expect(listFailedSends(SESSION)).toEqual([]);
    // And a subsequent record should still write cleanly.
    recordFailedSend(SESSION, makeEntry());
    expect(listFailedSends(SESSION)).toHaveLength(1);
  });

  it('round-trips selection metadata (model, agent, reasoning)', () => {
    recordFailedSend(
      SESSION,
      makeEntry({ model: 'anthropic/claude-opus-4-7', agent: 'build', reasoning: 'high' }),
    );
    const e = listFailedSends(SESSION)[0];
    expect(e.model).toBe('anthropic/claude-opus-4-7');
    expect(e.agent).toBe('build');
    expect(e.reasoning).toBe('high');
  });
});
