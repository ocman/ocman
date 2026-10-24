// @vitest-environment jsdom
import { afterEach, expect, it, vi } from 'vitest';
import { readStorage, readStored, writeStorage } from './storage';

afterEach(() => {
  vi.restoreAllMocks();
  localStorage.clear();
});

it('reads, writes and removes values', () => {
  expect(readStorage('k')).toBeNull();
  expect(writeStorage('k', 'v')).toBe(true);
  expect(readStorage('k')).toBe('v');
  expect(writeStorage('k', null)).toBe(true);
  expect(localStorage.getItem('k')).toBeNull();
});

it('parses stored JSON and falls back when missing or invalid', () => {
  expect(readStored('k', { a: 0 })).toEqual({ a: 0 });
  localStorage.setItem('k', '{"a":1}');
  expect(readStored('k', { a: 0 })).toEqual({ a: 1 });
  expect(readStored('k', 0, (raw) => raw.length)).toBe(7);
  localStorage.setItem('k', '{bad');
  expect(readStored('k', { a: 0 })).toEqual({ a: 0 });
});

it('degrades when storage throws', () => {
  vi.spyOn(Storage.prototype, 'getItem').mockImplementation(() => { throw new Error('blocked'); });
  vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => { throw new Error('full'); });
  expect(readStorage('k')).toBeNull();
  expect(readStored('k', 'fallback')).toBe('fallback');
  expect(writeStorage('k', 'v')).toBe(false);
});
