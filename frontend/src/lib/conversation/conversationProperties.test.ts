// Reducer properties from design §13.3, over every golden fixture and over
// seeded random delta streams. Snapshot equivalence (3) needs the Go-side
// statev2 conversion and compaction safety (4) is a hub test.
import { afterAll, beforeAll, describe, expect, it, vi } from 'vitest';
import { comparable, loadFixtures, seedView } from '../../test/oceFixtures';
import { decode } from '../sessionViewParts';
import type { SessionView } from '../sessionViewTypes';
import { reduceConversation } from './conversationReducer';
import type { OceEnvelope, OceEvent } from './events';

// Referenced in place, never copied (design §13.2).
const FIXTURES = loadFixtures(import.meta.glob('../../../../internal/convevents/v1map/testdata/*/*.oce.jsonl', {
  query: '?raw', import: 'default', eager: true,
}) as Record<string, string>);

/** mulberry32: small, seeded, deterministic. */
function rng(seed: number) {
  let a = seed >>> 0;
  return () => {
    a = (a + 0x6d2b79f5) >>> 0;
    let t = a;
    t = Math.imul(t ^ (t >>> 15), t | 1);
    t ^= t + Math.imul(t ^ (t >>> 7), t | 61);
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

const fold = (view: SessionView, envs: readonly OceEnvelope[]) => reduceConversation(view, envs);

beforeAll(() => {
  vi.useFakeTimers();
  vi.setSystemTime(1_700_000_000_000);
});
afterAll(() => vi.useRealTimers());

describe.each(FIXTURES.map((f) => [f.name, f] as const))('%s', (_name, fx) => {
  const seed = seedView(fx.sessionId);
  const whole = () => comparable(fold(seed, fx.envelopes));

  it('folds to the same view for any partition into batches', () => {
    const expected = whole();
    const random = rng(fx.envelopes.length * 7919 + 1);
    for (let round = 0; round < 50; round++) {
      let view = seed;
      for (let i = 0; i < fx.envelopes.length;) {
        const size = 1 + Math.floor(random() * 6);
        view = fold(view, fx.envelopes.slice(i, i + size));
        i += size;
      }
      expect(comparable(view)).toEqual(expected);
    }
  });

  it('folds to the same view after any overlapping resume', () => {
    const expected = whole();
    const n = fx.envelopes.length;
    // Folded through E[k-1]; a replay resumes anywhere up to E[k].
    for (let k = 0; k <= n; k++) {
      const prefix = fold(seed, fx.envelopes.slice(0, k));
      for (let j = 0; j <= k; j++) {
        expect(comparable(fold(prefix, fx.envelopes.slice(j)))).toEqual(expected);
      }
    }
  });

  it('ignores envelopes for another session', () => {
    const view = fold(seed, fx.envelopes);
    const foreign = fx.envelopes.map((env) => ({ ...env, sessionId: 'someone-else' }));
    expect(fold(view, foreign)).toBe(view);
  });
});

const ALPHABET = ['a', 'b', ' ', '日', '本', '📁', '\n', 'é', 'z'];
function randomText(random: () => number, max: number) {
  let out = '';
  const length = Math.floor(random() * max);
  for (let i = 0; i < length; i++) out += ALPHABET[Math.floor(random() * ALPHABET.length)];
  return out;
}

const env = (event: OceEvent, seq: number): OceEnvelope => ({
  v: 1, epoch: 'prop', seq, sessionId: 's', platform: 'opencode', at: 0, durability: 'ephemeral', event,
});

/** Chunk `text` into deltas, with random replays of earlier chunks and occasional gaps. */
function deltaStream(random: () => number, text: string, gaps: boolean): OceEnvelope[] {
  const out: OceEnvelope[] = [];
  let offset = 0;
  let seq = 0;
  while (offset < text.length) {
    const size = 1 + Math.floor(random() * 5);
    const chunk = text.slice(offset, offset + size);
    const replay = Math.max(0, offset - Math.floor(random() * 4));
    if (random() < 0.2) out.push(env({ type: 'part.delta', messageId: 'm', partId: 'p', field: 'text', offset: replay, text: text.slice(replay, offset + size), partType: 'text' }, ++seq));
    else if (gaps && random() < 0.1) out.push(env({ type: 'part.delta', messageId: 'm', partId: 'p', field: 'text', offset: offset + 3, text: 'gap', partType: 'text' }, ++seq));
    out.push(env({ type: 'part.delta', messageId: 'm', partId: 'p', field: 'text', offset, text: chunk, partType: 'text' }, ++seq));
    offset += chunk.length;
  }
  return out;
}

const textOf = (view: SessionView) => decode(view.parts.find((p) => p.id === 'p')!).text;
const upsert = (text: string, final: boolean, seq: number) => env({
  type: 'part.upserted', final, part: { id: 'p', messageId: 'm', sessionId: 's', data: { type: 'text', text } },
}, seq);

describe('delta streams', () => {
  it('accumulate exactly the streamed text, with replays and in any batching', () => {
    const random = rng(42);
    for (let round = 0; round < 200; round++) {
      const text = randomText(random, 40) || 'x';
      const stream = deltaStream(random, text, false);
      let view = seedView('s');
      for (let i = 0; i < stream.length;) {
        const size = 1 + Math.floor(random() * 8);
        view = fold(view, stream.slice(i, i + size));
        i += size;
      }
      expect(textOf(view)).toBe(text);
    }
  });

  it('end with exactly the final text, whatever came before', () => {
    const random = rng(7);
    for (let round = 0; round < 200; round++) {
      const streamed = randomText(random, 40) || 'x';
      const final = randomText(random, 40);
      const stream = deltaStream(random, streamed, true);
      const view = fold(seedView('s'), [...stream, upsert(final, true, stream.length + 1)]);
      expect(textOf(view)).toBe(final);
      expect(view._stale?.has('p') ?? false).toBe(false);
    }
  });

  it('are never shortened by a non-final upsert', () => {
    const random = rng(99);
    for (let round = 0; round < 200; round++) {
      const streamed = randomText(random, 40) || 'x';
      const stream = deltaStream(random, streamed, false);
      const cut = Math.floor(random() * (streamed.length + 1));
      const view = fold(seedView('s'), [...stream, upsert(streamed.slice(0, cut), false, stream.length + 1)]);
      expect(textOf(view)).toBe(streamed);
      const longer = `${streamed}${randomText(random, 5)}`;
      expect(textOf(fold(view, [upsert(longer, false, stream.length + 2)]))).toBe(longer);
    }
  });
});
