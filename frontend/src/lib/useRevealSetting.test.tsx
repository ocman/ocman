// @vitest-environment jsdom
import { act, render } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { useRevealSetting } from './useRevealSetting';

function Probe({ late }: { late: boolean }) {
  useRevealSetting('bell-sound');
  return late ? null : <div id="setting-bell-sound" />;
}

afterEach(() => vi.useRealTimers());

describe('useRevealSetting', () => {
  it('highlights the row once it renders, then clears it', () => {
    vi.useFakeTimers();
    const { container, rerender } = render(<Probe late />);
    act(() => { vi.advanceTimersByTime(100); });
    rerender(<Probe late={false} />);
    act(() => { vi.advanceTimersByTime(60); });
    const row = container.querySelector('#setting-bell-sound')!;
    expect(row.classList.contains('settings-row--found')).toBe(true);
    act(() => { vi.advanceTimersByTime(2000); });
    expect(row.classList.contains('settings-row--found')).toBe(false);
  });

  it('gives up quietly when the row never appears', () => {
    vi.useFakeTimers();
    render(<Probe late />);
    act(() => { vi.advanceTimersByTime(2000); });
    expect(vi.getTimerCount()).toBe(0);
  });
});
