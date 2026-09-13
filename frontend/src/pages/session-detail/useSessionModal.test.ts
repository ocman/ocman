// @vitest-environment jsdom

import { act, renderHook } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { useSessionModal } from './useSessionModal';

describe('useSessionModal', () => {
  it('holds one open modal and adapts boolean setters', () => {
    const { result } = renderHook(() => useSessionModal());
    expect(result.current.openModal).toBeNull();

    act(() => result.current.open('rename'));
    expect(result.current.openModal).toBe('rename');
    act(() => result.current.open('fork'));
    expect(result.current.openModal).toBe('fork');
    act(() => result.current.close());
    expect(result.current.openModal).toBeNull();

    const setRename = result.current.setterFor('rename');
    expect(result.current.setterFor('rename')).toBe(setRename);
    act(() => setRename(true));
    expect(result.current.openModal).toBe('rename');
    // Closing a modal that isn't the open one is a no-op.
    act(() => result.current.setterFor('fork')(false));
    expect(result.current.openModal).toBe('rename');
    act(() => setRename(false));
    expect(result.current.openModal).toBeNull();
  });
});
