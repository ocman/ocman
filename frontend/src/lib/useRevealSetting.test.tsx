// @vitest-environment jsdom
import { act, render } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { useRevealSetting, type RevealRequest } from './useRevealSetting';

const first: RevealRequest = { id: 'bell-sound', seq: 1 };

function Probe({ late, request = first }: { late: boolean; request?: RevealRequest }) {
  const found = useRevealSetting(request);
  return late ? <p>{String(found)}</p> : <div id="setting-bell-sound">{String(found)}</div>;
}

afterEach(() => vi.useRealTimers());

describe('useRevealSetting', () => {
  it('highlights the row once it renders, then clears it', async () => {
    vi.useFakeTimers();
    const { container, rerender } = render(<Probe late />);
    act(() => { vi.advanceTimersByTime(100); });
    await act(async () => { rerender(<Probe late={false} />); });
    const row = container.querySelector('#setting-bell-sound')!;
    expect(row.classList.contains('settings-row--found')).toBe(true);
    act(() => { vi.advanceTimersByTime(2000); });
    expect(row.classList.contains('settings-row--found')).toBe(false);
  });

  it('reveals a row that only renders after its slow fetches resolve', async () => {
    vi.useFakeTimers();
    const { container, rerender } = render(<Probe late />);
    act(() => { vi.advanceTimersByTime(5000); });
    expect(container).toHaveTextContent('false');
    await act(async () => { rerender(<Probe late={false} />); });
    const row = container.querySelector('#setting-bell-sound')!;
    expect(row.classList.contains('settings-row--found')).toBe(true);
    expect(row).toHaveTextContent('true');
  });

  it('reveals again when the same setting is picked a second time', () => {
    vi.useFakeTimers();
    const { container, rerender } = render(<Probe late={false} />);
    const row = container.querySelector('#setting-bell-sound')!;
    act(() => { vi.advanceTimersByTime(2000); });
    expect(row.classList.contains('settings-row--found')).toBe(false);
    rerender(<Probe late={false} request={{ id: 'bell-sound', seq: 2 }} />);
    expect(row.classList.contains('settings-row--found')).toBe(true);
  });

  it('stops watching once unmounted', () => {
    vi.useFakeTimers();
    const { unmount } = render(<Probe late />);
    unmount();
    expect(vi.getTimerCount()).toBe(0);
  });
});
