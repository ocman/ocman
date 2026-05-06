import { describe, expect, it } from 'vitest';
import { isRecoverableThreadBoundaryError } from './threadBoundaryRecovery';

describe('isRecoverableThreadBoundaryError', () => {
  it('matches the transient assistant-ui lookup crash signature', () => {
    expect(
      isRecoverableThreadBoundaryError(
        new Error('tapClientLookup: Index 1 out of bounds (length: 1)'),
      ),
    ).toBe(true);
  });

  it('ignores unrelated render errors', () => {
    expect(isRecoverableThreadBoundaryError(new Error('boom'))).toBe(false);
  });
});
