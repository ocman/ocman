// @vitest-environment jsdom
import { act, render, screen } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { useRevealSetting, type RevealRequest } from './useRevealSetting';
import { SettingRow } from '../components/SettingRow';

const first: RevealRequest = { id: 'bell-sound', seq: 1 };

function Probe({ late, request = first, scrollIntoView }: { late: boolean; request?: RevealRequest; scrollIntoView?: HTMLElement['scrollIntoView'] }) {
  const found = useRevealSetting(request);
  return late ? <p>{String(found)}</p> : <div id="setting-bell-sound" ref={(row) => {
    if (row && scrollIntoView) row.scrollIntoView = scrollIntoView;
  }}>{String(found)}</div>;
}

afterEach(() => {
  vi.useRealTimers();
  vi.unstubAllGlobals();
});

describe('useRevealSetting', () => {
  it('focuses the named row when its control is disabled', () => {
    function DisabledSetting() {
      useRevealSetting(first);
      return <SettingRow setting="bell-sound"><input type="checkbox" aria-label="Bell sound" disabled /></SettingRow>;
    }
    render(<DisabledSetting />);
    expect(screen.getByRole('group', { name: 'Bell sound' })).toHaveFocus();
  });

  it.each([[true, 'instant'], [false, 'smooth']])('reveals with reduced motion %s using %s scrolling', async (reduced, behavior) => {
    const matchMedia = vi.fn(() => ({ matches: reduced }));
    const scrollIntoView = vi.fn();
    vi.stubGlobal('matchMedia', matchMedia);
    render(<Probe late={false} scrollIntoView={scrollIntoView} />);
    expect(scrollIntoView).toHaveBeenCalledWith({ block: 'center', behavior });
    expect(matchMedia).toHaveBeenCalledWith('(prefers-reduced-motion: reduce)');
  });

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
