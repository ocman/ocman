// @vitest-environment jsdom

import { act, renderHook } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import type { SessionWarning } from '../../lib/api';
import type { SessionMetadata } from '../../lib/sessionReducer';
import { sessionWarningKey, useSessionWarnings } from './useSessionWarnings';

const w1 = { kind: 'port', message: 'busy', ports: [1, 2] } as unknown as SessionWarning;
const w2 = { kind: 'other', message: 'hmm' } as unknown as SessionWarning;
const session = { id: 's1', warnings: [w1, w2] } as SessionMetadata;

describe('useSessionWarnings', () => {
  it('keys warnings and hides dismissed ones', () => {
    expect(sessionWarningKey('s1', w1)).toBe('s1:port:busy:1,2');
    const { result, rerender } = renderHook(
      ({ s }: { s: SessionMetadata | null }) => useSessionWarnings(s),
      { initialProps: { s: session as SessionMetadata | null } },
    );
    expect(result.current.visibleSessionWarnings).toEqual([w1, w2]);
    act(() => result.current.dismissSessionWarning(w1));
    act(() => result.current.dismissSessionWarning(w1));
    expect(result.current.visibleSessionWarnings).toEqual([w2]);
    rerender({ s: null });
    expect(result.current.visibleSessionWarnings).toEqual([]);
  });
});
