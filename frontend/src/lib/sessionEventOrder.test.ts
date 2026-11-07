import { beforeEach, expect, it } from 'vitest';
import { acceptSessionEvent, isCompactSessionSnapshot, resetSessionEventOrder } from './sessionEventOrder';
import type { Session } from './api';

beforeEach(resetSessionEventOrder);
it('rejects stale and duplicate snapshots without conflating owners', () => {
  const event = { sessionID: 's', platform: 'local', revision: 2 };
  expect(acceptSessionEvent(event)).toBe(true);
  expect(acceptSessionEvent(event)).toBe(false);
  expect(acceptSessionEvent({ ...event, revision: 1 })).toBe(false);
  expect(acceptSessionEvent({ ...event, platform: 'r-owner:test' })).toBe(true);
  resetSessionEventOrder();
  expect(acceptSessionEvent({ ...event, revision: 1 })).toBe(true);
});
it('keeps legacy invalidations and rejects malformed versioned identities', () => {
  expect(acceptSessionEvent({ sessionID: 's' })).toBe(true);
  for (const revision of [0, -1, NaN, Infinity, 1.5]) expect(acceptSessionEvent({ platform: 'p', sessionID: 's', revision })).toBe(false);
  expect(acceptSessionEvent({ sessionID: 's', revision: 1 })).toBe(false);
  expect(acceptSessionEvent({ platform: 'p', revision: 1 })).toBe(false);
  for (let i = 0; i < 5000; i++) expect(acceptSessionEvent({ platform: 'p', sessionID: String(i), revision: 1 })).toBe(true);
});
it('distinguishes complete compact rows from provisional creation and partial patches', () => {
  const row = { id: 's', platform: 'p' } as Session;
  expect(isCompactSessionSnapshot(row, row)).toBe(true);
  expect(isCompactSessionSnapshot(row)).toBe(false);
  expect(isCompactSessionSnapshot(row, { title: 'partial' })).toBe(false);
  expect(isCompactSessionSnapshot(row, { ...row, platform: 'other' })).toBe(false);
  expect(isCompactSessionSnapshot()).toBe(false);
});
