import { describe, expect, it } from 'vitest';
import { DELTA_FIELD_PATH, eventsAfter, formatCursor, OCE_VERSION, parseCursor, type OceEnvelope } from './events';

describe('OCE event helpers', () => {
  it('formats and parses cursors, keeping colons in the epoch', () => {
    const cursor = formatCursor({ epoch: 'owner:abc', seq: 42 });
    expect(cursor).toBe('owner:abc:42');
    expect(parseCursor(cursor)).toEqual({ epoch: 'owner:abc', seq: 42 });
  });

  it.each(['', ':1', 'e', 'e:x', 'e:-1', 'e:1.5', `e:${'9'.repeat(20)}`])('rejects cursor %j', (cursor) => {
    expect(parseCursor(cursor)).toBeNull();
  });

  it('maps every delta field to its PartData path', () => {
    expect(OCE_VERSION).toBe(1);
    expect(DELTA_FIELD_PATH).toEqual({ text: 'text', 'tool.input': 'state.raw', 'tool.output': 'state.output' });
  });

  it('keeps only events a read covering the cursor does not contain', () => {
    const env = (epoch: string, seq: number, type = 'part.delta') => ({ epoch, seq, event: { type } } as OceEnvelope);
    const fence = [env('old', 7), env('e', 2, 'session.updated'), env('e', 3), env('e', 4), env('new', 1)];
    const at = (list: OceEnvelope[]) => list.map((e) => `${e.epoch}:${e.seq}`);
    // Metadata and child events are not in the composed read; they always replay.
    expect(at(eventsAfter(fence, 'e:3'))).toEqual(['e:2', 'e:4', 'new:1']);
    // An unknown or missing cursor replays everything.
    expect(at(eventsAfter(fence, 'other:9'))).toEqual(at(fence));
    expect(at(eventsAfter(fence, undefined))).toEqual(at(fence));
    expect(at(eventsAfter(fence, 'bad'))).toEqual(at(fence));
  });
});
