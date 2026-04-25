import { describe, expect, it } from 'vitest';
import { filterVisibleSessions } from './sessionVisibility';

describe('filterVisibleSessions', () => {
  it('returns [] for null input', () => {
    // Regression: /api/sessions can serialize a Go nil slice as JSON
    // `null`. Reaching this helper used to crash with
    // `null is not an object (evaluating 'sessions.length')`.
    expect(filterVisibleSessions(null)).toEqual([]);
  });

  it('returns [] for undefined input', () => {
    expect(filterVisibleSessions(undefined)).toEqual([]);
  });

  it('returns the same array reference when empty', () => {
    const empty: { archived?: boolean }[] = [];
    expect(filterVisibleSessions(empty)).toBe(empty);
  });

  it('drops archived sessions', () => {
    const sessions = [
      { id: 'a', archived: false },
      { id: 'b', archived: true },
      { id: 'c' },
    ];
    expect(filterVisibleSessions(sessions).map(s => s.id)).toEqual(['a', 'c']);
  });

  it('keeps every session when none are archived', () => {
    const sessions: { id: string; archived?: boolean }[] = [
      { id: 'a' },
      { id: 'b', archived: false },
    ];
    expect(filterVisibleSessions(sessions)).toEqual(sessions);
  });
});
