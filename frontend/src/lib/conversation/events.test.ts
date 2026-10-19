import { describe, expect, it } from 'vitest';
import { DELTA_FIELD_PATH, formatCursor, OCE_VERSION, parseCursor } from './events';

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
});
