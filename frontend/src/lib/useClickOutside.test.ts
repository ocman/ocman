// @vitest-environment jsdom
import { fireEvent, renderHook } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import type { RefObject } from 'react';
import { useClickOutside } from './useClickOutside';

describe('useClickOutside', () => {
  it('handles inside, outside, enablement, current callbacks, and cleanup', () => {
    const inside = document.createElement('div');
    const child = inside.appendChild(document.createElement('button'));
    const outside = document.createElement('button');
    document.body.append(inside, outside);
    const ref = { current: inside } as RefObject<HTMLElement>;
    const first = vi.fn();
    const latest = vi.fn();

    const { rerender, unmount } = renderHook(
      ({ enabled, onOutside }) => useClickOutside(ref, enabled, onOutside),
      { initialProps: { enabled: true, onOutside: first } },
    );

    fireEvent.mouseDown(child);
    expect(first).not.toHaveBeenCalled();
    fireEvent.mouseDown(outside);
    expect(first).toHaveBeenCalledOnce();

    rerender({ enabled: true, onOutside: latest });
    fireEvent.mouseDown(outside);
    expect(latest).toHaveBeenCalledOnce();

    rerender({ enabled: false, onOutside: latest });
    fireEvent.mouseDown(outside);
    expect(latest).toHaveBeenCalledOnce();

    rerender({ enabled: true, onOutside: latest });
    unmount();
    fireEvent.mouseDown(outside);
    expect(latest).toHaveBeenCalledOnce();
    inside.remove();
    outside.remove();
  });
});
