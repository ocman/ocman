import { afterEach, describe, expect, it, vi } from 'vitest';
import { randomId } from './randomId';

describe('randomId', () => {
  afterEach(() => vi.unstubAllGlobals());

  it('falls back to getRandomValues outside a secure context', () => {
    vi.stubGlobal('crypto', { getRandomValues: (a: Uint32Array) => a.fill(255) });
    expect(randomId()).toBe('000000ff'.repeat(4));
  });
});
