import { describe, expect, it } from 'vitest';
import { isRecoverableThreadBoundaryError } from './threadBoundaryRecovery';

describe('isRecoverableThreadBoundaryError', () => {
  it('matches the legacy tapClientLookup crash signature', () => {
    expect(
      isRecoverableThreadBoundaryError(
        new Error('tapClientLookup: Index 1 out of bounds (length: 1)'),
      ),
    ).toBe(true);
  });

  it('matches the renamed useClientLookup crash signature (store 0.2.x)', () => {
    expect(
      isRecoverableThreadBoundaryError(
        new Error('useClientLookup: Index 1 out of bounds (length: 1)'),
      ),
    ).toBe(true);
  });

  it('matches the key-not-found variant', () => {
    expect(
      isRecoverableThreadBoundaryError(
        new Error('useClientLookup: Key "ses_abc" not found'),
      ),
    ).toBe(true);
  });

  it('ignores unrelated render errors', () => {
    expect(isRecoverableThreadBoundaryError(new Error('boom'))).toBe(false);
  });

  it('ignores unrelated "out of bounds" errors', () => {
    expect(
      isRecoverableThreadBoundaryError(
        new Error('Array index 5 out of bounds (length: 3)'),
      ),
    ).toBe(false);
  });
});
