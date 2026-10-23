// @vitest-environment jsdom

import { act, renderHook } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { useSessionModal } from './useSessionModal';

describe('useSessionModal', () => {
  it('holds one open modal at a time and closes it', () => {
    const { result } = renderHook(() => useSessionModal());
    expect(result.current.openModal).toBeNull();

    act(() => result.current.open('rename'));
    expect(result.current.openModal).toBe('rename');
    act(() => result.current.open('fork'));
    expect(result.current.openModal).toBe('fork');
    act(() => result.current.close());
    expect(result.current.openModal).toBeNull();

  });
});
